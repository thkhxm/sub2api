package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIMWebhookConfigGetUpdate(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	n := NewIMWebhookNotifier(repo)

	// 首次读取返回默认空配置。
	cfg, err := n.GetConfig(ctx)
	require.NoError(t, err)
	require.False(t, cfg.Enabled)
	require.Empty(t, cfg.Channels)

	// 保存有效配置。
	updated, err := n.UpdateConfig(ctx, &IMWebhookConfig{
		Enabled: true,
		Channels: []IMWebhookChannel{
			{Type: "feishu", URL: "https://open.feishu.cn/open-apis/bot/v2/hook/abc", Enabled: true},
			{Type: "wecom", URL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=abc", Enabled: true},
			{Type: "telegram", TelegramChatID: "123", TelegramBotToken: "tok", Enabled: true},
		},
	})
	require.NoError(t, err)
	require.True(t, updated.Enabled)
	require.Len(t, updated.Channels, 3)

	// 重新读取持久化结果。
	reloaded, err := n.GetConfig(ctx)
	require.NoError(t, err)
	require.Len(t, reloaded.Channels, 3)
}

func TestIMWebhookConfigValidation(t *testing.T) {
	ctx := context.Background()
	n := NewIMWebhookNotifier(newNotificationEmailMemorySettingRepo())

	// 不支持的类型。
	_, err := n.UpdateConfig(ctx, &IMWebhookConfig{
		Channels: []IMWebhookChannel{{Type: "slack", URL: "https://x"}},
	})
	require.Error(t, err)

	// feishu 缺 URL。
	_, err = n.UpdateConfig(ctx, &IMWebhookConfig{
		Channels: []IMWebhookChannel{{Type: "feishu"}},
	})
	require.Error(t, err)

	// telegram 缺 chat_id。
	_, err = n.UpdateConfig(ctx, &IMWebhookConfig{
		Channels: []IMWebhookChannel{{Type: "telegram", TelegramBotToken: "tok"}},
	})
	require.Error(t, err)
}

func TestTelegramSendMessageURL(t *testing.T) {
	// 完整 sendMessage 端点。
	u, err := telegramSendMessageURL(IMWebhookChannel{
		URL:            "https://api.telegram.org/bot123:ABC/sendMessage",
		TelegramChatID: "1",
	})
	require.NoError(t, err)
	require.Equal(t, "https://api.telegram.org/bot123:ABC/sendMessage", u)

	// 只到 /bot<token>。
	u, err = telegramSendMessageURL(IMWebhookChannel{
		URL:            "https://api.telegram.org/bot123:ABC",
		TelegramChatID: "1",
	})
	require.NoError(t, err)
	require.Equal(t, "https://api.telegram.org/bot123:ABC/sendMessage", u)

	// 基址 + 单独 token。
	u, err = telegramSendMessageURL(IMWebhookChannel{
		TelegramBotToken: "123:ABC",
		TelegramChatID:   "1",
	})
	require.NoError(t, err)
	require.Equal(t, "https://api.telegram.org/bot123:ABC/sendMessage", u)

	// 既无 token 又无 /bot 的 URL。
	_, err = telegramSendMessageURL(IMWebhookChannel{
		URL:            "https://api.telegram.org",
		TelegramChatID: "1",
	})
	require.Error(t, err)
}

func TestFeishuSignDeterministic(t *testing.T) {
	s1, err := feishuSign("1700000000", "secret")
	require.NoError(t, err)
	s2, err := feishuSign("1700000000", "secret")
	require.NoError(t, err)
	require.Equal(t, s1, s2)
	require.NotEmpty(t, s1)
}

func TestComposeIMText(t *testing.T) {
	require.Equal(t, "title\nbody", composeIMText("title", "body"))
	require.Equal(t, "body", composeIMText("", "body"))
	require.Equal(t, "title", composeIMText("title", ""))
}
