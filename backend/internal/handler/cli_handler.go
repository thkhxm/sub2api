package handler

import (
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// CliHandler 处理 PunkcodeAI 桌面端 CLI 模式的 HTTP 请求。
//
// 它复用 sub2api 现有的 service 层（AuthService / UserService / APIKeyService / UsageService），
// 仅在 handler 层提供面向桌面客户端的简化接口，与面向 Web 前端的 AuthHandler / UserHandler 等解耦。
type CliHandler struct {
	cfg        *config.Config
	authSvc    *service.AuthService
	userSvc    *service.UserService
	apiKeySvc  *service.APIKeyService
	usageSvc   *service.UsageService
	settingSvc *service.SettingService
}

// NewCliHandler 构造 CliHandler
func NewCliHandler(
	cfg *config.Config,
	authSvc *service.AuthService,
	userSvc *service.UserService,
	apiKeySvc *service.APIKeyService,
	usageSvc *service.UsageService,
	settingSvc *service.SettingService,
) *CliHandler {
	return &CliHandler{
		cfg:        cfg,
		authSvc:    authSvc,
		userSvc:    userSvc,
		apiKeySvc:  apiKeySvc,
		usageSvc:   usageSvc,
		settingSvc: settingSvc,
	}
}

// cliApiKeyName 是 CLI 客户端自动创建的 API Key 名字。
// 桌面端按此固定名称查找/创建，避免在 UI 暴露 key 管理。
const cliApiKeyName = "opencode-cli"

// Register POST /api/v1/cli/register
//
// 桌面端注册：邮箱 + 密码 + 昵称。
// 部署侧约束（M2 setting seed）：
//   - settings.registration_enabled = true
//   - settings.invitation_code_enabled = false
//   - settings.email_verify_enabled = false
func (h *CliHandler) Register(c *gin.Context) {
	var req dto.CliRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	ctx := c.Request.Context()

	// 1. 调基础 Register（不带 verify code / invitation / promo / aff）
	_, user, err := h.authSvc.RegisterWithVerification(ctx, req.Email, req.Password, "", "", "", "")
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// 2. 立即把 nickname 写入 Username 字段。
	// 失败不阻断注册（用户已创建），但必须打日志，避免静默吞错。
	if nick := strings.TrimSpace(req.Nickname); nick != "" {
		updated, perr := h.userSvc.UpdateProfile(ctx, user.ID, service.UpdateProfileRequest{
			Username: &nick,
		})
		if perr != nil {
			slog.Warn("cli register: failed to set nickname after creating user",
				"user_id", user.ID, "email", user.Email, "err", perr)
		} else if updated != nil {
			user = updated
		}
	}

	h.respondWithTokenPair(c, user)
}

// Login POST /api/v1/cli/login
func (h *CliHandler) Login(c *gin.Context) {
	var req dto.CliLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	ctx := c.Request.Context()
	_, user, err := h.authSvc.Login(ctx, req.Email, req.Password)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if user == nil {
		response.Unauthorized(c, "user not found")
		return
	}
	if !user.IsActive() {
		response.ErrorFrom(c, service.ErrUserNotActive)
		return
	}

	h.respondWithTokenPair(c, user)
}

// respondWithTokenPair 生成 access + refresh 并写入完整 CliAuthResponse
func (h *CliHandler) respondWithTokenPair(c *gin.Context, user *service.User) {
	pair, err := h.authSvc.GenerateTokenPair(c.Request.Context(), user, "")
	if err != nil {
		response.InternalError(c, "Failed to generate token pair")
		return
	}
	response.Success(c, dto.CliAuthResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
		TokenType:    "Bearer",
		User: dto.CliUser{
			ID:         user.ID,
			Email:      user.Email,
			Nickname:   user.Username,
			BalanceUSD: user.Balance,
		},
	})
}

// Refresh POST /api/v1/cli/refresh
//
// 只返新 token pair，不带 user 详情；客户端拿到后自行调 /cli/me 取最新用户信息。
func (h *CliHandler) Refresh(c *gin.Context) {
	var req dto.CliRefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	ctx := c.Request.Context()
	result, err := h.authSvc.RefreshTokenPair(ctx, req.RefreshToken)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.CliTokenPair{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresIn:    result.ExpiresIn,
		TokenType:    "Bearer",
	})
}

// Logout POST /api/v1/cli/logout
//
// 撤销指定 refresh_token（如果提供）；access_token 由客户端本地清除即可。
func (h *CliHandler) Logout(c *gin.Context) {
	var req dto.CliLogoutRequest
	_ = c.ShouldBindJSON(&req)

	if req.RefreshToken != "" {
		_ = h.authSvc.RevokeRefreshToken(c.Request.Context(), req.RefreshToken)
	}
	response.Success(c, gin.H{"message": "ok"})
}

