package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// BalanceRequest 状态常量
const (
	BalanceRequestStatusPending  = "pending"
	BalanceRequestStatusApproved = "approved"
	BalanceRequestStatusRejected = "rejected"
)

// 申请金额上限（USD），管理员审批时也会受这个上限约束
const (
	BalanceRequestMaxAmountUSD = 10000.0
)

var (
	ErrBalanceRequestNotFound    = infraerrors.NotFound("BALANCE_REQUEST_NOT_FOUND", "balance request not found")
	ErrBalanceRequestNotPending  = infraerrors.Conflict("BALANCE_REQUEST_NOT_PENDING", "balance request is not pending; cannot modify")
	ErrBalanceRequestAmountRange = infraerrors.BadRequest("BALANCE_REQUEST_AMOUNT_RANGE", "amount must be in (0, 10000]")
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

	// BeginTx + UpdateStatusTx + AddUserBalanceTx 用于 approve 流程的事务一致性
	BeginTx(ctx context.Context) (*sql.Tx, error)
	UpdateStatusTx(ctx context.Context, tx *sql.Tx, id int64, newStatus string, reviewerID int64, approvedAmount *float64, rejectReason string) (*BalanceRequest, error)
	AddUserBalanceTx(ctx context.Context, tx *sql.Tx, userID int64, amount float64) error
}

// BalanceRequestService 负责 PunkcodeAI 桌面端的余额申请业务逻辑。
type BalanceRequestService struct {
	repo BalanceRequestRepository
}

// NewBalanceRequestService 构造服务
func NewBalanceRequestService(repo BalanceRequestRepository) *BalanceRequestService {
	return &BalanceRequestService{repo: repo}
}

// CreateRequest 用户提交申请
func (s *BalanceRequestService) CreateRequest(ctx context.Context, userID int64, amountUSD float64, note string) (*BalanceRequest, error) {
	if amountUSD <= 0 || amountUSD > BalanceRequestMaxAmountUSD {
		return nil, ErrBalanceRequestAmountRange
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
//   - approvedAmountUSD 为 nil 表示按原申请金额；非 nil 时使用指定金额（必须在合法区间内）
//   - 在同一事务中更新申请状态 + 给用户加余额
func (s *BalanceRequestService) Approve(ctx context.Context, requestID, reviewerID int64, approvedAmountUSD *float64) (*BalanceRequest, error) {
	// 先取出申请，校验状态 + 取 user_id
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

	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	updated, err := s.repo.UpdateStatusTx(ctx, tx, requestID, BalanceRequestStatusApproved, reviewerID, &finalAmount, "")
	if err != nil {
		return nil, err
	}
	if err = s.repo.AddUserBalanceTx(ctx, tx, existing.UserID, finalAmount); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
}

// Reject 管理员拒绝申请
func (s *BalanceRequestService) Reject(ctx context.Context, requestID, reviewerID int64, reason string) (*BalanceRequest, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) > 500 {
		reason = reason[:500]
	}
	// 走非事务版本：拒绝不需要改 users 表
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	updated, err := s.repo.UpdateStatusTx(ctx, tx, requestID, BalanceRequestStatusRejected, reviewerID, nil, reason)
	if err != nil {
		// 区分 "已不是 pending" 与 "不存在"
		if errors.Is(err, ErrBalanceRequestNotPending) {
			// 看看是不是不存在
			if _, e := s.repo.GetByID(ctx, requestID); errors.Is(e, ErrBalanceRequestNotFound) {
				return nil, ErrBalanceRequestNotFound
			}
		}
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
}
