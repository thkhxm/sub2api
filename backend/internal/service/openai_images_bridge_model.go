package service

import (
	"context"
	"os"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

type openAIImagesBridgeModelContextKey struct{}

// OpenAIImagesResponsesModel 返回 OAuth 图片桥接实际使用的主模型。
func (s *OpenAIGatewayService) OpenAIImagesResponsesModel() string {
	return openAIImagesResponsesModel(s.cfg)
}

func openAIImagesResponsesModel(cfg *config.Config) string {
	// 兼容上游的紧急覆盖变量，未设置时保留现有网关配置。
	model := strings.TrimSpace(os.Getenv("SUB2API_IMAGES_MAIN_MODEL"))
	if model == "" && cfg != nil {
		model = strings.TrimSpace(cfg.Gateway.OpenAIImagesResponsesModel)
	}
	if model == "" {
		model = openAIImagesResponsesMainModel
	}
	if canonical := normalizeKnownOpenAICodexModel(model); canonical != "" {
		return canonical
	}
	return model
}

// WithOpenAIImagesBridgeModel 让调度和错误处理使用与请求体一致的桥接主模型。
func WithOpenAIImagesBridgeModel(ctx context.Context, model string) context.Context {
	return context.WithValue(WithOpenAIImagesEndpoint(ctx), openAIImagesBridgeModelContextKey{}, strings.TrimSpace(model))
}

func openAIImagesBridgeModelFromContext(ctx context.Context) string {
	if ctx == nil || !OpenAIImagesEndpointFromContext(ctx) {
		return ""
	}
	model, _ := ctx.Value(openAIImagesBridgeModelContextKey{}).(string)
	return model
}

func upstreamImageBridgeModelRejected(body []byte, model string) bool {
	message := strings.ToLower(extractUpstreamErrorMessage(body))
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return false
	}
	return strings.Contains(message, "'"+model+"' model is not supported") ||
		strings.Contains(message, `"`+model+`" model is not supported`)
}
