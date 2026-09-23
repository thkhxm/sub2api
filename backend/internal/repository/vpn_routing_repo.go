package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

func validateVPNRotationNode(ctx context.Context, tx *sql.Tx, id int64) error {
	var available bool
	err := tx.QueryRowContext(ctx, `SELECT enabled AND healthy AND last_checked_at>now()-interval '2 minutes' FROM vpn_servers WHERE id=$1 FOR SHARE`, id).Scan(&available)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !available {
		return service.ErrVPNNoServer
	}
	return err
}

func newVPNMigration(s *service.VPNSubscription, target int64, owner string, manual bool) *service.VPNMigration {
	return &service.VPNMigration{SourceServerID: s.ServerID, SourceOwnerRef: s.OwnerRef, SourceUsername: s.RemoteUsername, TargetServerID: target, TargetOwnerRef: owner, TargetUsername: "pc_" + strings.ReplaceAll(owner, "-", "")[:28], Stage: "revoke_source", QuotaBytes: s.QuotaBytes, Enabled: s.Enabled, ManualTarget: manual}
}

// 先锁订阅再锁操作，与 Queue/Retry 保持一致；租约过期或操作已被替代时禁止提交。
func lockVPNOperation(ctx context.Context, tx *sql.Tx, o *service.VPNOperation) (*service.VPNSubscription, *service.VPNOperation, *service.VPNOperationPayload, error) {
	if o == nil || o.LeaseToken == "" {
		return nil, nil, nil, service.ErrVPNBusy
	}
	s, err := scanVPNSub(tx.QueryRowContext(ctx, `SELECT `+vpnSubColumns+vpnSubJoin+` WHERE s.id=$1 FOR UPDATE OF s`, o.SubscriptionID))
	if err != nil {
		return nil, nil, nil, err
	}
	current := *o
	err = tx.QueryRowContext(ctx, `SELECT payload,dispatched,attempts FROM vpn_operations WHERE id=$1 AND subscription_id=$2 AND lease_token=$3 AND status='running' AND lease_until>now() AND payload=$4::jsonb AND id=(SELECT id FROM vpn_operations WHERE subscription_id=$2 ORDER BY created_at DESC,id DESC LIMIT 1) FOR UPDATE`, o.ID, o.SubscriptionID, o.LeaseToken, string(o.Payload)).Scan(&current.Payload, &current.Dispatched, &current.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil, service.ErrVPNBusy
	}
	if err != nil {
		return nil, nil, nil, err
	}
	var p service.VPNOperationPayload
	if err = json.Unmarshal(current.Payload, &p); err != nil {
		return nil, nil, nil, err
	}
	if p.OperationID != o.ID || s.DeletedAt != nil {
		return nil, nil, nil, service.ErrVPNBusy
	}
	if p.Migration != nil {
		m := p.Migration
		server, owner, username := m.SourceServerID, m.SourceOwnerRef, m.SourceUsername
		if m.Stage == "completed" {
			server, owner, username = m.TargetServerID, m.TargetOwnerRef, m.TargetUsername
		}
		if p.Action != "migrate" || s.ServerID != server || s.OwnerRef != owner || s.RemoteUsername != username {
			return nil, nil, nil, service.ErrVPNBusy
		}
	} else if p.OwnerRef != s.OwnerRef || p.Action == "migrate" {
		return nil, nil, nil, service.ErrVPNBusy
	}
	return s, &current, &p, nil
}