// GetMe GET /api/v1/cli/me  (需要 JWT)
func (h *CliHandler) GetMe(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "user not authenticated")
		return
	}

	ctx := c.Request.Context()
	user, err := h.userSvc.GetProfile(ctx, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	usedTodayUSD := 0.0
	usedMonthUSD := 0.0
	if stats, e := h.usageSvc.GetStatsByUser(ctx, subject.UserID, startOfDay, now); e == nil && stats != nil {
		usedTodayUSD = stats.TotalCost
	}
	if stats, e := h.usageSvc.GetStatsByUser(ctx, subject.UserID, startOfMonth, now); e == nil && stats != nil {
		usedMonthUSD = stats.TotalCost
	}

	response.Success(c, dto.CliMeResponse{
		ID:           user.ID,
		Email:        user.Email,
		Nickname:     user.Username,
		BalanceUSD:   user.Balance,
		UsedTodayUSD: usedTodayUSD,
		UsedMonthUSD: usedMonthUSD,
	})
}

// GetApiKey GET /api/v1/cli/api-key  (需要 JWT)
//
// 返回当前用户的 CLI 专属 API Key（不存在则自动创建）。
// 返回的是明文 key，桌面端只在内存使用、不落盘。
func (h *CliHandler) GetApiKey(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "user not authenticated")
		return
	}

	ctx := c.Request.Context()

	// 1. 查找用户已有的同名 active key
	keys, _, err := h.apiKeySvc.List(ctx, subject.UserID, pagination.PaginationParams{
		Page:     1,
		PageSize: 200,
	}, service.APIKeyListFilters{Search: cliApiKeyName})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	for i := range keys {
		if keys[i].Name == cliApiKeyName && keys[i].Status == service.StatusActive {
			response.Success(c, dto.CliApiKeyResponse{Key: keys[i].Key})
			return
		}
	}

	// 2. 不存在则创建（默认配额无限、永不过期）
	created, err := h.apiKeySvc.Create(ctx, subject.UserID, service.CreateAPIKeyRequest{
		Name: cliApiKeyName,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.CliApiKeyResponse{Key: created.Key})
}

// GetLlm GET /api/v1/cli/llm  (需要 JWT)
//
// 返回桌面端可见的 LLM 网关 base URL + 用户可见模型列表。
// 模型列表通过 APIKeyService.GetAvailableGroups 拿到用户能绑的所有 group，再聚合
// 它们的 ModelsListConfig.Models 去重后返出。
func (h *CliHandler) GetLlm(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "user not authenticated")
		return
	}

	ctx := c.Request.Context()

	base := strings.TrimRight(h.cfg.Server.FrontendURL, "/")
	if base == "" {
		// 兜底：dev 环境下 frontend_url 可能未配，用本机回环
		base = "http://localhost:8080"
	}

	// 聚合用户可见模型：所有 available group 的 models_list_config.models 去重
	groups, err := h.apiKeySvc.GetAvailableGroups(ctx, subject.UserID)
	if err != nil {
		slog.Warn("cli/llm: failed to load available groups; returning empty model list",
			"user_id", subject.UserID, "err", err)
	}

	modelSet := make(map[string]struct{})
	for _, g := range groups {
		if !g.ModelsListConfig.Enabled {
			continue
		}
		for _, m := range g.ModelsListConfig.Models {
			m = strings.TrimSpace(m)
			if m != "" {
				modelSet[m] = struct{}{}
			}
		}
	}

	models := make([]dto.CliModel, 0, len(modelSet))
	for id := range modelSet {
		models = append(models, dto.CliModel{
			ID:   id,
			Name: id, // 桌面端展示直接用模型 ID
		})
	}

	response.Success(c, dto.CliLlmResponse{
		BaseURL:          base + "/v1",
		AnthropicBaseURL: base + "/anthropic/v1",
		GeminiBaseURL:    base + "/gemini/v1beta",
		Models:           models,
	})
}

// GetUsage GET /api/v1/cli/usage?days=7  (需要 JWT)
//
// 返回最近 N 天的按天用量。
// M1：直接透传 usage service 的 raw rows，字段结构与 sub2api 内部对齐；
// M7 桌面端实现 widget 时再做字段映射。
func (h *CliHandler) GetUsage(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "user not authenticated")
		return
	}

	days := 7
	if d := c.Query("days"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n >= 1 && n <= 90 {
			days = n
		}
	}

	rows, err := h.usageSvc.GetDailyStats(c.Request.Context(), subject.UserID, days)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"daily": rows})
}
