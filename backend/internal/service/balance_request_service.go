package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// BalanceRequest 状态常量
const (
	BalanceRequestStatusPending  = "pending"
	BalanceRequestStatusApproved = "approved"
	BalanceRequestStatusRejected = "rejected"
)

// 申请上限
const (
	BalanceRequestMaxAmountUSD = 10000.0
	// 单用户允许的最大 pending 申请数，防止用户连点提交多单
	BalanceRequestMaxPendingPerUser = 3
)

var (
	ErrBalanceRequestNotFound      = infraerrors.NotFound("BALANCE_REQUEST_NOT_FOUND", "balance request not found")
	ErrBalanceRequestNotPending    = infraerrors.Conflict("BALANCE_REQUEST_NOT_PENDING", "balance request is not pending; cannot modify")
	ErrBalanceRequestAmountRange   = infraerrors.BadRequest("BALANCE_REQUEST_AMOUNT_RANGE", "amount must be in (0, 10000]")
	ErrBalanceRequestTooManyPending = infraerrors.Conflict("BALANCE_REQUEST_TOO_MANY_PENDING", "you have too many pending balance requests; wait for admin review")
)

// BalanceRequest 是 balance_requests 表的领域模型
type BalanceRequest struct {
	ID                int64
	UserID            int64
	AmountUSD         float64
	ApprovedAmountUSD *float64
	Note              string
	Status            string // pending / approved / rejected
	ReviewerID        *int64
	ReviewedAt        *time.Time
	RejectReason      string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// BalanceRequestRepository 由仓储层实现
type BalanceRequestRepository interface {
	Create(ctx context.Context, in *BalanceRequest) (*BalanceRequest, error)
	GetByID(ctx context.Context, id int64) (*BalanceRequest, error)
	ListByUser(ctx context.Context, userID int64, limit int) ([]BalanceRequest, error)
	ListAll(ctx context.Context, status string, limit, offset int) ([]BalanceRequest, int, error)
	CountPendingByUser(ctx context.Context, userID int64) (int, error)
	UpdateStatus(ctx context.Context, id int64, newStatus string, reviewerID int64, approvedAmount *float64, rejectReason string) (*BalanceRequest, error)
}

// BalanceRequestService 负责 PunkcodeAI 桌面端的余额申请业务逻辑。
//
// approve 流程委托给 AdminService.UpdateUserBalance 完成"加余额 + 缓存失效 + 写
// balance-history 审计记录（admin_balance 类型）"链路，避免与 sub2api 既有的余额
// 调整流程脱节。
type BalanceRequestService struct {
	repo  BalanceRequestRepository
	admin AdminService
}

// NewBalanceRequestService 构造服务
func NewBalanceRequestService(repo BalanceRequestRepository, admin AdminService) *BalanceRequestService {
	return &BalanceRequestService{repo: repo, admin: admin}
}

// CreateRequest 用户提交申请
func (s *BalanceRequestService) CreateRequest(ctx context.Context, userID int64, amountUSD float64, note string) (*BalanceRequest, error) {
	if amountUSD <= 0 || amountUSD > BalanceRequestMaxAmountUSD {
		return nil, ErrBalanceRequestAmountRange
	}

	// 防重复申请：同用户 pending 申请数受限
	pendingCount, err := s.repo.CountPendingByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("count pending requests: %w", err)
	}
	if pendingCount >= BalanceRequestMaxPendingPerUser {
		return nil, ErrBalanceRequestTooManyPending
	}

	note = strings.TrimSpace(note)
	if len(note) > 1000 {
		note = note[:1000]
	}
	return s.repo.Create(ctx, &BalanceRequest{
		UserID:    userID,
		AmountUSD: amountUSD,
		Note:      note,
		Status:    BalanceRequestStatusPending,
	})
}

// ListMine 用户自己的申请列表
func (s *BalanceRequestService) ListMine(ctx context.Context, userID int64, limit int) ([]BalanceRequest, error) {
	return s.repo.ListByUser(ctx, userID, limit)
}