func storeVPNPayload(ctx context.Context, tx *sql.Tx, o *service.VPNOperation, p *service.VPNOperationPayload) ([]byte, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE vpn_operations SET action=$4,payload=$5,updated_at=now() WHERE id=$1 AND lease_token=$2 AND status='running' AND lease_until>now() AND payload=$3::jsonb`, o.ID, o.LeaseToken, string(o.Payload), p.Action, string(b))
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, service.ErrVPNBusy
	}
	return b, nil
}

func (r *vpnRepository) PrepareOperation(ctx context.Context, o *service.VPNOperation) (*service.VPNOperation, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(821407238)`); err != nil {
		return nil, err
	}
	s, current, p, err := lockVPNOperation(ctx, tx, o)
	if err != nil {
		return nil, err
	}
	var enabled bool
	if err = tx.QueryRowContext(ctx, `SELECT enabled FROM vpn_servers WHERE id=$1 FOR SHARE`, s.ServerID).Scan(&enabled); err != nil {
		return nil, err
	}
	if p.Action == "revoke" && p.TargetServerID > 0 {
		if p.TargetServerID != s.ServerID {
			return nil, service.ErrVPNInvalid
		}
		if err := validateVPNRotationNode(ctx, tx, s.ServerID); err != nil {
			return nil, err
		}
	}
	routeNeeded := !enabled && (p.Action == "create" || p.Action == "revoke")
	requiredQuota := s.QuotaBytes
	if p.Action == "create" && !current.Dispatched && p.DataLimit != nil && *p.DataLimit > requiredQuota {
		requiredQuota = *p.DataLimit
	}
	if p.Action == "create" && !current.Dispatched && enabled {
		_, capacityErr := selectVPNServerByQuota(ctx, tx, vpnNodeRequest{RequestedID: s.ServerID, RequiredQuota: requiredQuota, OwnerRef: s.OwnerRef, IgnoreOperationID: o.ID})
		if capacityErr != nil && !errors.Is(capacityErr, service.ErrVPNNoServer) {
			return nil, capacityErr
		}
		routeNeeded = capacityErr != nil
	}
	if routeNeeded {
		owner := s.OwnerRef
		if p.Action != "create" || current.Dispatched {
			owner = uuid.NewString()
		}
		target, e := selectVPNServerByQuota(ctx, tx, vpnNodeRequest{SourceID: s.ServerID, RequiredQuota: requiredQuota, OwnerRef: owner, IgnoreOperationID: o.ID})
		if e != nil {
			return nil, e
		}
		if p.Action == "create" && !current.Dispatched {
			// 从未发送的创建沿用 owner 与操作编号，不会留下需回收的源账号。
			_, err = tx.ExecContext(ctx, `UPDATE vpn_subscriptions SET server_id=$2,updated_at=now() WHERE id=$1`, s.ID, target)
		} else {
			p.Action = "migrate"
			p.Migration = newVPNMigration(s, target, owner, false)
			current.Payload, err = storeVPNPayload(ctx, tx, current, p)
		}
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE vpn_servers SET last_assigned_at=now() WHERE id=$1`, target); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return current, nil
}

func (r *vpnRepository) MarkOperationDispatched(ctx context.Context, o *service.VPNOperation) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	s, _, p, err := lockVPNOperation(ctx, tx, o)
	if err != nil {
		return err
	}
	if p.Action == "create" {
		var enabled bool
		if err = tx.QueryRowContext(ctx, `SELECT enabled FROM vpn_servers WHERE id=$1 FOR SHARE`, s.ServerID).Scan(&enabled); err != nil {
			return err
		}
		if !enabled {
			return service.ErrVPNServerDisabled
		}
	}
	if p.Action == "revoke" && p.TargetServerID > 0 {
		if p.TargetServerID != s.ServerID {
			return service.ErrVPNInvalid
		}
		if err := validateVPNRotationNode(ctx, tx, p.TargetServerID); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE vpn_operations SET dispatched=true,updated_at=now() WHERE id=$1 AND lease_token=$2 AND status='running' AND lease_until>now()`, o.ID, o.LeaseToken)
	if err != nil {
		return err
	}
	if n, e := result.RowsAffected(); e != nil {
		return e
	} else if n != 1 {
		return service.ErrVPNBusy
	}
	if err = tx.Commit(); err == nil {
		o.Dispatched = true
	}
	return err
}

func sameVPNMigrationBinding(a, b *service.VPNMigration) bool {
	return a != nil && b != nil && a.SourceServerID == b.SourceServerID && a.SourceOwnerRef == b.SourceOwnerRef && a.SourceUsername == b.SourceUsername && a.TargetServerID == b.TargetServerID && a.TargetOwnerRef == b.TargetOwnerRef && a.TargetUsername == b.TargetUsername && a.QuotaBytes == b.QuotaBytes && a.Enabled == b.Enabled && a.ManualTarget == b.ManualTarget
}

