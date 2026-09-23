//go:build vpnintegration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func vpnDisableServer(t *testing.T, db *sql.DB, id int64) {
	t.Helper()
	_, err := db.Exec(`UPDATE vpn_servers SET enabled=false WHERE id=$1`, id)
	require.NoError(t, err)
}

func vpnMigrationPayload(t *testing.T, o *service.VPNOperation) *service.VPNMigration {
	t.Helper()
	var p service.VPNOperationPayload
	require.NoError(t, json.Unmarshal(o.Payload, &p))
	require.Equal(t, "migrate", p.Action)
	require.NotNil(t, p.Migration)
	return p.Migration
}

func vpnMigrationFixture(t *testing.T) (*vpnRepository, *sql.DB, *service.VPNSubscription, *service.VPNOperation, *service.VPNMigration) {
	t.Helper()
	ctx := context.Background()
	r, db := vpnTestRepository(t)
	source := vpnTestServer(t, r, 0)
	sub := vpnReserve(t, r, vpnTestUser(t, db, 1))
	op, err := r.Claim(ctx)
	require.NoError(t, err)
	require.NoError(t, r.MarkOperationDispatched(ctx, op))
	vpnTestServer(t, r, 0)
	vpnDisableServer(t, db, source.ID)
	op, err = r.PrepareOperation(ctx, op)
	require.NoError(t, err)
	return r, db, sub, op, vpnMigrationPayload(t, op)
}

func vpnSourceRetired(t *testing.T, r *vpnRepository, op *service.VPNOperation, m *service.VPNMigration) {
	t.Helper()
	start := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	m.Stage = "create_target"
	m.InitialUsage = &service.VPNInitialUsage{UploadBytes: 12, DownloadBytes: 34, UsedTraffic: 46, PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), AccountingStatus: "ok"}
	require.NoError(t, r.CheckpointMigration(context.Background(), op, m))
}

func TestVPNRoutingDisabledBeforeSelectionAndDispatch(t *testing.T) {
	r, db := vpnTestRepository(t)
	ctx := context.Background()
	a := vpnTestServer(t, r, 0)
	vpnDisableServer(t, db, a.ID)
	b := vpnTestServer(t, r, 0)
	sub := vpnReserve(t, r, vpnTestUser(t, db, 1))
	require.Equal(t, b.ID, sub.ServerID)
	op, err := r.Claim(ctx)
	require.NoError(t, err)
	require.False(t, op.Dispatched)
	vpnDisableServer(t, db, b.ID)
	_, err = r.Reserve(ctx, sub.UserID, false, sub.UserID, uuid.NewString())
	require.ErrorIs(t, err, service.ErrVPNServerDisabled)
	require.ErrorIs(t, r.MarkOperationDispatched(ctx, op), service.ErrVPNServerDisabled)
	_, err = r.PrepareOperation(ctx, op)
	require.ErrorIs(t, err, service.ErrVPNNoServer)
	c := vpnTestServer(t, r, 0)
	prepared, err := r.PrepareOperation(ctx, op)
	require.NoError(t, err)
	require.Equal(t, op.ID, prepared.ID)
	require.False(t, prepared.Dispatched)
	var p service.VPNOperationPayload
	require.NoError(t, json.Unmarshal(prepared.Payload, &p))
	require.Equal(t, "create", p.Action)
	require.Nil(t, p.Migration)
	fresh, err := r.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, c.ID, fresh.ServerID)
	require.Equal(t, sub.OwnerRef, fresh.OwnerRef)
	require.Equal(t, sub.RemoteUsername, fresh.RemoteUsername)
	require.NoError(t, r.MarkOperationDispatched(ctx, prepared))
	var dispatched bool
	require.NoError(t, db.QueryRow(`SELECT dispatched FROM vpn_operations WHERE id=$1`, op.ID).Scan(&dispatched))
	require.True(t, dispatched)
}

