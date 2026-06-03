package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// AccountRevokeNotifier 是「账号 revoke 自动告警」的通知接口。
// RateLimitService / TokenRefreshService 在确认账号被永久禁用（SetError）后调用，
// 由实现负责解析 owner、发邮件 + IM webhook，并附上成员自助重授权链接。
//
// 接口而非具体类型，便于在单测中替换，也便于可选注入（nil-safe）。
type AccountRevokeNotifier interface {
	OnAccountRevoked(account *Account, reason string)
}

const (
	// accountRevokeNotifyDeliveryPrefix revoke 告警去重标记前缀。
	accountRevokeNotifyDeliveryPrefix = "account_revoke_notify:"
	// accountRevokeNotifyDefaultTTLHours 同一账号 revoke 告警默认去重窗口（小时）。
	accountRevokeNotifyDefaultTTLHours = 6
	// accountRevokeReauthPath 前端重授权落地页路径。
	accountRevokeReauthPath = "/account/reauth"
)

// AccountRevokeNotifierImpl 是 AccountRevokeNotifier 的默认实现。
type AccountRevokeNotifierImpl struct {
	settingRepo              SettingRepository
	notificationEmailService *NotificationEmailService
	imWebhookNotifier        *IMWebhookNotifier
	reauthService            *AccountReauthService
	// reauthCache 可选 Redis 缓存（带 TTL）。为 nil 时退回 settingRepo（无 TTL）方案。
	reauthCache AccountReauthCache
}

// NewAccountRevokeNotifier 创建 revoke 告警通知器。
func NewAccountRevokeNotifier(
	settingRepo SettingRepository,
	notificationEmailService *NotificationEmailService,
	imWebhookNotifier *IMWebhookNotifier,
	reauthService *AccountReauthService,
	reauthCache AccountReauthCache,
) *AccountRevokeNotifierImpl {
	return &AccountRevokeNotifierImpl{
		settingRepo:              settingRepo,
		notificationEmailService: notificationEmailService,
		imWebhookNotifier:        imWebhookNotifier,
		reauthService:            reauthService,
		reauthCache:              reauthCache,
	}
}

// OnAccountRevoked 异步处理 revoke 告警：发邮件 + IM webhook（带去重）。
// 入参 account 可能来自调度缓存（字段不全），仅依赖 ID/Name/Platform/Extra，安全。
func (n *AccountRevokeNotifierImpl) OnAccountRevoked(account *Account, reason string) {
	if n == nil || account == nil || account.ID <= 0 {
		return
	}
	// 仅对 OpenAI（codex）账号触发自助重授权流程；其余平台不发（避免误导）。
	if account.Platform != PlatformOpenAI {
		return
	}
	// 异步执行，避免阻塞 SetError 后的热路径。
	accountID := account.ID
	accountName := account.Name
	platform := account.Platform
	ownerEmail := account.GetOwnerEmail()
	ownerName := account.GetOwnerName()
	go n.dispatch(accountID, accountName, platform, ownerEmail, ownerName, reason)
}

func (n *AccountRevokeNotifierImpl) dispatch(accountID int64, accountName, platform, ownerEmail, ownerName, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 去重：同一账号在 TTL 窗口内只告警一次。
	deliveryKey := accountRevokeNotifyDeliveryPrefix + strconv.FormatInt(accountID, 10)
	dedupeTTL := time.Duration(n.notifyTTLHours(ctx)) * time.Hour
	if n.alreadyNotified(ctx, deliveryKey, dedupeTTL) {
		return
	}

	triggeredAt := time.Now().Format("2006-01-02 15:04:05")
	if reason == "" {
		reason = "account authentication revoked"
	}

	// 生成一次性重授权链接（owner_email 可能为空，仍生成链接由站长转交）。
	reauthURL := n.buildReauthURL(ctx, accountID, ownerEmail)

	// 1) 邮件：owner_email（若有）+ 站长通知邮箱。
	recipients := n.resolveEmailRecipients(ctx, ownerEmail)
	displayOwnerName := ownerName
	if displayOwnerName == "" {
		displayOwnerName = accountName
	}
	for _, to := range recipients {
		n.sendReauthEmail(ctx, to, accountName, platform, displayOwnerName, reason, reauthURL, triggeredAt)
	}

	// 2) IM webhook：纯文本告警。
	n.sendIMWebhook(ctx, accountName, platform, displayOwnerName, reason, reauthURL, triggeredAt)

	// 标记已告警（去重窗口）。
	n.markNotified(ctx, deliveryKey, dedupeTTL)
}

