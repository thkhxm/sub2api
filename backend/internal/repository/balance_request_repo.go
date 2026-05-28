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

// NewBalanceRequestRepository 构造仓储，返回 service 层接口以简化 wire 绑定（无需 wire.Bind）。
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

// Create 插入新的申请单，返回填充了 ID 的实体
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

// ListByUser 用户查看自己的申请列表（最新在前）
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

// ListAll 管理员列表，可选按状态过滤
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

// UpdateStatus 原子化更新状态（仅在 pending 时允许）。
// 同时把 reviewer / reviewed_at / approved_amount / reject_reason 一并写入。
// 返回更新后的行；若状态不再是 pending（被并发修改）返回 ErrBalanceRequestNotPending。
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
		// 要么 id 不存在，要么 status 不是 pending
		// 区分一下
		existing, e := r.GetByID(ctx, id)
		if errors.Is(e, service.ErrBalanceRequestNotFound) {
			return nil, service.ErrBalanceRequestNotFound
		}
		if e != nil {
			return nil, e
		}
		_ = existing
		return nil, service.ErrBalanceRequestNotPending
	}
	return br, err
}

// AddUserBalance 给用户增加余额（事务内）。
// approve 流程用：在同一事务中 update 申请状态 + 给用户加钱。
//
// 注意：本方法独立于 BalanceRequestRepository 主线（用同一个 *sql.DB），
// 调用方在 service 层负责事务边界（开 tx → 改 balance_request → 改 user → commit）。
//
// 这里返回事务以让 service 层组合操作。
func (r *balanceRequestRepository) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return r.db.BeginTx(ctx, nil)
}

// AddUserBalanceTx 在指定事务内给用户加余额
func (r *balanceRequestRepository) AddUserBalanceTx(ctx context.Context, tx *sql.Tx, userID int64, amount float64) error {
	if amount <= 0 {
		return errors.New("amount must be positive")
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE users SET balance = balance + $1, updated_at = NOW() WHERE id = $2`,
		amount, userID,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("user not found")
	}
	return nil
}

// UpdateStatusTx 在指定事务内更新状态
func (r *balanceRequestRepository) UpdateStatusTx(
	ctx context.Context,
	tx *sql.Tx,
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

	row := tx.QueryRowContext(ctx, query, newStatus, reviewerID, now, approvedArg, rejectReason, id)
	br, err := scanBalanceRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
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