func TestVPNRoutingDispatchedCreateMigratesAndReservesCapacity(t *testing.T) {
	r, db, sub, op, m := vpnMigrationFixture(t)
	ctx := context.Background()
	require.Equal(t, "revoke_source", m.Stage)
	require.Equal(t, sub.ServerID, m.SourceServerID)
	require.Equal(t, sub.OwnerRef, m.SourceOwnerRef)
	require.NotEqual(t, sub.OwnerRef, m.TargetOwnerRef)
	require.True(t, op.Dispatched)
	c := vpnTestServer(t, r, 0)
	// 强制目标节点在无预占计数时更优，证明迁移的目标预占实际参与负载比较。
	_, err := db.Exec(`UPDATE vpn_servers SET last_assigned_at=CASE WHEN id=$1 THEN now()-interval '1 hour' ELSE now() END`, m.TargetServerID)
	require.NoError(t, err)
	other := vpnReserve(t, r, vpnTestUser(t, db, 1))
	require.Equal(t, c.ID, other.ServerID)
	again, err := r.Reserve(ctx, sub.UserID, false, sub.UserID, uuid.NewString())
	require.NoError(t, err)
	require.Equal(t, sub.ID, again.ID)
	require.Equal(t, "pending", again.OperationStatus)
	prepared, err := r.PrepareOperation(ctx, op)
	require.NoError(t, err)
	replayed := vpnMigrationPayload(t, prepared)
	require.Equal(t, *m, *replayed)
	target, err := r.GetServer(ctx, m.TargetServerID)
	require.NoError(t, err)
	target.BaseURL = "https://replacement.invalid"
	_, err = r.SaveServer(ctx, target)
	require.ErrorIs(t, err, service.ErrVPNInvalid)
}

func TestVPNRoutingDisabledRevokeMigratesAndCannotBeSuperseded(t *testing.T) {
	r, db := vpnTestRepository(t)
	ctx := context.Background()
	vpnTestServer(t, r, 0)
	sub := vpnReserve(t, r, vpnTestUser(t, db, 1))
	create, err := r.Claim(ctx)
	require.NoError(t, err)
	vpnApplyOperation(t, r, sub, create, sub.QuotaBytes, "active", "allowed")
	vpnDisableServer(t, db, sub.ServerID)
	vpnTestServer(t, r, 0)
	require.NoError(t, r.Queue(ctx, sub.ID, 1, service.VPNOperationPayload{Action: "revoke"}))
	op, err := r.Claim(ctx)
	require.NoError(t, err)
	op, err = r.PrepareOperation(ctx, op)
	require.NoError(t, err)
	m := vpnMigrationPayload(t, op)
	require.Equal(t, sub.ServerID, m.SourceServerID)
	for _, action := range []string{"delete", "revoke", "update"} {
		off := false
		require.ErrorIs(t, r.Queue(ctx, sub.ID, 0, service.VPNOperationPayload{Action: action, Enabled: &off}), service.ErrVPNBusy)
	}
	require.NoError(t, r.Finish(ctx, op, "failed", "offline"))
	require.ErrorIs(t, r.Queue(ctx, sub.ID, 0, service.VPNOperationPayload{Action: "delete"}), service.ErrVPNBusy)
	require.NoError(t, r.Retry(ctx, sub.ID))
	retry, err := r.Claim(ctx)
	require.NoError(t, err)
	require.Equal(t, op.ID, retry.ID)
	require.Equal(t, *m, *vpnMigrationPayload(t, retry))
}