func (n *AccountRevokeNotifierImpl) sendReauthEmail(ctx context.Context, to, accountName, platform, ownerName, reason, reauthURL, triggeredAt string) {
	if n.notificationEmailService == nil || strings.TrimSpace(to) == "" {
		return
	}
	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err := n.notificationEmailService.Send(sendCtx, NotificationEmailSendInput{
		Event:          NotificationEmailEventAccountReauthRequest,
		RecipientEmail: to,
		RecipientName:  emailRecipientName(to),
		SourceType:     "account_revoke",
		SourceID:       accountName,
		Variables: map[string]string{
			"account_name":  accountName,
			"platform":      platform,
			"owner_name":    ownerName,
			"revoke_reason": reason,
			"reauth_url":    reauthURL,
			"triggered_at":  triggeredAt,
		},
	})
	if err != nil {
		slog.Warn("account_revoke_email_failed", "to", to, "account", accountName, "error", err)
	}
}

func (n *AccountRevokeNotifierImpl) sendIMWebhook(ctx context.Context, accountName, platform, ownerName, reason, reauthURL, triggeredAt string) {
	if n.imWebhookNotifier == nil {
		return
	}
	siteName := n.siteName(ctx)
	title := fmt.Sprintf("[%s] 账号需要重新授权 / Account re-authorization required", siteName)
	var b strings.Builder
	fmt.Fprintf(&b, "账号 / Account: %s\n", accountName)
	fmt.Fprintf(&b, "平台 / Platform: %s\n", platform)
	if ownerName != "" {
		fmt.Fprintf(&b, "归属成员 / Owner: %s\n", ownerName)
	}
	fmt.Fprintf(&b, "原因 / Reason: %s\n", reason)
	fmt.Fprintf(&b, "检测时间 / Time: %s\n", triggeredAt)
	if reauthURL != "" {
		fmt.Fprintf(&b, "重授权链接 / Re-authorize (24h, one-time): %s", reauthURL)
	}
	n.imWebhookNotifier.Notify(ctx, title, b.String())
}

// buildReauthURL 生成成员自助重授权链接 <frontend>/account/reauth?token=xxx。
// reauthService 不可用或签名失败时返回空串（仍发告警，由站长手动处理）。
func (n *AccountRevokeNotifierImpl) buildReauthURL(ctx context.Context, accountID int64, ownerEmail string) string {
	if n.reauthService == nil {
		return ""
	}
	token, err := n.reauthService.SignReauthToken(ctx, accountID, ownerEmail)
	if err != nil {
		slog.Warn("account_revoke_sign_token_failed", "account_id", accountID, "error", err)
		return ""
	}
	base := n.baseURL(ctx)
	path := accountRevokeReauthPath + "?token=" + url.QueryEscape(token)
	if base == "" {
		return path
	}
	return base + path
}

// resolveEmailRecipients 收集 owner_email + 站长通知邮箱（去重）。
func (n *AccountRevokeNotifierImpl) resolveEmailRecipients(ctx context.Context, ownerEmail string) []string {
	seen := make(map[string]struct{})
	var recipients []string
	add := func(email string) {
		email = strings.TrimSpace(strings.ToLower(email))
		if email == "" {
			return
		}
		if _, ok := seen[email]; ok {
			return
		}
		seen[email] = struct{}{}
		recipients = append(recipients, email)
	}
	add(ownerEmail)
	// 站长通知邮箱复用账号限额告警的收件人配置。
	if n.settingRepo != nil {
		if raw, err := n.settingRepo.GetValue(ctx, SettingKeyAccountQuotaNotifyEmails); err == nil && strings.TrimSpace(raw) != "" && raw != "[]" {
			for _, entry := range filterVerifiedEmails(ParseNotifyEmails(raw)) {
				add(entry)
			}
		}
	}
	return recipients
}

