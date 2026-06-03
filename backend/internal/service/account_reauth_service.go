package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// 成员自助重授权服务（路线乙：一次性签名链接）。
//
// 团队成员无需注册 sub2api。codex 账号被 revoke 后，notifier 会向 owner 邮箱 + 站长发送一封
// 含「专属签名链接」的通知，成员点击链接即可在浏览器中完成 OAuth 重授权，刷新该账号的凭证。
//
// 安全模型（务必保持）：
//   - 短 TTL 24h：token 过期即失效；
//   - 一次性消费：成功 ExchangeReauthCode 后写 deliveryKey 让 token 失效，防重放；
//   - 绑定单一 account_id：token 内锁定 account_id，重授权只能更新该账号；
//   - HMAC 签名：secret 仿 unsubscribeSecret，存 settings 自动生成；
//   - 只放行 credentials/extra 更新字段：不新建账号、不改 group/proxy/owner，防越权；
//   - 继承账号原 ProxyID：避免授权 IP 与运行 IP 不一致触发上游风控。

const (
	// accountReauthTokenTTL 签名链接有效期。
	accountReauthTokenTTL = 24 * time.Hour
	// accountReauthConsumedKeyPrefix 一次性消费标记的 settings key 前缀。
	accountReauthConsumedKeyPrefix = "account_reauth_consumed:"
)

// AccountReauthService 处理成员自助重授权的签名 token、OAuth 发起与凭证回写。
type AccountReauthService struct {
	accountRepo           AccountRepository
	settingRepo           SettingRepository
	openaiOAuthService    *OpenAIOAuthService
	tokenCacheInvalidator TokenCacheInvalidator
	// revokeNotifier 用于成功重授权后清理 runtime block（可选）。
	runtimeBlocker AccountRuntimeBlocker
}

// NewAccountReauthService 创建成员自助重授权服务。
func NewAccountReauthService(
	accountRepo AccountRepository,
	settingRepo SettingRepository,
	openaiOAuthService *OpenAIOAuthService,
	tokenCacheInvalidator TokenCacheInvalidator,
) *AccountReauthService {
	return &AccountReauthService{
		accountRepo:           accountRepo,
		settingRepo:           settingRepo,
		openaiOAuthService:    openaiOAuthService,
		tokenCacheInvalidator: tokenCacheInvalidator,
	}
}

// SetAccountRuntimeBlocker 注入运行时调度封锁器（可选，重授权成功后清理 block）。
func (s *AccountReauthService) SetAccountRuntimeBlocker(blocker AccountRuntimeBlocker) {
	s.runtimeBlocker = blocker
}

// accountReauthClaims 是签名 token 的载荷。
type accountReauthClaims struct {
	AccountID  int64  `json:"account_id"`
	OwnerEmail string `json:"owner_email"`
	Exp        int64  `json:"exp"`
	// Nonce 保证同一账号多次 revoke 生成的 token 互不相同，使一次性消费可按 token 维度精确去重。
	Nonce string `json:"nonce"`
}

// AccountReauthInfo 是脱敏后的账号信息，用于重授权落地页展示。
type AccountReauthInfo struct {
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name"`
	Platform    string `json:"platform"`
	OwnerName   string `json:"owner_name"`
	OwnerEmail  string `json:"owner_email"`
	ExpiresAt   int64  `json:"expires_at"` // token 过期时间（Unix 秒）
}

// AccountReauthURLResult 是发起 OAuth 后返回的授权 URL + 会话。
type AccountReauthURLResult struct {
	AuthURL   string `json:"auth_url"`
	SessionID string `json:"session_id"`
}