func TestVPNRoutingStaleLeaseCannotMutateMigration(t *testing.T) {
	r, db, _, op, m := vpnMigrationFixture(t)
	ctx := context.Background()
	vpnSourceRetired(t, r, op, m)
	m.TargetDispatched = true
	require.NoError(t, r.CheckpointMigration(ctx, op, m))
	old := *op
	_, err := db.Exec(`UPDATE vpn_operations SET lease_until=now()-interval '1 second' WHERE id=$1`, op.ID)
	require.NoError(t, err)
	require.ErrorIs(t, r.MarkOperationDispatched(ctx, &old), service.ErrVPNBusy)
	newer, err := r.Claim(ctx)
	require.NoError(t, err)
	require.NotEqual(t, old.LeaseToken, newer.LeaseToken)
	_, err = r.PrepareOperation(ctx, &old)
	require.ErrorIs(t, err, service.ErrVPNBusy)
	require.ErrorIs(t, r.CheckpointMigration(ctx, &old, m), service.ErrVPNBusy)
	_, err = r.RetargetMigration(ctx, &old, m)
	require.ErrorIs(t, err, service.ErrVPNBusy)
	require.ErrorIs(t, r.CompleteMigration(ctx, &old, m, service.VPNSnapshot{}, ""), service.ErrVPNBusy)
	require.NoError(t, r.MarkOperationDispatched(ctx, newer))
	// 同租约下旧 payload 也不能把已确认阶段倒退。
	stale := *newer
	stale.Payload = []byte(`{}`)
	require.ErrorIs(t, r.CheckpointMigration(ctx, &stale, m), service.ErrVPNBusy)
}

