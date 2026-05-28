package handler

import (
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// BalanceRequestHandler 提供两类端点：
//   - 用户侧（/api/v1/cli/balance-requests）：提交申请、查看自己的列表
//   - 管理员侧（/api/v1/admin/balance-requests）：列出全部、一键审批
type BalanceRequestHandler struct {
	svc *service.BalanceRequestService
}

// NewBalanceRequestHandler 构造 handler
func NewBalanceRequestHandler(svc *service.BalanceRequestService) *BalanceRequestHandler {
	return &BalanceRequestHandler{svc: svc}
}

// ================ DTO ================

type createBalanceRequestRequest struct {
	AmountUSD float64 `json:"amount_usd" binding:"required,gt=0"`
	Note      string  `json:"note"`
}

type approveBalanceRequestRequest struct {
	// nil 时按原申请金额；非 nil 时管理员可改金额
	ApprovedAmountUSD *float64 `json:"approved_amount_usd,omitempty"`
}

type rejectBalanceRequestRequest struct {
	Reason string `json:"reason"`
}

type balanceRequestResponse struct {
	ID                int64      `json:"id"`
	UserID            int64      `json:"user_id"`
	AmountUSD         float64    `json:"amount_usd"`
	ApprovedAmountUSD *float64   `json:"approved_amount_usd,omitempty"`
	Note              string     `json:"note"`
	Status            string     `json:"status"`
	ReviewerID        *int64     `json:"reviewer_id,omitempty"`
	ReviewedAt        *time.Time `json:"reviewed_at,omitempty"`
	RejectReason      string     `json:"reject_reason,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func toBalanceRequestResponse(r *service.BalanceRequest) balanceRequestResponse {
	if r == nil {
		return balanceRequestResponse{}
	}
	return balanceRequestResponse{
		ID:                r.ID,
		UserID:            r.UserID,
		AmountUSD:         r.AmountUSD,
		ApprovedAmountUSD: r.ApprovedAmountUSD,
		Note:              r.Note,
		Status:            r.Status,
		ReviewerID:        r.ReviewerID,
		ReviewedAt:        r.ReviewedAt,
		RejectReason:      r.RejectReason,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
	}
}

// ================ 用户侧 ================

// CreateMine POST /api/v1/cli/balance-requests (Bearer)
func (h *BalanceRequestHandler) CreateMine(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "user not authenticated")
		return
	}

	var req createBalanceRequestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	created, err := h.svc.CreateRequest(c.Request.Context(), subject.UserID, req.AmountUSD, req.Note)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, toBalanceRequestResponse(created))
}

// ListMine GET /api/v1/cli/balance-requests?limit=20 (Bearer)
func (h *BalanceRequestHandler) ListMine(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "user not authenticated")
		return
	}

	limit := 20
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	list, err := h.svc.ListMine(c.Request.Context(), subject.UserID, limit)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]balanceRequestResponse, 0, len(list))
	for i := range list {
		out = append(out, toBalanceRequestResponse(&list[i]))
	}
	response.Success(c, gin.H{"items": out})
}

// ================ 管理员侧 ================

// AdminList GET /api/v1/admin/balance-requests?status=pending&page=1&page_size=20
func (h *BalanceRequestHandler) AdminList(c *gin.Context) {
	status := c.Query("status")
	page, pageSize := response.ParsePagination(c)
	offset := (page - 1) * pageSize

	list, total, err := h.svc.ListAll(c.Request.Context(), status, pageSize, offset)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]balanceRequestResponse, 0, len(list))
	for i := range list {
		out = append(out, toBalanceRequestResponse(&list[i]))
	}
	response.Paginated(c, out, int64(total), page, pageSize)
}

// AdminApprove POST /api/v1/admin/balance-requests/:id/approve
func (h *BalanceRequestHandler) AdminApprove(c *gin.Context) {
	reviewerID, ok := h.extractReviewerID(c)
	if !ok {
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid id")
		return
	}

	var req approveBalanceRequestRequest
	// body 可空：管理员不修改金额时直接 approve
	_ = c.ShouldBindJSON(&req)

	updated, err := h.svc.Approve(c.Request.Context(), id, reviewerID, req.ApprovedAmountUSD)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, toBalanceRequestResponse(updated))
}

// AdminReject POST /api/v1/admin/balance-requests/:id/reject
func (h *BalanceRequestHandler) AdminReject(c *gin.Context) {
	reviewerID, ok := h.extractReviewerID(c)
	if !ok {
		return
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid id")
		return
	}

	var req rejectBalanceRequestRequest
	_ = c.ShouldBindJSON(&req)

	updated, err := h.svc.Reject(c.Request.Context(), id, reviewerID, req.Reason)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, toBalanceRequestResponse(updated))
}

func (h *BalanceRequestHandler) extractReviewerID(c *gin.Context) (int64, bool) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "admin not authenticated")
		return 0, false
	}
	return subject.UserID, true
}
