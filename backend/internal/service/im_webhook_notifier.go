package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// IM webhook 通用通知渠道。
//
// sub2api 此前没有任何 webhook 能力，本模块为「账号 revoke 自动告警」新增一个轻量的、
// 与邮件并行的即时消息渠道。支持三种主流自建机器人：
//   - feishu   飞书自定义机器人（custom bot），可选 secret 做签名校验
//   - wecom    企业微信群机器人（group robot）
//   - telegram Telegram Bot sendMessage
//
// 配置存 settings（SettingKeyIMWebhookConfig，JSON），由管理员在后台配置，仿 OpsEmailNotificationConfig
// 的 DB 存配置模式。发送的是纯文本消息（text/markdown 视渠道而定，这里统一用纯文本以便跨渠道一致）。

const (
	IMWebhookTypeFeishu   = "feishu"
	IMWebhookTypeWeCom    = "wecom"
	IMWebhookTypeTelegram = "telegram"
)

const imWebhookRequestTimeout = 10 * time.Second

// IMWebhookChannel 描述单个 IM webhook 通道。
type IMWebhookChannel struct {
	// Type: feishu / wecom / telegram
	Type string `json:"type"`
	// URL: 渠道的 webhook 地址。
	//   - feishu: https://open.feishu.cn/open-apis/bot/v2/hook/xxx
	//   - wecom:  https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxx
	//   - telegram: https://api.telegram.org/bot<token>/sendMessage（也可只填 https://api.telegram.org 由代码补全）
	URL string `json:"url"`
	// Secret: 飞书自定义机器人可选签名校验密钥（启用「签名校验」时填写）。其他渠道忽略。
	Secret string `json:"secret,omitempty"`
	// TelegramChatID: telegram 渠道必填，目标会话 ID（群或个人）。
	TelegramChatID string `json:"telegram_chat_id,omitempty"`
	// TelegramBotToken: telegram 渠道，若 URL 未内嵌 bot token，可单独提供。
	TelegramBotToken string `json:"telegram_bot_token,omitempty"`
	// Enabled: 是否启用该通道。
	Enabled bool `json:"enabled"`
}

// IMWebhookConfig 是 IM webhook 渠道集合配置。
type IMWebhookConfig struct {
	// Enabled: 总开关。关闭时不发送任何 IM 通知。
	Enabled bool `json:"enabled"`
	// Channels: 通道列表。一次告警会向所有 enabled 通道并发投递。
	Channels []IMWebhookChannel `json:"channels"`
}

// IMWebhookNotifier 负责按渠道格式 POST 告警文本。
type IMWebhookNotifier struct {
	settingRepo SettingRepository
	httpClient  *http.Client
}

// NewIMWebhookNotifier 创建 IM webhook 通知器。
func NewIMWebhookNotifier(settingRepo SettingRepository) *IMWebhookNotifier {
	return &IMWebhookNotifier{
		settingRepo: settingRepo,
		httpClient: &http.Client{
			Timeout: imWebhookRequestTimeout,
		},
	}
}

// GetConfig 读取 IM webhook 配置（首次读取时初始化默认空配置）。
func (n *IMWebhookNotifier) GetConfig(ctx context.Context) (*IMWebhookConfig, error) {
	defaultCfg := &IMWebhookConfig{Enabled: false, Channels: []IMWebhookChannel{}}
	if n == nil || n.settingRepo == nil {
		return defaultCfg, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := n.settingRepo.GetValue(ctx, SettingKeyIMWebhookConfig)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			if b, mErr := json.Marshal(defaultCfg); mErr == nil {
				_ = n.settingRepo.Set(ctx, SettingKeyIMWebhookConfig, string(b))
			}
			return defaultCfg, nil
		}
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return defaultCfg, nil
	}
	cfg := &IMWebhookConfig{}
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		// 配置损坏不应阻塞告警链路，回退空配置。
		return defaultCfg, nil
	}
	normalizeIMWebhookConfig(cfg)
	return cfg, nil
}