func (r *vpnRepository) CheckpointMigration(ctx context.Context, o *service.VPNOperation, m *service.VPNMigration) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	s, current, p, err := lockVPNOperation(ctx, tx, o)
	if err != nil {
		return err
	}
	old := p.Migration
	if !sameVPNMigrationBinding(old, m) || old.Stage == "completed" || m.Stage == "completed" || (old.TargetDispatched && !m.TargetDispatched) || (old.TargetRetired && !m.TargetRetired) {
		return service.ErrVPNBusy
	}
	if m.TargetRetired && (!old.TargetDispatched || m.Stage != "create_target") {
		return service.ErrVPNInvalid
	}
	if old.InitialUsage != nil && !reflect.DeepEqual(old.InitialUsage, m.InitialUsage) && (!m.TargetRetired || old.TargetRetired || !validVPNRetargetUsage(old.InitialUsage, m.InitialUsage)) {
		return service.ErrVPNBusy
	}
	if old.Stage == "create_target" && m.Stage == "revoke_source" {
		return service.ErrVPNBusy
	}
	if (m.Stage != "revoke_source" && m.Stage != "create_target") || (m.Stage == "create_target" && m.InitialUsage == nil) || (m.Stage == "revoke_source" && m.TargetDispatched) {
		return service.ErrVPNInvalid
	}
	if !old.TargetDispatched && m.TargetDispatched {
		var enabled bool
		if err = tx.QueryRowContext(ctx, `SELECT enabled FROM vpn_servers WHERE id=$1 FOR SHARE`, m.TargetServerID).Scan(&enabled); err != nil {
			return err
		}
		if !enabled {
			return service.ErrVPNServerDisabled
		}
	}
	if m.TargetRetired && !old.TargetRetired {
		if err = saveVPNBindingHistory(ctx, tx, s, o.ID, old.TargetServerID, old.TargetOwnerRef, old.TargetUsername); err != nil {
			return err
		}
	}
	p.Migration = m
	b, err := storeVPNPayload(ctx, tx, current, p)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err == nil {
		o.Payload = b
	}
	return err
}