// alreadyNotified 判断某账号是否在去重窗口内已告警。
//
// P2-3：优先用 Redis（key 带去重窗口 TTL，自动过期清理，避免 settings 无限增长）。
// 去重是「锦上添花」而非安全关键，故 Redis 故障时 fail-open（当作未告警，宁可重发也不漏发）。
func (n *AccountRevokeNotifierImpl) alreadyNotified(ctx context.Context, key string, ttl time.Duration) bool {
	if n.reauthCache != nil {
		notified, err := n.reauthCache.IsNotified(ctx, key)
		if err == nil {
			return notified
		}
		// Redis 故障：fail-open。
		slog.Warn("account_revoke_notify_dedupe_redis_failed", "error", err)
		return false
	}
	return n.alreadyNotifiedFromSettings(ctx, key, ttl)
}

func (n *AccountRevokeNotifierImpl) alreadyNotifiedFromSettings(ctx context.Context, key string, ttl time.Duration) bool {
	if n.settingRepo == nil {
		return false
	}
	raw, err := n.settingRepo.GetValue(ctx, key)
	if err != nil {
		// 不存在或读失败：当作未告警（fail-open，宁可重发也不漏发）。
		return false
	}
	if strings.TrimSpace(raw) == "" {
		return false
	}
	// raw 是上次告警的 RFC3339 时间；超过 TTL 窗口则视为可再次告警。
	last, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	if parseErr != nil {
		return false
	}
	return time.Since(last) < ttl
}

func (n *AccountRevokeNotifierImpl) markNotified(ctx context.Context, key string, ttl time.Duration) {
	if n.reauthCache != nil {
		if err := n.reauthCache.MarkNotified(ctx, key, ttl); err == nil {
			return
		} else {
			slog.Warn("account_revoke_notify_mark_redis_failed", "error", err)
			// 回退写 settings。
		}
	}
	if n.settingRepo == nil {
		return
	}
	_ = n.settingRepo.Set(ctx, key, time.Now().UTC().Format(time.RFC3339Nano))
}

func (n *AccountRevokeNotifierImpl) notifyTTLHours(ctx context.Context) int {
	if n.settingRepo == nil {
		return accountRevokeNotifyDefaultTTLHours
	}
	raw, err := n.settingRepo.GetValue(ctx, SettingKeyAccountReauthNotifyTTLH)
	if err != nil || strings.TrimSpace(raw) == "" {
		return accountRevokeNotifyDefaultTTLHours
	}
	hours, parseErr := strconv.Atoi(strings.TrimSpace(raw))
	if parseErr != nil || hours <= 0 {
		return accountRevokeNotifyDefaultTTLHours
	}
	return hours
}

func (n *AccountRevokeNotifierImpl) baseURL(ctx context.Context) string {
	if n.settingRepo == nil {
		return ""
	}
	for _, key := range []string{SettingKeyFrontendURL, SettingKeyAPIBaseURL} {
		value, err := n.settingRepo.GetValue(ctx, key)
		if err == nil && strings.TrimSpace(value) != "" {
			return strings.TrimRight(strings.TrimSpace(value), "/")
		}
	}
	return ""
}

func (n *AccountRevokeNotifierImpl) siteName(ctx context.Context) string {
	if n.settingRepo == nil {
		return defaultSiteName
	}
	name, err := n.settingRepo.GetValue(ctx, SettingKeySiteName)
	if err != nil || strings.TrimSpace(name) == "" {
		return defaultSiteName
	}
	return strings.TrimSpace(name)
}
