package handler

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AccountReauthHandler 处理成员自助重授权的公开端点（无 admin 鉴权，仿 email-unsubscribe）。
// 鉴权完全依赖 URL 中的一次性 HMAC 签名 token，由 service 层校验。
type AccountReauthHandler struct {
	reauthService *service.AccountReauthService
}

// NewAccountReauthHandler 创建自助重授权处理器。
func NewAccountReauthHandler(reauthService *service.AccountReauthService) *AccountReauthHandler {
	return &AccountReauthHandler{reauthService: reauthService}
}

// GetInfo 验签返回脱敏账号信息。
// GET /api/v1/account-reauth/info?token=
func (h *AccountReauthHandler) GetInfo(c *gin.Context) {
	if h.reauthService == nil {
		response.InternalError(c, "account reauth service is not configured")
		return
	}
	token := strings.TrimSpace(c.Query("token"))
	if token == "" {
		response.BadRequest(c, "token is required")
		return
	}
	info, err := h.reauthService.GetReauthInfo(c.Request.Context(), token)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, info)
}

// GenerateURLRequest 发起 OAuth 的请求体。
type GenerateURLRequest struct {
	Token string `json:"token" binding:"required"`
}

// GenerateURL 验 token → 发起 OpenAI OAuth → 返回 auth_url + session。
// POST /api/v1/account-reauth/generate-url
func (h *AccountReauthHandler) GenerateURL(c *gin.Context) {
	if h.reauthService == nil {
		response.InternalError(c, "account reauth service is not configured")
		return
	}
	var req GenerateURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	result, err := h.reauthService.GenerateReauthURL(c.Request.Context(), strings.TrimSpace(req.Token))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// ExchangeCodeRequest 回填授权码的请求体。
type ExchangeCodeRequest struct {
	Token   string `json:"token" binding:"required"`
	Session string `json:"session" binding:"required"`
	Code    string `json:"code" binding:"required"`
	State   string `json:"state" binding:"required"`
}

// ExchangeCode 验 token → ExchangeCode → 更新账号凭证 + ClearError。
// POST /api/v1/account-reauth/exchange-code
func (h *AccountReauthHandler) ExchangeCode(c *gin.Context) {
	if h.reauthService == nil {
		response.InternalError(c, "account reauth service is not configured")
		return
	}
	var req ExchangeCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	info, err := h.reauthService.ExchangeReauthCode(
		c.Request.Context(),
		strings.TrimSpace(req.Token),
		strings.TrimSpace(req.Session),
		strings.TrimSpace(req.Code),
		strings.TrimSpace(req.State),
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, info)
}
