package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

const vpnGroupColumns = `g.id,g.name,g.quota_bytes,g.is_default,
(SELECT count(*) FROM vpn_user_group_members m JOIN users u ON u.id=m.user_id WHERE m.group_id=g.id AND u.deleted_at IS NULL),
(SELECT count(*) FROM vpn_subscriptions s WHERE s.group_id=g.id AND s.deleted_at IS NULL),
(SELECT count(*) FROM vpn_subscriptions s WHERE s.group_id=g.id AND s.deleted_at IS NULL AND s.operation_status<>'failed' AND (s.quota_sync_needed OR s.operation_status IN ('pending','running'))),
(SELECT count(*) FROM vpn_subscriptions s WHERE s.group_id=g.id AND s.deleted_at IS NULL AND s.operation_status='failed')`

func scanVPNGroup(row vpnScanner) (*service.VPNGroup, error) {
	var g service.VPNGroup
	err := row.Scan(&g.ID, &g.Name, &g.QuotaBytes, &g.IsDefault, &g.MemberCount, &g.SubscriptionCount, &g.PendingCount, &g.FailedCount)
	return &g, vpnError(err)
}
func (r *vpnRepository) ListVPNGroups(ctx context.Context) ([]service.VPNGroup, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+vpnGroupColumns+` FROM vpn_user_groups g ORDER BY g.is_default DESC,g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []service.VPNGroup{}
	for rows.Next() {
		g, e := scanVPNGroup(rows)
		if e != nil {
			return nil, e
		}
		groups = append(groups, *g)
	}
	return groups, rows.Err()
}
func (r *vpnRepository) SaveVPNGroup(ctx context.Context, id int64, in service.VPNGroupInput) (*service.VPNGroup, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(821407238)`); err != nil {
		return nil, err
	}
	if id == 0 {
		err = tx.QueryRowContext(ctx, `INSERT INTO vpn_user_groups(name,quota_bytes) VALUES($1,$2) RETURNING id`, in.Name, in.QuotaBytes).Scan(&id)
	} else {
		var found int64
		err = tx.QueryRowContext(ctx, `UPDATE vpn_user_groups SET name=COALESCE($2,name),quota_bytes=COALESCE($3,quota_bytes),updated_at=now() WHERE id=$1 RETURNING id`, id, in.Name, in.QuotaBytes).Scan(&found)
		if err == nil && in.QuotaBytes != nil {
			_, err = tx.ExecContext(ctx, `UPDATE vpn_subscriptions s SET quota_bytes=$2,quota_sync_needed=true,updated_at=now() FROM vpn_user_group_members m WHERE s.user_id=m.user_id AND m.group_id=$1 AND s.deleted_at IS NULL AND s.delete_requested_at IS NULL`, id, *in.QuotaBytes)
		}
	}
	if err != nil {
		var pg *pq.Error
		if errors.As(err, &pg) && pg.Code == "23505" {
			return nil, service.ErrVPNInvalid
		}
		return nil, vpnError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return scanVPNGroup(r.db.QueryRowContext(ctx, `SELECT `+vpnGroupColumns+` FROM vpn_user_groups g WHERE g.id=$1`, id))
}
func (r *vpnRepository) UserVPNGroup(ctx context.Context, userID int64) (*service.VPNUserGroup, error) {
	var result service.VPNUserGroup
	err := r.db.QueryRowContext(ctx, `SELECT u.id,g.id,g.name,g.quota_bytes FROM users u LEFT JOIN vpn_user_group_members m ON m.user_id=u.id JOIN vpn_user_groups g ON g.id=COALESCE(m.group_id,(SELECT id FROM vpn_user_groups WHERE is_default)) WHERE u.id=$1 AND u.deleted_at IS NULL`, userID).Scan(&result.UserID, &result.GroupID, &result.GroupName, &result.QuotaBytes)
	return &result, vpnError(err)
}
func (r *vpnRepository) SetUserVPNGroup(ctx context.Context, userID, groupID int64) (*service.VPNUserGroup, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(821407238)`); err != nil {
		return nil, err
	}
	var found, quota int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, userID).Scan(&found); err != nil {
		return nil, vpnError(err)
	}
	if err = tx.QueryRowContext(ctx, `SELECT quota_bytes FROM vpn_user_groups WHERE id=$1 FOR SHARE`, groupID).Scan(&quota); err != nil {
		return nil, vpnError(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO vpn_user_group_members(user_id,group_id) VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET group_id=excluded.group_id,updated_at=now()`, userID, groupID)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE vpn_subscriptions SET group_id=$2,quota_bytes=$3,quota_sync_needed=true,updated_at=now() WHERE user_id=$1 AND deleted_at IS NULL AND delete_requested_at IS NULL`, userID, groupID, quota)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return r.UserVPNGroup(ctx, userID)
}
func (r *vpnRepository) QuotaSyncCandidates(ctx context.Context) ([]int64, error) {
	return r.ids(ctx, `SELECT s.id FROM vpn_subscriptions s WHERE s.quota_sync_needed AND s.deleted_at IS NULL AND s.delete_requested_at IS NULL AND NOT EXISTS(SELECT 1 FROM vpn_operations o WHERE o.subscription_id=s.id AND o.status IN ('pending','running','failed')) ORDER BY s.updated_at,s.id LIMIT 50`)
}
func (r *vpnRepository) QueueQuotaSync(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var quota int64
	var owner string
	var needed bool
	err = tx.QueryRowContext(ctx, `SELECT quota_bytes,owner_ref,quota_sync_needed AND deleted_at IS NULL AND delete_requested_at IS NULL FROM vpn_subscriptions WHERE id=$1 FOR UPDATE`, id).Scan(&quota, &owner, &needed)
	if err != nil {
		return vpnError(err)
	}
	if !needed {
		return nil
	}
	var busy bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM vpn_operations WHERE subscription_id=$1 AND status IN ('pending','running','failed'))`, id).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return nil
	}
	p := service.VPNOperationPayload{OperationID: uuid.NewString(), OwnerRef: owner, Action: "update", DataLimit: &quota}
	b, _ := json.Marshal(p)
	_, err = tx.ExecContext(ctx, `INSERT INTO vpn_operations(id,subscription_id,action,payload) VALUES($1,$2,'update',$3)`, p.OperationID, id, string(b))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE vpn_subscriptions SET operation_status='pending',apply_status='pending',last_error='',updated_at=now() WHERE id=$1`, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
