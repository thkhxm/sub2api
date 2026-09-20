//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cliLlmUserRepo struct{ service.UserRepository }

func (*cliLlmUserRepo) GetByID(_ context.Context, id int64) (*service.User, error) {
	return &service.User{ID: id}, nil
}

type cliLlmGroupRepo struct {
	service.GroupRepository
	groups []service.Group
}

func (r *cliLlmGroupRepo) ListActive(context.Context) ([]service.Group, error) {
	return r.groups, nil
}

type cliLlmSubscriptionRepo struct {
	service.UserSubscriptionRepository
}

func (*cliLlmSubscriptionRepo) ListActiveByUserID(context.Context, int64) ([]service.UserSubscription, error) {
	return nil, nil
}

func TestCliLlmPreservesCatalogAfterModelAllowlistMigration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groups := &cliLlmGroupRepo{groups: []service.Group{
		{ID: 5, ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-6", "gpt-6-astra", "gpt-image-2.5-flare"}}},
		{ID: 6, ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{" gpt-6 ", ""}}},
		{ID: 7, IsExclusive: true, ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"private-model"}}},
		{ID: 8, ModelAllowlist: service.GroupModelAllowlist{Models: []string{"disabled-model"}}},
	}}
	cfg := &config.Config{}
	cfg.Server.FrontendURL = "https://gateway.example/"
	keys := service.NewAPIKeyService(nil, &cliLlmUserRepo{}, groups, &cliLlmSubscriptionRepo{}, nil, nil, cfg)
	h := NewCliHandler(cfg, nil, nil, keys, nil, nil)
	router := gin.New()
	router.GET("/api/v1/cli/llm", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		h.GetLlm(c)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/cli/llm", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Code int                `json:"code"`
		Data dto.CliLlmResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Zero(t, response.Code)
	require.Equal(t, "https://gateway.example/v1", response.Data.BaseURL)
	require.Equal(t, "https://gateway.example/anthropic/v1", response.Data.AnthropicBaseURL)
	require.Equal(t, "https://gateway.example/gemini/v1beta", response.Data.GeminiBaseURL)
	models := make(map[string]int)
	for _, model := range response.Data.Models {
		require.Equal(t, model.ID, model.Name)
		models[model.ID] = model.ContextWindow
	}
	require.Len(t, response.Data.Models, 3)
	require.Equal(t, map[string]int{"gpt-6": 922000, "gpt-6-astra": 922000, "gpt-image-2.5-flare": 0}, models)
}
