package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type vpnRepository struct{ db *sql.DB }

func NewVPNRepository(db *sql.DB) service.VPNRepository { return &vpnRepository{db} }

type vpnScanner interface{ Scan(...any) error }

func vpnError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrVPNNotFound
	}
	return err
}

const vpnServerColumns = `v.id,v.name,v.base_url,v.admin_username,v.credentials_encrypted,v.enabled,v.healthy,v.health_error,v.personal_user_count,v.known_owner_refs,v.last_checked_at,v.created_at,v.updated_at,(SELECT count(*) FROM vpn_subscriptions s WHERE s.server_id=v.id),(SELECT count(*) FROM vpn_subscriptions s WHERE s.server_id=v.id AND s.operation_status IN ('pending','running'))`

func scanVPNServer(row vpnScanner) (*service.VPNServer, error) {
	var v service.VPNServer
	var refs []byte
	err := row.Scan(&v.ID, &v.Name, &v.BaseURL, &v.AdminUsername, &v.CredentialsEncrypted, &v.Enabled, &v.Healthy, &v.HealthError, &v.PersonalUserCount, &refs, &v.LastCheckedAt, &v.CreatedAt, &v.UpdatedAt, &v.AssignedCount, &v.PendingCount)
	if err != nil {
		return nil, vpnError(err)
	}
	if err = json.Unmarshal(refs, &v.KnownOwnerRefs); err != nil {
		return nil, err
	}
	return &v, nil
}
func (r *vpnRepository) ListServers(ctx context.Context) ([]service.VPNServer, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+vpnServerColumns+` FROM vpn_servers v ORDER BY v.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []service.VPNServer{}
	for rows.Next() {
		v, e := scanVPNServer(rows)
		if e != nil {
			return nil, e
		}
		list = append(list, *v)
	}
	return list, rows.Err()
}
func (r *vpnRepository) GetServer(ctx context.Context, id int64) (*service.VPNServer, error) {
	return scanVPNServer(r.db.QueryRowContext(ctx, `SELECT `+vpnServerColumns+` FROM vpn_servers v WHERE v.id=$1`, id))
}
func (r *vpnRepository) SaveServer(ctx context.Context, v *service.VPNServer) (*service.VPNServer, error) {
	if v.ID == 0 {
		err := r.db.QueryRowContext(ctx, `INSERT INTO vpn_servers(name,base_url,admin_username,credentials_encrypted,enabled) VALUES($1,$2,$3,$4,$5) RETURNING id`, v.Name, v.BaseURL, v.AdminUsername, v.CredentialsEncrypted, v.Enabled).Scan(&v.ID)
		if err != nil {
			return nil, err
		}
	} else {
		result, err := r.db.ExecContext(ctx, `UPDATE vpn_servers SET name=$2,base_url=$3,admin_username=$4,credentials_encrypted=$5,enabled=$6,healthy=false,last_checked_at=NULL,updated_at=now() WHERE id=$1 AND (base_url=$3 OR NOT EXISTS(SELECT 1 FROM vpn_subscriptions WHERE server_id=$1))`, v.ID, v.Name, v.BaseURL, v.AdminUsername, v.CredentialsEncrypted, v.Enabled)
		if err != nil {
			return nil, err
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			return nil, service.ErrVPNInvalid
		}
	}
	return r.GetServer(ctx, v.ID)
}
func (r *vpnRepository) UpdateServerHealth(ctx context.Context, id int64, m *service.VPNServerMeta, message string) error {
	if m == nil {
		_, err := r.db.ExecContext(ctx, `UPDATE vpn_servers SET healthy=false,health_error=$2,last_checked_at=now() WHERE id=$1`, id, message)
		return err
	}
	if m.ManagedOwnerRefs == nil {
		m.ManagedOwnerRefs = []string{}
	}
	refs, _ := json.Marshal(m.ManagedOwnerRefs)
	_, err := r.db.ExecContext(ctx, `UPDATE vpn_servers SET healthy=$2,health_error=$3,personal_user_count=$4,known_owner_refs=$5,last_checked_at=now() WHERE id=$1`, id, m.Healthy, message, m.PersonalUserCount, string(refs))
	return err
}

const vpnSubColumns = `s.id,s.user_id,u.email,s.server_id,v.name,s.owner_ref,s.remote_username,s.enabled,s.quota_bytes,s.status,s.apply_status,s.access_state,s.snapshot,s.url_encrypted,s.last_error,s.synced_at,s.operation_status,s.created_at`
const vpnSubJoin = ` FROM vpn_subscriptions s JOIN users u ON u.id=s.user_id JOIN vpn_servers v ON v.id=s.server_id `

func scanVPNSub(row vpnScanner) (*service.VPNSubscription, error) {
	var s service.VPNSubscription
	var snapshot []byte
	err := row.Scan(&s.ID, &s.UserID, &s.UserEmail, &s.ServerID, &s.ServerName, &s.OwnerRef, &s.RemoteUsername, &s.Enabled, &s.QuotaBytes, &s.Status, &s.ApplyStatus, &s.AccessState, &snapshot, &s.URLEncrypted, &s.LastError, &s.SyncedAt, &s.OperationStatus, &s.CreatedAt)
	if err != nil {
		return nil, vpnError(err)
	}
	if err = json.Unmarshal(snapshot, &s.Snapshot); err != nil {
		return nil, err
	}
	return &s, nil
}
func (r *vpnRepository) GetSubscription(ctx context.Context, id int64) (*service.VPNSubscription, error) {
	return scanVPNSub(r.db.QueryRowContext(ctx, `SELECT `+vpnSubColumns+vpnSubJoin+` WHERE s.id=$1`, id))
}
func (r *vpnRepository) GetUserSubscription(ctx context.Context, id int64) (*service.VPNSubscription, error) {
	return scanVPNSub(r.db.QueryRowContext(ctx, `SELECT `+vpnSubColumns+vpnSubJoin+` WHERE s.user_id=$1`, id))
}
func (r *vpnRepository) Eligibility(ctx context.Context, id int64) (bool, string, error) {
	var active, balance, server bool
	err := r.db.QueryRowContext(ctx, `SELECT status='active' AND deleted_at IS NULL,balance>0,EXISTS(SELECT 1 FROM vpn_servers WHERE enabled AND healthy AND last_checked_at>now()-interval '2 minutes') FROM users WHERE id=$1`, id).Scan(&active, &balance, &server)
	if err != nil {
		return false, "", vpnError(err)
	}
	if !active {
		return false, "user_inactive", nil
	}
	if !balance {
		return false, "balance_required", nil
	}
	if !server {
		return false, "no_available_server", nil
	}
	return true, "", nil
}
func (r *vpnRepository) Reserve(ctx context.Context, userID int64, admin bool, actor int64, owner string) (*service.VPNSubscription, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// 统一串行节点选择和预占；远程请求在事务外执行。
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(821407238)`); err != nil {
		return nil, err
	}
	var active, balance bool
	if err = tx.QueryRowContext(ctx, `SELECT status='active' AND deleted_at IS NULL,balance>0 FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&active, &balance); err != nil {
		return nil, vpnError(err)
	}
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM vpn_subscriptions WHERE user_id=$1`, userID).Scan(&id)
	if err == nil {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return r.GetSubscription(ctx, id)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if !active {
		return nil, service.ErrVPNInvalid
	}
	if !admin && !balance {
		return nil, service.ErrVPNBalance
	}
	var serverID int64
	err = tx.QueryRowContext(ctx, `SELECT v.id FROM vpn_servers v WHERE v.enabled AND v.healthy AND v.last_checked_at>now()-interval '2 minutes' ORDER BY v.personal_user_count+(SELECT count(*) FROM vpn_subscriptions s WHERE s.server_id=v.id AND NOT(v.known_owner_refs ? s.owner_ref)),v.last_assigned_at NULLS FIRST,v.id LIMIT 1 FOR UPDATE OF v`).Scan(&serverID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrVPNNoServer
	}
	if err != nil {
		return nil, err
	}
	username := "pc_" + strings.ReplaceAll(owner, "-", "")[:28]
	err = tx.QueryRowContext(ctx, `INSERT INTO vpn_subscriptions(user_id,server_id,owner_ref,remote_username) VALUES($1,$2,$3,$4) RETURNING id`, userID, serverID, owner, username).Scan(&id)
	if err != nil {
		return nil, err
	}
	quota, enabled := service.VPNDefaultQuota, true
	p := service.VPNOperationPayload{OperationID: uuid.NewString(), OwnerRef: owner, Action: "create", DataLimit: &quota, Enabled: &enabled}
	payload, _ := json.Marshal(p)
	if _, err = tx.ExecContext(ctx, `INSERT INTO vpn_operations(id,subscription_id,action,payload,actor_id) VALUES($1,$2,'create',$3,$4)`, p.OperationID, id, string(payload), actor); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE vpn_servers SET last_assigned_at=now() WHERE id=$1`, serverID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetSubscription(ctx, id)
}
func (r *vpnRepository) Queue(ctx context.Context, id, actor int64, p service.VPNOperationPayload) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner string
	var active bool
	err = tx.QueryRowContext(ctx, `SELECT s.owner_ref,u.status='active' AND u.deleted_at IS NULL FROM vpn_subscriptions s JOIN users u ON u.id=s.user_id WHERE s.id=$1 FOR UPDATE OF s`, id).Scan(&owner, &active)
	if err != nil {
		return vpnError(err)
	}
	if p.Enabled != nil && *p.Enabled && !active {
		return service.ErrVPNInvalid
	}
	if actor == 0 && p.Enabled != nil && !*p.Enabled {
		// 平台停用优先于已失败的管理操作，保留取消记录以避免旧操作重试后重新启用。
		if _, err = tx.ExecContext(ctx, `UPDATE vpn_operations SET status='cancelled',updated_at=now() WHERE subscription_id=$1 AND status='failed'`, id); err != nil {
			return err
		}
	}
	var busy bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM vpn_operations WHERE subscription_id=$1 AND status IN ('pending','running','failed'))`, id).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return service.ErrVPNBusy
	}
	p.OwnerRef = owner
	if p.OperationID == "" {
		p.OperationID = uuid.NewString()
	}
	b, _ := json.Marshal(p)
	if _, err = tx.ExecContext(ctx, `INSERT INTO vpn_operations(id,subscription_id,action,payload,actor_id) VALUES($1,$2,$3,$4,$5)`, p.OperationID, id, p.Action, string(b), actor); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE vpn_subscriptions SET enabled=COALESCE($2,enabled),quota_bytes=COALESCE($3,quota_bytes),apply_status='pending',operation_status='pending',last_error='',updated_at=now() WHERE id=$1`, id, p.Enabled, p.DataLimit)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *vpnRepository) Retry(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var found int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM vpn_subscriptions WHERE id=$1 FOR UPDATE`, id).Scan(&found); err != nil {
		return vpnError(err)
	}
	_, err = tx.ExecContext(ctx, `UPDATE vpn_operations SET status='pending',available_at=now(),last_error='',lease_until=NULL,lease_token=NULL,updated_at=now() WHERE subscription_id=$1 AND status='failed'`, id)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE vpn_subscriptions SET operation_status='pending',apply_status='pending',last_error='' WHERE id=$1 AND EXISTS(SELECT 1 FROM vpn_operations WHERE subscription_id=$1 AND status='pending')`, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *vpnRepository) Claim(ctx context.Context) (*service.VPNOperation, error) {
	var o service.VPNOperation
	o.LeaseToken = uuid.NewString()
	err := r.db.QueryRowContext(ctx, `UPDATE vpn_operations SET status='running',lease_until=now()+interval '90 seconds',lease_token=$1,attempts=attempts+1 WHERE id=(SELECT id FROM vpn_operations WHERE status IN ('pending','running') AND available_at<=now() AND (lease_until IS NULL OR lease_until<now()) ORDER BY available_at,created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,subscription_id,payload,attempts`, o.LeaseToken).Scan(&o.ID, &o.SubscriptionID, &o.Payload, &o.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &o, err
}
func (r *vpnRepository) Finish(ctx context.Context, o *service.VPNOperation, state, message string) error {
	_, err := r.db.ExecContext(ctx, `WITH done AS (UPDATE vpn_operations SET status=$3,last_error=$4,available_at=now()+interval '10 seconds',lease_until=NULL,lease_token=NULL,updated_at=now() WHERE id=$1 AND lease_token=$2 RETURNING subscription_id) UPDATE vpn_subscriptions SET operation_status=$3,last_error=$4,apply_status=CASE WHEN $3='failed' THEN 'failed' WHEN $3='succeeded' THEN apply_status ELSE 'pending' END,updated_at=now() WHERE id IN(SELECT subscription_id FROM done)`, o.ID, o.LeaseToken, state, message)
	return err
}
func (r *vpnRepository) SaveSnapshot(ctx context.Context, id int64, s service.VPNSnapshot, encryptedURL string) error {
	s.SubscriptionURL = ""
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE vpn_subscriptions SET snapshot=$2,last_error=CASE WHEN operation_status='succeeded' AND $5='applied' THEN '' ELSE last_error END,url_encrypted=CASE WHEN $3='' THEN url_encrypted ELSE $3 END,status=$4,apply_status=CASE WHEN operation_status='failed' THEN 'failed' WHEN operation_status IN ('pending','running') AND NOT EXISTS(SELECT 1 FROM vpn_operations WHERE subscription_id=$1 AND status IN ('pending','running') AND id=$7) THEN 'pending' ELSE $5 END,access_state=$6,synced_at=now(),sync_attempted_at=now(),updated_at=now() WHERE id=$1 AND owner_ref=$8 AND remote_username=$9 AND $7=(SELECT id FROM vpn_operations WHERE subscription_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1) AND (snapshot->>'sampled_at' IS NULL OR $10::timestamptz IS NULL OR (snapshot->>'sampled_at')::timestamptz<=$10::timestamptz)`, id, string(b), encryptedURL, s.Status, s.ApplyStatus, s.AccessState, s.LastOperationID, s.OwnerRef, s.Username, s.SampledAt)
	return err
}
func (r *vpnRepository) MarkSyncError(ctx context.Context, id int64, message string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE vpn_subscriptions SET last_error=$2,sync_attempted_at=now() WHERE id=$1`, id, message)
	return err
}
func (r *vpnRepository) List(ctx context.Context, f service.VPNFilter) ([]service.VPNSubscription, int, error) {
	where := ` WHERE ($1='' OR u.email ILIKE '%'||$1||'%' OR s.remote_username ILIKE '%'||$1||'%') AND ($2::bigint=0 OR s.server_id=$2) AND ($3='' OR s.status=$3 OR ($3='failed' AND s.apply_status='failed'))`
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*)`+vpnSubJoin+where, f.Query, f.ServerID, f.Status).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+vpnSubColumns+vpnSubJoin+where+` ORDER BY s.id DESC LIMIT $4 OFFSET $5`, f.Query, f.ServerID, f.Status, f.PageSize, (f.Page-1)*f.PageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list := []service.VPNSubscription{}
	for rows.Next() {
		s, e := scanVPNSub(rows)
		if e != nil {
			return nil, 0, e
		}
		list = append(list, *s)
	}
	return list, total, rows.Err()
}
func (r *vpnRepository) Summary(ctx context.Context) (*service.VPNSummary, error) {
	var s service.VPNSummary
	err := r.db.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE status='active' AND apply_status='applied'),count(*) FILTER(WHERE status='disabled'),count(*) FILTER(WHERE status='limited'),count(*) FILTER(WHERE operation_status IN ('pending','running')),count(*) FILTER(WHERE apply_status='failed'),COALESCE(sum((snapshot->>'used_traffic')::bigint),0),COALESCE(sum(quota_bytes),0),(SELECT count(*) FROM vpn_servers WHERE enabled AND healthy AND last_checked_at>now()-interval '2 minutes'),(SELECT count(*) FROM vpn_servers) FROM vpn_subscriptions`).Scan(&s.Total, &s.Active, &s.Disabled, &s.Limited, &s.Pending, &s.Failed, &s.UsedBytes, &s.QuotaBytes, &s.HealthyServers, &s.TotalServers)
	return &s, err
}
func (r *vpnRepository) ids(ctx context.Context, q string) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (r *vpnRepository) SyncCandidates(ctx context.Context) ([]int64, error) {
	return r.ids(ctx, `SELECT id FROM vpn_subscriptions WHERE sync_attempted_at IS NULL OR sync_attempted_at<now()-interval '30 seconds' ORDER BY sync_attempted_at NULLS FIRST,id LIMIT 50`)
}
func (r *vpnRepository) InactiveUserSubscriptions(ctx context.Context) ([]int64, error) {
	return r.ids(ctx, `SELECT s.id FROM vpn_subscriptions s JOIN users u ON u.id=s.user_id WHERE s.enabled AND (u.status<>'active' OR u.deleted_at IS NOT NULL) AND NOT EXISTS(SELECT 1 FROM vpn_operations o WHERE o.subscription_id=s.id AND o.status IN ('pending','running')) LIMIT 50`)
}
func (r *vpnRepository) RequestRefresh(ctx context.Context, id int64) (bool, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE vpn_subscriptions SET refresh_requested_at=now() WHERE id=$1 AND (refresh_requested_at IS NULL OR refresh_requested_at<now()-interval '10 seconds')`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

var _ service.VPNRepository = (*vpnRepository)(nil)