// ListAll 管理员列出全部申请
func (s *BalanceRequestService) ListAll(ctx context.Context, status string, limit, offset int) ([]BalanceRequest, int, error) {
	if status != "" &&
		status != BalanceRequestStatusPending &&
		status != BalanceRequestStatusApproved &&
		status != BalanceRequestStatusRejected {
		return nil, 0, infraerrors.BadRequest("INVALID_STATUS_FILTER", "status must be pending|approved|rejected")
	}
	return s.repo.ListAll(ctx, status, limit, offset)
}

// Approve 管理员批准申请。
//
// 流程：
//  1. 校验申请存在且为 pending
//  2. 调用 AdminService.UpdateUserBalance("add") —— 这一步带完整链路：
//     - users.balance += finalAmount
//     - InvalidateAuthCacheByUserID（API Key 鉴权缓存失效，确保桌面端调用 /v1 立即可见）
//     - InvalidateUserBalance（billing 缓存失效）
//     - 写一条 RedeemCode 审计记录（type=admin_balance），在 admin/users/:id/balance-history 可见
//  3. 改 balance_requests.status = approved
//
// 若 step 2 成功但 step 3 失败，余额已加但状态没改：记录严重错误日志，下次管理员
// 可手动修复（重新点 approve 会因 ErrBalanceRequestNotPending 报错触发回查）。
func (s *BalanceRequestService) Approve(ctx context.Context, requestID, reviewerID int64, approvedAmountUSD *float64) (*BalanceRequest, error) {
	existing, err := s.repo.GetByID(ctx, requestID)
	if err != nil {
		return nil, err
	}
	if existing.Status != BalanceRequestStatusPending {
		return nil, ErrBalanceRequestNotPending
	}

	finalAmount := existing.AmountUSD
	if approvedAmountUSD != nil {
		if *approvedAmountUSD <= 0 || *approvedAmountUSD > BalanceRequestMaxAmountUSD {
			return nil, ErrBalanceRequestAmountRange
		}
		finalAmount = *approvedAmountUSD
	}

	// Step 1: 走 AdminService 完整链路加余额
	notes := fmt.Sprintf("PunkcodeAI balance request #%d approved by user %d", existing.ID, reviewerID)
	if _, err := s.admin.UpdateUserBalance(ctx, existing.UserID, finalAmount, "add", notes); err != nil {
		return nil, fmt.Errorf("approve update balance: %w", err)
	}

	// Step 2: 改申请状态。失败必须强告警（余额已加但状态没改）。
	updated, err := s.repo.UpdateStatus(ctx, requestID, BalanceRequestStatusApproved, reviewerID, &finalAmount, "")
	if err != nil {
		logger.LegacyPrintf("service.balance_request",
			"CRITICAL: balance added but request status update failed; request_id=%d user_id=%d amount=%.2f err=%v",
			requestID, existing.UserID, finalAmount, err,
		)
		return nil, fmt.Errorf("balance credited but request status update failed (manual reconciliation needed): %w", err)
	}
	return updated, nil
}

// Reject 管理员拒绝申请
func (s *BalanceRequestService) Reject(ctx context.Context, requestID, reviewerID int64, reason string) (*BalanceRequest, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) > 500 {
		reason = reason[:500]
	}

	// 先校验存在 + pending
	existing, err := s.repo.GetByID(ctx, requestID)
	if err != nil {
		return nil, err
	}
	if existing.Status != BalanceRequestStatusPending {
		return nil, ErrBalanceRequestNotPending
	}

	updated, err := s.repo.UpdateStatus(ctx, requestID, BalanceRequestStatusRejected, reviewerID, nil, reason)
	if err != nil {
		if errors.Is(err, ErrBalanceRequestNotPending) {
			return nil, ErrBalanceRequestNotPending
		}
		return nil, err
	}
	return updated, nil
}