// SignReauthToken 为指定账号生成一次性签名 token。
// 调用方（notifier）负责把 token 拼进重授权链接。
func (s *AccountReauthService) SignReauthToken(ctx context.Context, accountID int64, ownerEmail string) (string, error) {
	if accountID <= 0 {
		return "", errors.New("invalid account id")
	}
	secret, err := s.reauthSecret(ctx)
	if err != nil {
		return "", err
	}
	nonce, err := randomNonce()
	if err != nil {
		return "", err
	}
	claims := accountReauthClaims{
		AccountID:  accountID,
		OwnerEmail: strings.TrimSpace(strings.ToLower(ownerEmail)),
		Exp:        time.Now().Add(accountReauthTokenTTL).Unix(),
		Nonce:      nonce,
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signature := signNotificationEmailToken(secret, encodedPayload)
	return encodedPayload + "." + signature, nil
}

// ParseReauthToken 验签 + 校验过期 + 校验是否已被一次性消费。
// 不消费 token（消费在 ExchangeReauthCode 成功后进行）。
func (s *AccountReauthService) ParseReauthToken(ctx context.Context, token string) (accountReauthClaims, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return accountReauthClaims{}, infraerrors.New(http.StatusBadRequest, "REAUTH_INVALID_TOKEN", "invalid reauth token")
	}
	secret, err := s.reauthSecret(ctx)
	if err != nil {
		return accountReauthClaims{}, err
	}
	expected := signNotificationEmailToken(secret, parts[0])
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return accountReauthClaims{}, infraerrors.New(http.StatusBadRequest, "REAUTH_INVALID_SIGNATURE", "invalid reauth token signature")
	}
	rawPayload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return accountReauthClaims{}, infraerrors.New(http.StatusBadRequest, "REAUTH_INVALID_PAYLOAD", "invalid reauth token payload")
	}
	var claims accountReauthClaims
	if err := json.Unmarshal(rawPayload, &claims); err != nil {
		return accountReauthClaims{}, infraerrors.New(http.StatusBadRequest, "REAUTH_INVALID_PAYLOAD", "invalid reauth token payload")
	}
	if claims.AccountID <= 0 {
		return accountReauthClaims{}, infraerrors.New(http.StatusBadRequest, "REAUTH_INVALID_CLAIMS", "invalid reauth token claims")
	}
	if claims.Exp <= time.Now().Unix() {
		return accountReauthClaims{}, infraerrors.New(http.StatusBadRequest, "REAUTH_TOKEN_EXPIRED", "reauth token expired")
	}
	consumed, err := s.isConsumed(ctx, claims)
	if err != nil {
		return accountReauthClaims{}, err
	}
	if consumed {
		return accountReauthClaims{}, infraerrors.New(http.StatusBadRequest, "REAUTH_TOKEN_CONSUMED", "reauth token already used")
	}
	return claims, nil
}

// GetReauthInfo 验 token 并返回脱敏账号信息，用于落地页展示。
func (s *AccountReauthService) GetReauthInfo(ctx context.Context, token string) (*AccountReauthInfo, error) {
	claims, err := s.ParseReauthToken(ctx, token)
	if err != nil {
		return nil, err
	}
	account, err := s.loadReauthAccount(ctx, claims)
	if err != nil {
		return nil, err
	}
	return &AccountReauthInfo{
		AccountID:   account.ID,
		AccountName: account.Name,
		Platform:    account.Platform,
		OwnerName:   account.GetOwnerName(),
		OwnerEmail:  maskReauthEmail(account.GetOwnerEmail()),
		ExpiresAt:   claims.Exp,
	}, nil
}

// GenerateReauthURL 验 token → 取 account → 用 account.ProxyID 发起 OpenAI OAuth → 返回 auth_url + session。
// redirect_uri 锁死 localhost:1455（code 手动回填，与管理端一致）。
func (s *AccountReauthService) GenerateReauthURL(ctx context.Context, token string) (*AccountReauthURLResult, error) {
	claims, err := s.ParseReauthToken(ctx, token)
	if err != nil {
		return nil, err
	}
	account, err := s.loadReauthAccount(ctx, claims)
	if err != nil {
		return nil, err
	}
	if s.openaiOAuthService == nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "REAUTH_OAUTH_UNAVAILABLE", "openai oauth service unavailable")
	}
	// 继承账号原 ProxyID，保证授权 IP 与运行 IP 一致，避免触发上游风控。
	result, err := s.openaiOAuthService.GenerateAuthURL(ctx, account.ProxyID, openai.DefaultRedirectURI, openai.OAuthPlatformOpenAI)
	if err != nil {
		return nil, err
	}
	return &AccountReauthURLResult{
		AuthURL:   result.AuthURL,
		SessionID: result.SessionID,
	}, nil
}

