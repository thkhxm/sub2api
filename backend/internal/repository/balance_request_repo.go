package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// balanceRequestRepository 提供 balance_requests 表的 CRUD。
//
// 走原生 SQL（*sql.DB）而非 ent，原因：M3 阶段 ent generate 需要 Go 1.26 工具链，
// 而仓库迁移工作流不强依赖 ent client。后续 ent generate 后可平滑切换。
type balanceRequestRepository struct {
	db *sql.DB
}

// NewBalanceRequestRepository 构造仓储，返回 service 层接口（避免 cross-package wire.Bind）。
func NewBalanceRequestRepository(db *sql.DB) service.BalanceRequestRepository {
	return &balanceRequestRepository{db: db}
}

const balanceRequestColumns = `
    id, user_id, amount_usd, approved_amount_usd, note, status,
    reviewer_id, reviewed_at, reject_reason, created_at, updated_at
`

func scanBalanceRequest(scanner interface {
	Scan(dest ...any) error
}) (*service.BalanceRequest, error) {
	var r service.BalanceRequest
	var approvedAmount sql.NullFloat64
	var reviewerID sql.NullInt64
	var reviewedAt sql.NullTime

	if err := scanner.Scan(
		&r.ID,
		&r.UserID,
		&r.AmountUSD,
		&approvedAmount,
		&r.Note,
		&r.Status,
		&reviewerID,
		&reviewedAt,
		&r.RejectReason,
		&r.CreatedAt,
		&r.UpdatedAt,
	); err != nil {
		return nil, err
	}

	if approvedAmount.Valid {
		v := approvedAmount.Float64
		r.ApprovedAmountUSD = &v
	}
	if reviewerID.Valid {
		v := reviewerID.Int64
		r.ReviewerID = &v
	}
	if reviewedAt.Valid {
		r.ReviewedAt = &reviewedAt.Time
	}
	return &r, nil
}

// Create 插入新申请单
func (r *balanceRequestRepository) Create(ctx context.Context, in *service.BalanceRequest) (*service.BalanceRequest, error) {
	if in == nil {
		return nil, errors.New("nil balance request")
	}
	now := time.Now().UTC()
	query := `
INSERT INTO balance_requests (user_id, amount_usd, note, status, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $5)
RETURNING ` + strings.TrimSpace(balanceRequestColumns)

	row := r.db.QueryRowContext(ctx, query, in.UserID, in.AmountUSD, in.Note, in.Status, now)
	return scanBalanceRequest(row)
}

// GetByID 查询单条申请
func (r *balanceRequestRepository) GetByID(ctx context.Context, id int64) (*service.BalanceRequest, error) {
	query := `SELECT ` + strings.TrimSpace(balanceRequestColumns) + `
FROM balance_requests WHERE id = $1`
	row := r.db.QueryRowContext(ctx, query, id)
	br, err := scanBalanceRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrBalanceRequestNotFound
	}
	return br, err
}

// ListByUser 用户自己的申请列表
func (r *balanceRequestRepository) ListByUser(ctx context.Context, userID int64, limit int) ([]service.BalanceRequest, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `SELECT ` + strings.TrimSpace(balanceRequestColumns) + `
FROM balance_requests
WHERE user_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2`
	rows, err := r.db.QueryContext(ctx, query, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectBalanceRequests(rows)
}

// ListAll 管理员列表（可按状态过滤 + 分页）
func (r *balanceRequestRepository) ListAll(ctx context.Context, status string, limit, offset int) ([]service.BalanceRequest, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	var (
		listArgs  []any
		countArgs []any
		where     string
	)
	if status != "" {
		where = "WHERE status = $1"
		listArgs = []any{status, limit, offset}
		countArgs = []any{status}
	} else {
		listArgs = []any{limit, offset}
	}

	listQuery := fmt.Sprintf(`SELECT %s
FROM balance_requests %s
ORDER BY created_at DESC, id DESC
LIMIT $%d OFFSET $%d`, strings.TrimSpace(balanceRequestColumns), where, len(listArgs)-1, len(listArgs))

	rows, err := r.db.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list, err := collectBalanceRequests(rows)
	if err != nil {
		return nil, 0, err
	}

	countQuery := "SELECT COUNT(*) FROM balance_requests " + where
	var total int
	if err := r.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// CountPendingByUser 统计指定用户的 pending 申请数（防重复提交）
func (r *balanceRequestRepository) CountPendingByUser(ctx context.Context, userID int64) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM balance_requests WHERE user_id = $1 AND status = 'pending'`,
		userID,
	).Scan(&n)
	return n, err
}

// UpdateStatus 原子化更新申请状态（仅在 pending 时允许）。
//
// 使用 `WHERE status = 'pending'` + `RETURNING` 一句 SQL 完成行锁 + 状态变更，
// 不需要外层事务（balance 加在 AdminService.UpdateUserBalance 中独立完成）。
func (r *balanceRequestRepository) UpdateStatus(
	ctx context.Context,
	id int64,
	newStatus string,
	reviewerID int64,
	approvedAmount *float64,
	rejectReason string,
) (*service.BalanceRequest, error) {
	now := time.Now().UTC()
	query := `UPDATE balance_requests
SET status = $1,
    reviewer_id = $2,
    reviewed_at = $3,
    approved_amount_usd = $4,
    reject_reason = $5,
    updated_at = $3
WHERE id = $6 AND status = 'pending'
RETURNING ` + strings.TrimSpace(balanceRequestColumns)

	var approvedArg any
	if approvedAmount != nil {
		approvedArg = *approvedAmount
	} else {
		approvedArg = nil
	}

	row := r.db.QueryRowContext(ctx, query, newStatus, reviewerID, now, approvedArg, rejectReason, id)
	br, err := scanBalanceRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		// 区分 "id 不存在" 与 "已被并发修改不再是 pending"
		if _, e := r.GetByID(ctx, id); errors.Is(e, service.ErrBalanceRequestNotFound) {
			return nil, service.ErrBalanceRequestNotFound
		}
		return nil, service.ErrBalanceRequestNotPending
	}
	return br, err
}

func collectBalanceRequests(rows *sql.Rows) ([]service.BalanceRequest, error) {
	var out []service.BalanceRequest
	for rows.Next() {
		br, err := scanBalanceRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *br)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