func TestVPNRoutingCompletionPreservesQuotaGroupUsageAndHistory(t *testing.T) {
	r, db, sub, op, m := vpnMigrationFixture(t)
	ctx := context.Background()
	vpnSourceRetired(t, r, op, m)
	m.TargetDispatched = true
	require.NoError(t, r.CheckpointMigration(ctx, op, m))
	name, quota := "迁移中改组", sub.QuotaBytes*2
	group, err := r.SaveVPNGroup(ctx, 0, service.VPNGroupInput{Name: &name, QuotaBytes: &quota})
	require.NoError(t, err)
	_, err = r.SetUserVPNGroup(ctx, sub.UserID, group.ID)
	require.NoError(t, err)
	now := time.Now().UTC()
	x := service.VPNSnapshot{OwnerRef: m.TargetOwnerRef, Username: m.TargetUsername, Status: "active", ApplyStatus: "applied", AccessState: "allowed", DataLimit: m.QuotaBytes, UploadBytes: 12, DownloadBytes: 34, UsedTraffic: 46, SampledAt: &now, LastOperationID: op.ID, SubscriptionURL: "https://private.invalid/sub/secret", PeriodStart: &m.InitialUsage.PeriodStart, PeriodEnd: &m.InitialUsage.PeriodEnd}
	require.NoError(t, r.CompleteMigration(ctx, op, m, x, "encrypted-target"))
	require.Equal(t, "completed", m.Stage)
	fresh, err := r.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, sub.ID, fresh.ID)
	require.Equal(t, m.TargetServerID, fresh.ServerID)
	require.Equal(t, m.TargetOwnerRef, fresh.OwnerRef)
	require.Equal(t, m.TargetUsername, fresh.RemoteUsername)
	require.Equal(t, group.ID, fresh.GroupID)
	require.Equal(t, quota, fresh.QuotaBytes)
	require.True(t, fresh.QuotaSyncNeeded)
	require.Equal(t, int64(46), fresh.Snapshot.UsedTraffic)
	require.Equal(t, op.ID, fresh.Snapshot.LastOperationID)
	require.Empty(t, fresh.Snapshot.SubscriptionURL)
	require.Equal(t, "encrypted-target", fresh.URLEncrypted)
	require.NoError(t, r.CompleteMigration(ctx, op, m, x, "encrypted-target"))
	require.NoError(t, r.Finish(ctx, op, "succeeded", ""))
	x.OwnerRef, x.Username = sub.OwnerRef, sub.RemoteUsername
	x.UsedTraffic = 999
	require.NoError(t, r.SaveSnapshot(ctx, sub.ID, x, "late-source"))
	fresh, err = r.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, int64(46), fresh.Snapshot.UsedTraffic)
	require.Equal(t, "encrypted-target", fresh.URLEncrypted)
	refs, err := r.UserOwnerRefs(ctx, sub.UserID)
	require.NoError(t, err)
	require.Equal(t, []string{sub.OwnerRef}, refs[sub.ServerID])
	require.Equal(t, []string{m.TargetOwnerRef}, refs[m.TargetServerID])
	var histories int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM vpn_subscription_binding_history WHERE subscription_id=$1`, sub.ID).Scan(&histories))
	require.Equal(t, 1, histories)
}

func TestVPNRoutingRetargetPersistsUsageAndRetiredTarget(t *testing.T) {
	r, db, sub, op, m := vpnMigrationFixture(t)
	ctx := context.Background()
	firstTarget := *m
	vpnDisableServer(t, db, m.TargetServerID)
	c := vpnTestServer(t, r, 0)
	next, err := r.RetargetMigration(ctx, op, m)
	require.NoError(t, err)
	require.Equal(t, "revoke_source", next.Stage)
	require.Equal(t, c.ID, next.TargetServerID)
	require.NotEqual(t, m.TargetOwnerRef, next.TargetOwnerRef)
	refs, err := r.UserOwnerRefs(ctx, sub.UserID)
	require.NoError(t, err)
	require.NotContains(t, refs, firstTarget.TargetServerID)
	m = next
	vpnSourceRetired(t, r, op, m)
	m.TargetDispatched = true
	require.NoError(t, r.CheckpointMigration(ctx, op, m))
	vpnDisableServer(t, db, m.TargetServerID)
	d := vpnTestServer(t, r, 0)
	// 已派发目标被撤销后的最终读数必须续接，不能回退到首次源读数。
	m.InitialUsage.DownloadBytes += 9
	m.InitialUsage.UsedTraffic += 9
	m.TargetRetired = true
	require.NoError(t, r.CheckpointMigration(ctx, op, m))
	next, err = r.RetargetMigration(ctx, op, m)
	require.NoError(t, err)
	require.Equal(t, d.ID, next.TargetServerID)
	require.Equal(t, "create_target", next.Stage)
	require.False(t, next.TargetDispatched)
	require.False(t, next.TargetRetired)
	require.Equal(t, int64(55), next.InitialUsage.UsedTraffic)
	require.Equal(t, int64(55), vpnMigrationPayload(t, op).InitialUsage.UsedTraffic)
	refs, err = r.UserOwnerRefs(ctx, sub.UserID)
	require.NoError(t, err)
	require.Equal(t, []string{m.TargetOwnerRef}, refs[m.TargetServerID])
	require.NotContains(t, refs, firstTarget.TargetServerID)
}

func TestVPNRoutingCompleteRejectsDisabledTarget(t *testing.T) {
	r, db, sub, op, m := vpnMigrationFixture(t)
	ctx := context.Background()
	vpnSourceRetired(t, r, op, m)
	m.TargetDispatched = true
	require.NoError(t, r.CheckpointMigration(ctx, op, m))
	vpnDisableServer(t, db, m.TargetServerID)
	x := service.VPNSnapshot{OwnerRef: m.TargetOwnerRef, Username: m.TargetUsername, ApplyStatus: "applied", DataLimit: m.QuotaBytes}
	require.ErrorIs(t, r.CompleteMigration(ctx, op, m, x, "disabled-url"), service.ErrVPNServerDisabled)
	fresh, err := r.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, sub.ServerID, fresh.ServerID)
	require.Empty(t, fresh.URLEncrypted)
	require.Equal(t, "create_target", vpnMigrationPayload(t, op).Stage)
}

func TestVPNRoutingRetiredTargetCannotCompleteOldBindingButCanAllocateFreshOwner(t *testing.T) {
	r, db, sub, op, m := vpnMigrationFixture(t)
	ctx := context.Background()
	vpnSourceRetired(t, r, op, m)
	m.TargetDispatched = true
	require.NoError(t, r.CheckpointMigration(ctx, op, m))
	vpnDisableServer(t, db, m.TargetServerID)
	_, err := r.RetargetMigration(ctx, op, m)
	require.ErrorIs(t, err, service.ErrVPNBusy)
	m.TargetRetired = true
	m.InitialUsage.DownloadBytes += 5
	m.InitialUsage.UsedTraffic += 5
	require.NoError(t, r.CheckpointMigration(ctx, op, m))
	_, err = r.RetargetMigration(ctx, op, m)
	require.ErrorIs(t, err, service.ErrVPNNoServer)
	_, err = db.Exec(`UPDATE vpn_servers SET enabled=true WHERE id=$1`, m.TargetServerID)
	require.NoError(t, err)
	persisted := vpnMigrationPayload(t, op)
	require.True(t, persisted.TargetRetired)
	require.Equal(t, int64(51), persisted.InitialUsage.UsedTraffic)
	x := service.VPNSnapshot{OwnerRef: m.TargetOwnerRef, Username: m.TargetUsername, ApplyStatus: "applied", DataLimit: m.QuotaBytes}
	require.ErrorIs(t, r.CompleteMigration(ctx, op, m, x, "retired-url"), service.ErrVPNInvalid)
	m.TargetRetired = false
	require.ErrorIs(t, r.CheckpointMigration(ctx, op, m), service.ErrVPNBusy)
	refs, err := r.UserOwnerRefs(ctx, sub.UserID)
	require.NoError(t, err)
	require.Equal(t, []string{m.TargetOwnerRef}, refs[m.TargetServerID])
	next, err := r.RetargetMigration(ctx, op, persisted)
	require.NoError(t, err)
	require.Equal(t, persisted.TargetServerID, next.TargetServerID)
	require.NotEqual(t, persisted.TargetOwnerRef, next.TargetOwnerRef)
	require.NotEqual(t, persisted.TargetUsername, next.TargetUsername)
	require.False(t, next.TargetRetired)
	require.Equal(t, int64(51), next.InitialUsage.UsedTraffic)
}

func TestVPNRoutingMigrationBackfillsUncertainLegacyOperations(t *testing.T) {
	r, db := vpnTestRepositoryMigrations(t, false)
	server := vpnTestServer(t, r, 0)
	type legacyOperation struct {
		action, status string
		dispatched     bool
	}
	operations := []legacyOperation{{"create", "pending", true}, {"create", "running", true}, {"revoke", "pending", true}, {"revoke", "running", true}, {"create", "failed", true}, {"create", "succeeded", false}, {"update", "pending", false}}
	ids := make([]string, len(operations))
	for i, operation := range operations {
		user := vpnTestUser(t, db, 1)
		owner := uuid.NewString()
		var sub int64
		require.NoError(t, db.QueryRow(`INSERT INTO vpn_subscriptions(user_id,server_id,owner_ref,remote_username,group_id) VALUES($1,$2,$3,$4,(SELECT id FROM vpn_user_groups WHERE is_default)) RETURNING id`, user, server.ID, owner, owner[:28]).Scan(&sub))
		ids[i] = uuid.NewString()
		_, err := db.Exec(`INSERT INTO vpn_operations(id,subscription_id,action,status,payload) VALUES($1,$2,$3,$4,'{}')`, ids[i], sub, operation.action, operation.status)
		require.NoError(t, err)
	}
	migration, err := os.ReadFile("../../migrations/241_vpn_node_routing.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	for i, operation := range operations {
		var dispatched bool
		require.NoError(t, db.QueryRow(`SELECT dispatched FROM vpn_operations WHERE id=$1`, ids[i]).Scan(&dispatched))
		require.Equal(t, operation.dispatched, dispatched, "%s/%s", operation.action, operation.status)
	}
}