// ExchangeReauthCode 验 token → ExchangeCode → BuildAccountCredentials → 仅更新 credentials/extra →
// ClearError → InvalidateToken。成功后一次性消费 token。
//
// 强制约束：
//   - token 锁定的 account_id 不可被改（只更新该账号）；
//   - 仅 platform=openai 的 OAuth 账号可重授权；
//   - 不新建账号、不改 group/proxy/owner。
func (s *AccountReauthService) ExchangeReauthCode(ctx context.Context, token, sessionID, code, state string) (*AccountReauthInfo, error) {
	claims, err := s.ParseReauthToken(ctx, token)
	if err != nil {
		return nil, err
	}
	account, err := s.loadReauthAccount(ctx, claims)
	if err != nil {
		return nil, err
	}
	if s.openaiOAuthService == nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "REAUTH_OAUTH_UNAVAILABLE", "openai oauth service unavailable")
	}
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(code) == "" || strings.TrimSpace(state) == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "REAUTH_MISSING_PARAMS", "session, code and state are required")
	}

	tokenInfo, err := s.openaiOAuthService.ExchangeCode(ctx, &OpenAIExchangeCodeInput{
		SessionID:   sessionID,
		Code:        code,
		State:       state,
		RedirectURI: openai.DefaultRedirectURI,
		ProxyID:     account.ProxyID, // 继承原 proxy
	})
	if err != nil {
		return nil, err
	}

	newCredentials := s.openaiOAuthService.BuildAccountCredentials(tokenInfo)
	if len(newCredentials) == 0 {
		return nil, infraerrors.New(http.StatusInternalServerError, "REAUTH_EMPTY_CREDENTIALS", "exchange returned empty credentials")
	}

	// 仅更新 credentials：在原有凭证上合并（保留敏感字段缺省语义），不动 group/proxy/owner。
	mergedCredentials := MergePreservingSensitiveCreds(account.Credentials, mergeReauthCredentials(account.Credentials, newCredentials))
	if err := persistAccountCredentials(ctx, s.accountRepo, account, mergedCredentials); err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "REAUTH_PERSIST_FAILED", "failed to persist credentials: %v", err)
	}

	// extra：记录最近一次重授权时间（不覆盖 owner_* 等既有字段）。
	extraUpdates := map[string]any{
		"reauthorized_at": time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.accountRepo.UpdateExtra(ctx, account.ID, extraUpdates); err != nil {
		// extra 更新失败不阻塞主流程（凭证已写入），仅记录。
		_ = err
	}

	// 清理 error 状态，恢复调度。
	if err := s.accountRepo.ClearError(ctx, account.ID); err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "REAUTH_CLEAR_ERROR_FAILED", "failed to clear error status: %v", err)
	}
	if s.runtimeBlocker != nil {
		s.runtimeBlocker.ClearAccountSchedulingBlock(account.ID)
	}

	// 失效 token 缓存，强制下次请求用新凭证。
	if s.tokenCacheInvalidator != nil {
		if err := s.tokenCacheInvalidator.InvalidateToken(ctx, account); err != nil {
			_ = err
		}
	}

	// 一次性消费：标记 token 失效，防重放。
	if err := s.markConsumed(ctx, claims); err != nil {
		// 凭证已更新成功；消费标记失败仅留下重放窗口风险，记录但不回滚。
		_ = err
	}

	return &AccountReauthInfo{
		AccountID:   account.ID,
		AccountName: account.Name,
		Platform:    account.Platform,
		OwnerName:   account.GetOwnerName(),
		OwnerEmail:  maskReauthEmail(account.GetOwnerEmail()),
		ExpiresAt:   claims.Exp,
	}, nil
}