func saveVPNBindingHistory(ctx context.Context, tx *sql.Tx, s *service.VPNSubscription, operationID string, serverID int64, owner, username string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO vpn_subscription_binding_history(subscription_id,user_id,server_id,owner_ref,remote_username,operation_id) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(server_id,owner_ref) DO NOTHING`, s.ID, s.UserID, serverID, owner, username, operationID)
	return err
}

func (r *vpnRepository) RetargetMigration(ctx context.Context, o *service.VPNOperation, m *service.VPNMigration) (*service.VPNMigration, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(821407238)`); err != nil {
		return nil, err
	}
	s, current, p, err := lockVPNOperation(ctx, tx, o)
	if err != nil {
		return nil, err
	}
	old := p.Migration
	if !sameVPNMigrationBinding(old, m) || old.Stage == "completed" || old.Stage != m.Stage || old.TargetDispatched != m.TargetDispatched || old.TargetRetired != m.TargetRetired || (old.TargetDispatched && !old.TargetRetired) {
		return nil, service.ErrVPNBusy
	}
	if old.TargetDispatched {
		// 服务层仅在目标的撤销操作已确认生效后调用，历史用于真实流量归属。
		if err = saveVPNBindingHistory(ctx, tx, s, o.ID, old.TargetServerID, old.TargetOwnerRef, old.TargetUsername); err != nil {
			return nil, err
		}
	}
	excluded := old.TargetServerID
	if old.TargetRetired {
		// 确认旧凭据撤销后，可在重新启用的同一节点上创建全新 owner。
		excluded = 0
	}
	newOwner := uuid.NewString()
	requested := int64(0)
	if old.ManualTarget {
		requested = old.TargetServerID
		excluded = 0
	}
	quota := old.QuotaBytes
	if s.QuotaBytes > quota {
		quota = s.QuotaBytes
	}
	target, err := selectVPNServerByQuota(ctx, tx, vpnNodeRequest{SourceID: old.SourceServerID, ExcludedID: excluded, RequestedID: requested, RequiredQuota: quota, OwnerRef: newOwner, IgnoreOperationID: o.ID})
	if err != nil {
		return nil, err
	}
	next := *old
	if old.TargetDispatched {
		if !validVPNRetargetUsage(old.InitialUsage, m.InitialUsage) {
			return nil, service.ErrVPNInvalid
		}
		next.InitialUsage = m.InitialUsage
	} else if !reflect.DeepEqual(old.InitialUsage, m.InitialUsage) {
		return nil, service.ErrVPNInvalid
	}
	next.TargetServerID = target
	next.TargetOwnerRef = newOwner
	next.TargetUsername = "pc_" + strings.ReplaceAll(next.TargetOwnerRef, "-", "")[:28]
	next.TargetDispatched = false
	next.TargetRetired = false
	p.Migration = &next
	b, err := storeVPNPayload(ctx, tx, current, p)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE vpn_servers SET last_assigned_at=now() WHERE id=$1`, target); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	o.Payload = b
	return &next, nil
}

func validVPNRetargetUsage(previous, next *service.VPNInitialUsage) bool {
	if previous == nil || next == nil || next.UploadBytes < 0 || next.DownloadBytes < 0 || next.UsedTraffic < next.UploadBytes+next.DownloadBytes || next.UsedTraffic > service.VPNMaxQuota || !next.PeriodStart.Before(next.PeriodEnd) {
		return false
	}
	if next.PeriodStart.Equal(previous.PeriodStart) && next.PeriodEnd.Equal(previous.PeriodEnd) {
		return next.UploadBytes >= previous.UploadBytes && next.DownloadBytes >= previous.DownloadBytes && next.UsedTraffic >= previous.UsedTraffic
	}
	return !next.PeriodStart.Before(previous.PeriodEnd)
}

func (r *vpnRepository) CompleteMigration(ctx context.Context, o *service.VPNOperation, m *service.VPNMigration, snapshot service.VPNSnapshot, encryptedURL string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	s, current, p, err := lockVPNOperation(ctx, tx, o)
	if err != nil {
		return err
	}
	old := p.Migration
	if !sameVPNMigrationBinding(old, m) {
		return service.ErrVPNBusy
	}
	if old.Stage == "completed" {
		return tx.Commit()
	}
	if old.Stage != "create_target" || !old.TargetDispatched || old.TargetRetired || snapshot.OwnerRef != old.TargetOwnerRef || snapshot.Username != old.TargetUsername || snapshot.ApplyStatus != "applied" || snapshot.DataLimit != old.QuotaBytes {
		return service.ErrVPNInvalid
	}
	var enabled bool
	if err = tx.QueryRowContext(ctx, `SELECT enabled FROM vpn_servers WHERE id=$1 FOR SHARE`, old.TargetServerID).Scan(&enabled); err != nil {
		return err
	}
	if !enabled {
		return service.ErrVPNServerDisabled
	}
	if err = saveVPNBindingHistory(ctx, tx, s, o.ID, old.SourceServerID, old.SourceOwnerRef, old.SourceUsername); err != nil {
		return err
	}
	snapshot.SubscriptionURL = ""
	snapshot.LastOperationID = o.ID
	b, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE vpn_subscriptions SET server_id=$2,owner_ref=$3,remote_username=$4,snapshot=$5,url_encrypted=$6,status=$7,apply_status=$8,access_state=$9,quota_sync_needed=quota_sync_needed OR quota_bytes<>$10,synced_at=now(),sync_attempted_at=now(),last_error='',updated_at=now() WHERE id=$1`, s.ID, old.TargetServerID, old.TargetOwnerRef, old.TargetUsername, string(b), encryptedURL, snapshot.Status, snapshot.ApplyStatus, snapshot.AccessState, snapshot.DataLimit)
	if err != nil {
		return err
	}
	old.Stage = "completed"
	b, err = storeVPNPayload(ctx, tx, current, p)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err == nil {
		o.Payload = b
		m.Stage = "completed"
	}
	return err
}
