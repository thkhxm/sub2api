//go:build vpnintegration

package repository

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestVPNOfflineRecoveryReusesPendingOperationAndPersistsCleanup(t *testing.T) {
	ctx := context.Background()
	r, db, sub, source := vpnManualAppliedSource(t)
	target := vpnTestServer(t, r, 0)
	require.NoError(t, r.Queue(ctx, sub.ID, sub.UserID, service.VPNOperationPayload{Action: "revoke", TargetServerID: target.ID}))
	stale, err := r.Claim(ctx)
	require.NoError(t, err)
	original := vpnMigrationPayload(t, stale)
	_, err = db.Exec(`UPDATE vpn_servers SET enabled=false,healthy=false WHERE id=$1`, source.ID)
	require.NoError(t, err)
	saved, err := r.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	require.True(t, saved.OfflineMigrationAvailable)
	require.NoError(t, r.Queue(ctx, sub.ID, sub.UserID, service.VPNOperationPayload{Action: "revoke", TargetServerID: target.ID, AllowOfflineMigration: true}))
	require.ErrorIs(t, r.MarkOperationDispatched(ctx, stale), service.ErrVPNBusy)
	op, err := r.Claim(ctx)
	require.NoError(t, err)
	require.Equal(t, stale.ID, op.ID)
	m := vpnMigrationPayload(t, op)
	require.Equal(t, original.TargetOwnerRef, m.TargetOwnerRef)
	require.True(t, m.AllowOfflineMigration)
	m.Stage = "create_target"
	m.SourceRevocationPending = true
	m.InitialUsage = &service.VPNInitialUsage{UsedTraffic: 11, PeriodStart: time.Now().UTC().Add(-time.Hour), PeriodEnd: time.Now().UTC().Add(time.Hour), AccountingStatus: "gap_detected"}
	require.NoError(t, r.CheckpointMigration(ctx, op, m))
	require.NoError(t, r.CheckpointMigration(ctx, op, m))
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM vpn_credential_revocations`).Scan(&count))
	require.Equal(t, 1, count)
	item, err := r.ClaimRevocation(ctx)
	require.NoError(t, err)
	require.Equal(t, service.VPNSourceRevocationID(op.ID), item.ID)
	saved, err = r.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	require.True(t, saved.SourceRevocationPending)
	require.False(t, saved.OfflineMigrationAvailable)
	require.NoError(t, r.FinishRevocation(ctx, item, ""))
	saved, err = r.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	require.False(t, saved.SourceRevocationPending)
	require.Equal(t, source.ID, saved.ServerID)
	require.Equal(t, int64(11), saved.Snapshot.UsedTraffic)
}
func TestVPNOfflineRecoveryRejectsHealthySourceAndDispatchedTarget(t *testing.T) {
	ctx := context.Background()
	r, db, sub, source := vpnManualAppliedSource(t)
	target := vpnTestServer(t, r, 0)
	p := service.VPNOperationPayload{Action: "revoke", TargetServerID: target.ID, AllowOfflineMigration: true}
	require.ErrorIs(t, r.Queue(ctx, sub.ID, sub.UserID, p), service.ErrVPNInvalid)
	_, err := db.Exec(`UPDATE vpn_servers SET enabled=false,healthy=false WHERE id=$1`, source.ID)
	require.NoError(t, err)
	require.NoError(t, r.Queue(ctx, sub.ID, sub.UserID, p))
	op, err := r.Claim(ctx)
	require.NoError(t, err)
	m := vpnMigrationPayload(t, op)
	m.Stage = "create_target"
	m.InitialUsage = &service.VPNInitialUsage{UsedTraffic: 11}
	m.TargetDispatched = true
	require.NoError(t, r.CheckpointMigration(ctx, op, m))
	require.ErrorIs(t, r.Queue(ctx, sub.ID, sub.UserID, p), service.ErrVPNBusy)
}
func TestVPNAllocationUsesHostMultiplierAndRejectsExhaustedNode(t *testing.T) {
	r, db := vpnTestRepository(t)
	ctx := context.Background()
	node := vpnAllocationServer(t, r, 200, 40, map[string]int64{})
	_, err := db.Exec(`UPDATE vpn_servers SET traffic_multiplier=2 WHERE id=$1`, node.ID)
	require.NoError(t, err)
	vpnAllocationSnapshot(t, db, node.ID, 40, map[string]int64{})
	id, err := vpnAllocationSelect(t, db, vpnNodeRequest{RequiredQuota: 60})
	require.NoError(t, err)
	require.Equal(t, node.ID, id)
	_, err = vpnAllocationSelect(t, db, vpnNodeRequest{RequiredQuota: 61})
	require.ErrorIs(t, err, service.ErrVPNNoServer)
	s, err := r.GetServer(ctx, node.ID)
	require.NoError(t, err)
	require.Equal(t, 0.4, *s.AllocationRatio)
	require.Equal(t, int64(60), *s.AllocationAvailableBytes)
	now := time.Now().UTC()
	local := now.In(time.FixedZone("Shanghai", 8*3600))
	start := time.Date(local.Year(), local.Month(), 22, 0, 0, 0, 0, local.Location())
	if local.Before(start) {
		start = start.AddDate(0, -1, 0)
	}
	end := start.AddDate(0, 1, 0)
	used := int64(100)
	raw, err := json.Marshal(service.VPNNodeTraffic{UsedBytes: &used, PeriodStart: &start, PeriodEnd: &end, SampledAt: &now, AccountingStatus: "ok"})
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE vpn_servers SET traffic_snapshot=$2 WHERE id=$1`, node.ID, string(raw))
	require.NoError(t, err)
	_, err = vpnAllocationSelect(t, db, vpnNodeRequest{RequiredQuota: 1})
	require.ErrorIs(t, err, service.ErrVPNNoServer)

}

func TestVPNLateSyncErrorCannotOverwriteRotatedBinding(t *testing.T) {
	r, db, sub, source := vpnManualAppliedSource(t)
	ctx := context.Background()
	fresh, err := r.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	require.NoError(t, r.MarkBindingSyncError(ctx, sub.ID, source.ID, sub.OwnerRef, fresh.Snapshot.LastOperationID, "current"))
	after, err := r.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, "current", after.LastError)
	_, err = db.Exec(`UPDATE vpn_subscriptions SET owner_ref='new-binding',last_error='' WHERE id=$1`, sub.ID)
	require.NoError(t, err)
	require.NoError(t, r.MarkBindingSyncError(ctx, sub.ID, source.ID, sub.OwnerRef, fresh.Snapshot.LastOperationID, "old-failure"))
	after, err = r.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	require.Empty(t, after.LastError)
}
