//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIImagesBridgeModelSelectionAndToolModel(t *testing.T) {
	t.Setenv("SUB2API_IMAGES_MAIN_MODEL", "")
	for _, tc := range []struct{ configured, expected string }{
		{"", "gpt-5.6-sol"},
		{" gpt-6 ", "gpt-6-astra"},
		{"gpt-5.5", "gpt-5.5"},
	} {
		t.Run(tc.expected, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.OpenAIImagesResponsesModel = tc.configured
			model := openAIImagesResponsesModel(cfg)
			require.Equal(t, tc.expected, model)
			for _, endpoint := range []string{openAIImagesGenerationsEndpoint, openAIImagesEditsEndpoint} {
				parsed := &OpenAIImagesRequest{Endpoint: endpoint, Model: "gpt-image-2", Prompt: "保持原始提示词", InputImageURLs: []string{"https://example.com/input.png"}}
				body, err := buildOpenAIImagesResponsesRequest(parsed, "gpt-image-2", model)
				require.NoError(t, err)
				require.Equal(t, model, gjson.GetBytes(body, "model").String())
				require.Equal(t, "gpt-image-2", gjson.GetBytes(body, "tools.0.model").String())
				require.Equal(t, "image_generation", gjson.GetBytes(body, "tool_choice.type").String())
				require.Equal(t, parsed.Prompt, gjson.GetBytes(body, "input.0.content.0.text").String())
			}
		})
	}
}

func TestOpenAIImagesBridgeModelUpstreamEnvironmentOverride(t *testing.T) {
	t.Setenv("SUB2API_IMAGES_MAIN_MODEL", " gpt-6 ")
	cfg := &config.Config{}
	cfg.Gateway.OpenAIImagesResponsesModel = "gpt-5.6-sol"
	require.Equal(t, "gpt-6-astra", openAIImagesResponsesModel(cfg))
	require.Equal(t, "gpt-6-astra", openAIImagesResponsesMainModelValue())
}

func TestOpenAIImagesBridgeModelCooldownOnlyAffectsOAuthImageRouting(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		"model_rate_limits": map[string]any{"gpt-5.6-sol": map[string]any{"rate_limit_reset_at": time.Now().Add(time.Minute).UTC().Format(time.RFC3339)}},
	}}
	ctx := WithOpenAIImagesBridgeModel(context.Background(), "gpt-5.6-sol")
	require.True(t, account.isModelRateLimitedWithContext(ctx, "gpt-image-2"))
	require.False(t, account.isModelRateLimitedWithContext(WithOpenAIImagesBridgeModel(context.Background(), "gpt-6-astra"), "gpt-image-2"))
	require.False(t, account.isModelRateLimitedWithContext(context.Background(), "gpt-image-2"))
	account.Type = AccountTypeAPIKey
	require.False(t, account.isModelRateLimitedWithContext(ctx, "gpt-image-2"))
}

func TestOpenAIImagesBridgeModelRejectionCooldownAttribution(t *testing.T) {
	for _, tc := range []struct{ rejected, scope, reason string }{
		{"gpt-5.6-sol", "gpt-5.6-sol", upstreamImageBridgeModelReason},
		{"gpt-image-2", "gpt-image-2", upstreamCodexPlanGatedModelReason},
	} {
		t.Run(tc.rejected, func(t *testing.T) {
			repo := &modelNotFoundAccountRepoStub{}
			svc := &RateLimitService{accountRepo: repo}
			ctx := WithOpenAIImagesBridgeModel(context.Background(), "gpt-5.6-sol")
			body := []byte(`{"detail":"The '` + tc.rejected + `' model is not supported when using Codex with a ChatGPT account."}`)
			require.True(t, svc.HandleUpstreamModelNotFound(ctx, openAICodexPlanGatedOAuthAccount(), "gpt-image-2", http.StatusBadRequest, body))
			require.Len(t, repo.modelRateLimitCalls, 1)
			require.Equal(t, tc.scope, repo.modelRateLimitCalls[0].scope)
			require.Equal(t, tc.reason, repo.modelRateLimitCalls[0].reason)
			require.Zero(t, repo.tempCalls)
		})
	}
}

func TestOpenAIImagesBridgeModelRejectionDoesNotMatchUnrelatedMention(t *testing.T) {
	body := []byte(`{"detail":"The 'gpt-image-2' model is not supported when using Codex with a ChatGPT account. Bridge configuration is 'gpt-5.6-sol'."}`)
	require.False(t, upstreamImageBridgeModelRejected(body, "gpt-5.6-sol"))
}