// loadReauthAccount 取账号并强制校验：存在、platform=openai、type=oauth。
func (s *AccountReauthService) loadReauthAccount(ctx context.Context, claims accountReauthClaims) (*Account, error) {
	account, err := s.accountRepo.GetByID(ctx, claims.AccountID)
	if err != nil {
		return nil, infraerrors.New(http.StatusNotFound, "REAUTH_ACCOUNT_NOT_FOUND", "account not found")
	}
	if account == nil {
		return nil, infraerrors.New(http.StatusNotFound, "REAUTH_ACCOUNT_NOT_FOUND", "account not found")
	}
	if account.Platform != PlatformOpenAI {
		return nil, infraerrors.New(http.StatusBadRequest, "REAUTH_PLATFORM_UNSUPPORTED", "only openai accounts support self-service re-authorization")
	}
	if account.Type != AccountTypeOAuth {
		return nil, infraerrors.New(http.StatusBadRequest, "REAUTH_TYPE_UNSUPPORTED", "only oauth accounts support self-service re-authorization")
	}
	return account, nil
}

// reauthSecret 读取（或首次生成）重授权签名 secret。仿 unsubscribeSecret。
func (s *AccountReauthService) reauthSecret(ctx context.Context) (string, error) {
	secret, err := s.settingRepo.GetValue(ctx, SettingKeyAccountReauthSecret)
	if err == nil && strings.TrimSpace(secret) != "" {
		return strings.TrimSpace(secret), nil
	}
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return "", err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	secret = base64.RawURLEncoding.EncodeToString(buf)
	if err := s.settingRepo.Set(ctx, SettingKeyAccountReauthSecret, secret); err != nil {
		return "", err
	}
	return secret, nil
}

func (s *AccountReauthService) consumedKey(claims accountReauthClaims) string {
	// 用 nonce 哈希作为 key，token 维度精确去重。
	identity := fmt.Sprintf("%d\x00%s", claims.AccountID, claims.Nonce)
	sum := sha256.Sum256([]byte(identity))
	return accountReauthConsumedKeyPrefix + base64.RawURLEncoding.EncodeToString(sum[:])
}

func (s *AccountReauthService) isConsumed(ctx context.Context, claims accountReauthClaims) (bool, error) {
	if s.settingRepo == nil {
		return false, nil
	}
	_, err := s.settingRepo.GetValue(ctx, s.consumedKey(claims))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrSettingNotFound) {
		return false, nil
	}
	return false, err
}

func (s *AccountReauthService) markConsumed(ctx context.Context, claims accountReauthClaims) error {
	if s.settingRepo == nil {
		return nil
	}
	return s.settingRepo.Set(ctx, s.consumedKey(claims), time.Now().UTC().Format(time.RFC3339Nano))
}

// mergeReauthCredentials 把新凭证合并到旧凭证之上。
// OpenAI BuildAccountCredentials 仅在有值时填字段，这里直接覆盖旧值即可。
func mergeReauthCredentials(existing, incoming map[string]any) map[string]any {
	out := make(map[string]any, len(existing)+len(incoming))
	for k, v := range existing {
		out[k] = v
	}
	for k, v := range incoming {
		out[k] = v
	}
	return out
}

func randomNonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// maskReauthEmail 对邮箱做脱敏：保留首字符与域名，中间用 *** 替代。
func maskReauthEmail(email string) string {
	email = strings.TrimSpace(email)
	if email == "" {
		return ""
	}
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return "***"
	}
	local := email[:at]
	domain := email[at:]
	if len(local) <= 1 {
		return local + "***" + domain
	}
	return local[:1] + "***" + domain
}
