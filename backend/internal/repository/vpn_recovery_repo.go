package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

// 管理员显式确认后复用卡住的迁移身份；目标曾被投递则禁止改派。
func (r *vpnRepository) queueOfflineMigration(ctx context.Context, id, actor, targetID int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(821407238)`); err != nil {
		return err
	}
	sub, err := scanVPNSub(tx.QueryRowContext(ctx, `SELECT `+vpnSubColumns+vpnSubJoin+` WHERE s.id=$1 FOR UPDATE OF s`, id))
	if err != nil {
		return err
	}
	if sub.DeletedAt != nil || sub.DeleteRequestedAt != nil || sub.ServerID == targetID {
		return service.ErrVPNInvalid
	}
	var offline bool
	if err = tx.QueryRowContext(ctx, `SELECT NOT enabled AND NOT healthy FROM vpn_servers WHERE id=$1 FOR SHARE`, sub.ServerID).Scan(&offline); err != nil {
		return err
	}
	if !offline {
		return service.ErrVPNInvalid
	}
	var opID string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT id,payload FROM vpn_operations WHERE subscription_id=$1 AND status IN ('pending','running','failed') ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, id).Scan(&opID, &raw)
	existing := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var p service.VPNOperationPayload
	if existing {
		if json.Unmarshal(raw, &p) != nil || p.Action != "migrate" || p.Migration == nil {
			return service.ErrVPNBusy
		}
		m := p.Migration
		if m.Stage != "revoke_source" || m.TargetDispatched || m.SourceServerID != sub.ServerID || m.SourceOwnerRef != sub.OwnerRef || m.SourceUsername != sub.RemoteUsername {
			return service.ErrVPNBusy
		}
	} else {
		opID = uuid.NewString()
		p = service.VPNOperationPayload{OperationID: opID, OwnerRef: sub.OwnerRef, Action: "migrate", Migration: newVPNMigration(sub, targetID, uuid.NewString(), true)}
	}
	m := p.Migration
	target, err := selectVPNServerByQuota(ctx, tx, vpnNodeRequest{SourceID: sub.ServerID, RequestedID: targetID, RequiredQuota: m.QuotaBytes, OwnerRef: m.TargetOwnerRef, IgnoreOperationID: opID})
	if err != nil {
		return err
	}
	m.TargetServerID = target
	m.ManualTarget = true
	m.AllowOfflineMigration = true
	p.AllowOfflineMigration = true
	p.TargetServerID = target
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if existing {
		_, err = tx.ExecContext(ctx, `UPDATE vpn_operations SET payload=$2,actor_id=$3,status='pending',available_at=now(),lease_token=NULL,lease_until=NULL,last_error='',updated_at=now() WHERE id=$1`, opID, string(b), actor)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO vpn_operations(id,subscription_id,action,payload,actor_id) VALUES($1,$2,'migrate',$3,$4)`, opID, id, string(b), actor)
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE vpn_subscriptions SET operation_status='pending',apply_status='pending',last_error='',updated_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE vpn_servers SET last_assigned_at=now() WHERE id=$1`, target); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *vpnRepository) ClaimRevocation(ctx context.Context) (*service.VPNCredentialRevocation, error) {
	item := &service.VPNCredentialRevocation{LeaseToken: uuid.NewString()}
	err := r.db.QueryRowContext(ctx, `UPDATE vpn_credential_revocations SET status='running',lease_until=now()+interval '90 seconds',lease_token=$1,attempts=attempts+1 WHERE id=(SELECT id FROM vpn_credential_revocations WHERE status<>'succeeded' AND available_at<=now() AND (lease_until IS NULL OR lease_until<now()) ORDER BY available_at,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,subscription_id,server_id,owner_ref,remote_username`, item.LeaseToken).Scan(&item.ID, &item.SubscriptionID, &item.ServerID, &item.OwnerRef, &item.Username)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return item, err
}
func (r *vpnRepository) FinishRevocation(ctx context.Context, item *service.VPNCredentialRevocation, message string) error {
	state := "pending"
	if message == "" {
		state = "succeeded"
	}
	_, err := r.db.ExecContext(ctx, `UPDATE vpn_credential_revocations SET status=$3,last_error=$4,available_at=now()+interval '60 seconds',lease_until=NULL,lease_token=NULL,updated_at=now() WHERE id=$1 AND lease_token=$2`, item.ID, item.LeaseToken, state, message)
	return err
}
