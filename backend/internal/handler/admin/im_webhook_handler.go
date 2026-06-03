package admin

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// IMWebhookHandler 管理账号 revoke 告警的 IM webhook 通道配置（飞书/企微/Telegram）。
type IMWebhookHandler struct {
	notifier *service.IMWebhookNotifier
}

// NewIMWebhookHandler 创建 IM webhook 配置处理器。
func NewIMWebhookHandler(notifier *service.IMWebhookNotifier) *IMWebhookHandler {
	return &IMWebhookHandler{notifier: notifier}
}

// GetConfig 返回当前 IM webhook 配置（DB-backed）。
// GET /api/v1/admin/im-webhook/config
func (h *IMWebhookHandler) GetConfig(c *gin.Context) {
	if h.notifier == nil {
		response.Error(c, http.StatusServiceUnavailable, "IM webhook notifier not available")
		return
	}
	cfg, err := h.notifier.GetConfig(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "Failed to get IM webhook config")
		return
	}
	response.Success(c, cfg)
}

// UpdateConfig 更新 IM webhook 配置（DB-backed）。
// PUT /api/v1/admin/im-webhook/config
func (h *IMWebhookHandler) UpdateConfig(c *gin.Context) {
	if h.notifier == nil {
		response.Error(c, http.StatusServiceUnavailable, "IM webhook notifier not available")
		return
	}
	var req service.IMWebhookConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	updated, err := h.notifier.UpdateConfig(c.Request.Context(), &req)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	response.Success(c, updated)
}

// TestRequest 测试发送请求体。
type IMWebhookTestRequest struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

// SendTest 向已配置的通道发送一条测试消息。
// POST /api/v1/admin/im-webhook/test
func (h *IMWebhookHandler) SendTest(c *gin.Context) {
	if h.notifier == nil {
		response.Error(c, http.StatusServiceUnavailable, "IM webhook notifier not available")
		return
	}
	var req IMWebhookTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		req = IMWebhookTestRequest{}
	}
	title := req.Title
	if title == "" {
		title = "Sub2API IM Webhook 测试 / Test"
	}
	text := req.Text
	if text == "" {
		text = "这是一条测试消息。/ This is a test message."
	}
	h.notifier.Notify(c.Request.Context(), title, text)
	response.Success(c, gin.H{"sent": true})
}