// UpdateConfig 校验并保存 IM webhook 配置。
func (n *IMWebhookNotifier) UpdateConfig(ctx context.Context, cfg *IMWebhookConfig) (*IMWebhookConfig, error) {
	if n == nil || n.settingRepo == nil {
		return nil, errors.New("setting repository not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg == nil {
		return nil, errors.New("invalid config")
	}
	normalizeIMWebhookConfig(cfg)
	if err := validateIMWebhookConfig(cfg); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if err := n.settingRepo.Set(ctx, SettingKeyIMWebhookConfig, string(raw)); err != nil {
		return nil, err
	}
	updated := &IMWebhookConfig{}
	_ = json.Unmarshal(raw, updated)
	return updated, nil
}

// Notify 向所有启用的通道发送同一条纯文本告警。
// best-effort：单个通道失败只记录日志，不影响其他通道，也不向上抛错。
func (n *IMWebhookNotifier) Notify(ctx context.Context, title, text string) {
	if n == nil {
		return
	}
	cfg, err := n.GetConfig(ctx)
	if err != nil {
		slog.Warn("im_webhook_get_config_failed", "error", err)
		return
	}
	if cfg == nil || !cfg.Enabled || len(cfg.Channels) == 0 {
		return
	}
	for _, channel := range cfg.Channels {
		if !channel.Enabled {
			continue
		}
		if err := n.send(ctx, channel, title, text); err != nil {
			slog.Warn("im_webhook_send_failed", "type", channel.Type, "error", err)
		}
	}
}

func (n *IMWebhookNotifier) send(ctx context.Context, channel IMWebhookChannel, title, text string) error {
	switch strings.ToLower(strings.TrimSpace(channel.Type)) {
	case IMWebhookTypeFeishu:
		return n.sendFeishu(ctx, channel, title, text)
	case IMWebhookTypeWeCom:
		return n.sendWeCom(ctx, channel, title, text)
	case IMWebhookTypeTelegram:
		return n.sendTelegram(ctx, channel, title, text)
	default:
		return fmt.Errorf("unsupported im webhook type: %s", channel.Type)
	}
}

// sendFeishu 飞书自定义机器人：{"msg_type":"text","content":{"text":"..."}}
// 启用签名时附带 timestamp + sign（HmacSHA256，key = timestamp+"\n"+secret，对空串签名）。
func (n *IMWebhookNotifier) sendFeishu(ctx context.Context, channel IMWebhookChannel, title, text string) error {
	body := map[string]any{
		"msg_type": "text",
		"content": map[string]any{
			"text": composeIMText(title, text),
		},
	}
	if secret := strings.TrimSpace(channel.Secret); secret != "" {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		sign, err := feishuSign(timestamp, secret)
		if err != nil {
			return err
		}
		body["timestamp"] = timestamp
		body["sign"] = sign
	}
	return n.postJSON(ctx, channel.URL, body)
}

// sendWeCom 企业微信群机器人：{"msgtype":"text","text":{"content":"..."}}
func (n *IMWebhookNotifier) sendWeCom(ctx context.Context, channel IMWebhookChannel, title, text string) error {
	body := map[string]any{
		"msgtype": "text",
		"text": map[string]any{
			"content": composeIMText(title, text),
		},
	}
	return n.postJSON(ctx, channel.URL, body)
}

// sendTelegram Telegram Bot sendMessage：{"chat_id":"...","text":"..."}
func (n *IMWebhookNotifier) sendTelegram(ctx context.Context, channel IMWebhookChannel, title, text string) error {
	chatID := strings.TrimSpace(channel.TelegramChatID)
	if chatID == "" {
		return errors.New("telegram channel requires telegram_chat_id")
	}
	endpoint, err := telegramSendMessageURL(channel)
	if err != nil {
		return err
	}
	body := map[string]any{
		"chat_id": chatID,
		"text":    composeIMText(title, text),
	}
	return n.postJSON(ctx, endpoint, body)
}

func (n *IMWebhookNotifier) postJSON(ctx context.Context, endpoint string, body any) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return errors.New("webhook url is empty")
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(ctx, imWebhookRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	// 读取并丢弃 body，避免连接无法复用；同时用于错误日志。
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// composeIMText 把标题与正文拼成纯文本（标题非空时置顶）。
func composeIMText(title, text string) string {
	title = strings.TrimSpace(title)
	text = strings.TrimSpace(text)
	if title == "" {
		return text
	}
	if text == "" {
		return title
	}
	return title + "\n" + text
}

// feishuSign 计算飞书自定义机器人签名。
// 规则：以 (timestamp + "\n" + secret) 为 HMAC-SHA256 的 key，对空字节串签名，结果 base64。
func feishuSign(timestamp, secret string) (string, error) {
	stringToSign := timestamp + "\n" + secret
	mac := hmac.New(sha256.New, []byte(stringToSign))
	if _, err := mac.Write([]byte{}); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

// telegramSendMessageURL 解析 telegram 的 sendMessage 端点。
// 支持两种配置：
//   - URL 已是完整 sendMessage 端点（含 bot token），直接使用。
//   - URL 是 api 基址（如 https://api.telegram.org），配合 telegram_bot_token 拼出端点。
func telegramSendMessageURL(channel IMWebhookChannel) (string, error) {
	url := strings.TrimSpace(channel.URL)
	token := strings.TrimSpace(channel.TelegramBotToken)
	if url == "" {
		url = "https://api.telegram.org"
	}
	if strings.Contains(url, "/bot") && strings.HasSuffix(url, "sendMessage") {
		return url, nil
	}
	if strings.Contains(url, "/bot") {
		// 形如 https://api.telegram.org/bot<token>
		return strings.TrimRight(url, "/") + "/sendMessage", nil
	}
	if token == "" {
		return "", errors.New("telegram channel requires bot token (in url as /bot<token> or telegram_bot_token)")
	}
	return strings.TrimRight(url, "/") + "/bot" + token + "/sendMessage", nil
}

func normalizeIMWebhookConfig(cfg *IMWebhookConfig) {
	if cfg == nil {
		return
	}
	if cfg.Channels == nil {
		cfg.Channels = []IMWebhookChannel{}
	}
	for i := range cfg.Channels {
		cfg.Channels[i].Type = strings.ToLower(strings.TrimSpace(cfg.Channels[i].Type))
		cfg.Channels[i].URL = strings.TrimSpace(cfg.Channels[i].URL)
		cfg.Channels[i].Secret = strings.TrimSpace(cfg.Channels[i].Secret)
		cfg.Channels[i].TelegramChatID = strings.TrimSpace(cfg.Channels[i].TelegramChatID)
		cfg.Channels[i].TelegramBotToken = strings.TrimSpace(cfg.Channels[i].TelegramBotToken)
	}
}

func validateIMWebhookConfig(cfg *IMWebhookConfig) error {
	if cfg == nil {
		return errors.New("invalid config")
	}
	for idx, channel := range cfg.Channels {
		switch channel.Type {
		case IMWebhookTypeFeishu, IMWebhookTypeWeCom, IMWebhookTypeTelegram:
		default:
			return fmt.Errorf("channel[%d]: unsupported type %q (must be feishu/wecom/telegram)", idx, channel.Type)
		}
		if channel.Type == IMWebhookTypeTelegram {
			if channel.TelegramChatID == "" {
				return fmt.Errorf("channel[%d]: telegram_chat_id is required for telegram", idx)
			}
			// telegram 允许仅提供 bot token + 默认 api 基址，因此 URL 可为空。
			if channel.URL == "" && channel.TelegramBotToken == "" {
				return fmt.Errorf("channel[%d]: telegram requires url or telegram_bot_token", idx)
			}
		} else if channel.URL == "" {
			return fmt.Errorf("channel[%d]: url is required", idx)
		}
	}
	return nil
}
