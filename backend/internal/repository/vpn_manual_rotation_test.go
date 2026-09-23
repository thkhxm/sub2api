//go:build vpnintegration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func vpnManualAppliedSource(t *testing.T) (*vpnRepository, *sql.DB, *service.VPNSubscription, *service.VPNServer) {
	t.Helper()
	r, db := vpnTestRepository(t)
	source := vpnTestServer(t, r, 0)
	sub := vpnReserve(t, r, vpnTestUser(t, db, 1))
	op, err := r.Claim(context.Background())
	require.NoError(t, err)
	vpnApplyOperation(t, r, sub, op, sub.QuotaBytes, "active", "allowed")
	return r, db, sub, source
}

func TestVPNManualSameSourceFullCapacityStillRotates(t *testing.T) {
	r, db, sub, source := vpnManualAppliedSource(t)
	ctx := context.Background()
	_, err := db.Exec(`UPDATE vpn_servers SET traffic_quota_bytes=$2 WHERE id=$1`, source.ID, sub.QuotaBytes)
	require.NoError(t, err)
	require.NoError(t, r.UpdateServerHealth(ctx, source.ID, vpnTestQuotaMeta(1, sub.OwnerRef), ""))
	require.NoError(t, r.Queue(ctx, sub.ID, sub.UserID, service.VPNOperationPayload{Action: "revoke", TargetServerID: source.ID}))
	op, err := r.Claim(ctx)
	require.NoError(t, err)
	op, err = r.PrepareOperation(ctx, op)
	require.NoError(t, err)
	var payload service.VPNOperationPayload
	require.NoError(t, json.Unmarshal(op.Payload, &payload))
	require.Equal(t, "revoke", payload.Action)
	require.Equal(t, source.ID, payload.TargetServerID)
	require.Nil(t, payload.Migration)
	fresh, err := r.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, sub.OwnerRef, fresh.OwnerRef)
	require.Equal(t, source.ID, fresh.ServerID)
	require.Equal(t, int64(11), fresh.Snapshot.UsedTraffic)
}

func TestVPNManualSelectedTargetPersistsAndDoesNotFallback(t *testing.T) {
	r, db, sub, source := vpnManualAppliedSource(t)
	ctx := context.Background()
	target := vpnTestServer(t, r, 5)
	vpnTestServer(t, r, 0)
	require.NoError(t, r.Queue(ctx, sub.ID, sub.UserID, service.VPNOperationPayload{Action: "revoke", TargetServerID: target.ID}))
	op, err := r.Claim(ctx)
	require.NoError(t, err)
	m := vpnMigrationPayload(t, op)
	require.True(t, m.ManualTarget)
	require.Equal(t, target.ID, m.TargetServerID)
	require.Equal(t, source.ID, m.SourceServerID)
	require.Equal(t, "revoke_source", m.Stage)
	freshSource, err := r.GetServer(ctx, source.ID)
	require.NoError(t, err)
	require.True(t, freshSource.Enabled)
	require.NoError(t, r.Finish(ctx, op, "pending", "test restart"))
	_, err = db.Exec(`UPDATE vpn_operations SET available_at=now() WHERE id=$1`, op.ID)
	require.NoError(t, err)
	restarted := &vpnRepository{db: db}
	recovered, err := restarted.Claim(ctx)
	require.NoError(t, err)
	require.Equal(t, op.ID, recovered.ID)
	require.Equal(t, *m, *vpnMigrationPayload(t, recovered))
	vpnDisableServer(t, db, target.ID)
	_, err = restarted.RetargetMigration(ctx, recovered, m)
	require.ErrorIs(t, err, service.ErrVPNNoServer)
	var stored []byte
	require.NoError(t, db.QueryRow(`SELECT payload FROM vpn_operations WHERE id=$1`, op.ID).Scan(&stored))
	require.JSONEq(t, string(recovered.Payload), string(stored))
	fresh, err := restarted.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, source.ID, fresh.ServerID)
	require.Equal(t, sub.OwnerRef, fresh.OwnerRef)
	require.Equal(t, "allowed", fresh.AccessState)
	require.Nil(t, fresh.DeleteRequestedAt)
}

func TestVPNManualUnavailableTargetRejectsBeforeSourceChange(t *testing.T) {
	for _, condition := range []string{"disabled", "unhealthy", "insufficient", "missing"} {
		t.Run(condition, func(t *testing.T) {
			r, db, sub, source := vpnManualAppliedSource(t)
			ctx := context.Background()
			target := vpnTestServer(t, r, 0)
			var err error
			switch condition {
			case "disabled":
				vpnDisableServer(t, db, target.ID)
			case "unhealthy":
				_, err = db.Exec(`UPDATE vpn_servers SET healthy=false WHERE id=$1`, target.ID)
			case "insufficient":
				_, err = db.Exec(`UPDATE vpn_servers SET traffic_quota_bytes=$2 WHERE id=$1`, target.ID, sub.QuotaBytes-1)
			case "missing":
				target.ID += 10000
			}
			require.NoError(t, err)
			require.ErrorIs(t, r.Queue(ctx, sub.ID, sub.UserID, service.VPNOperationPayload{Action: "revoke", TargetServerID: target.ID}), service.ErrVPNNoServer)
			var count int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM vpn_operations WHERE subscription_id=$1`, sub.ID).Scan(&count))
			require.Equal(t, 1, count)
			fresh, err := r.GetSubscription(ctx, sub.ID)
			require.NoError(t, err)
			require.Equal(t, source.ID, fresh.ServerID)
			require.Equal(t, sub.OwnerRef, fresh.OwnerRef)
			require.Equal(t, "succeeded", fresh.OperationStatus)
			require.Equal(t, "applied", fresh.ApplyStatus)
			require.Equal(t, "allowed", fresh.AccessState)
			require.Nil(t, fresh.DeleteRequestedAt)
		})
	}
}

func TestVPNManualRetiredTargetReenabledKeepsSelectionAndRenewsOwner(t *testing.T) {
	r, db, sub, _ := vpnManualAppliedSource(t)
	ctx := context.Background()
	target := vpnTestServer(t, r, 0)
	vpnTestServer(t, r, 0)
	require.NoError(t, r.Queue(ctx, sub.ID, sub.UserID, service.VPNOperationPayload{Action: "revoke", TargetServerID: target.ID}))
	op, err := r.Claim(ctx)
	require.NoError(t, err)
	m := vpnMigrationPayload(t, op)
	vpnSourceRetired(t, r, op, m)
	m.TargetDispatched = true
	require.NoError(t, r.CheckpointMigration(ctx, op, m))
	vpnDisableServer(t, db, target.ID)
	m.TargetRetired = true
	require.NoError(t, r.CheckpointMigration(ctx, op, m))
	_, err = r.RetargetMigration(ctx, op, m)
	require.ErrorIs(t, err, service.ErrVPNNoServer)
	_, err = db.Exec(`UPDATE vpn_servers SET enabled=true WHERE id=$1`, target.ID)
	require.NoError(t, err)
	next, err := r.RetargetMigration(ctx, op, m)
	require.NoError(t, err)
	require.True(t, next.ManualTarget)
	require.Equal(t, target.ID, next.TargetServerID)
	require.NotEqual(t, m.TargetOwnerRef, next.TargetOwnerRef)
	require.NotEqual(t, m.TargetUsername, next.TargetUsername)
	require.False(t, next.TargetRetired)
	require.False(t, next.TargetDispatched)
	require.Equal(t, m.InitialUsage, next.InitialUsage)
}

func TestVPNUsageSortGlobalBeforePaginationWithStableTies(t *testing.T) {
	r, db := vpnTestRepository(t)
	ctx := context.Background()
	vpnTestServer(t, r, 0)
	ids := make([]int64, 0, 5)
	for _, used := range []int64{100, 5, 100, 50, 0} {
		sub := vpnReserve(t, r, vpnTestUser(t, db, 1))
		ids = append(ids, sub.ID)
		_, err := db.Exec(`UPDATE vpn_subscriptions SET snapshot=jsonb_build_object('used_traffic',$2::bigint) WHERE id=$1`, sub.ID, used)
		require.NoError(t, err)
	}
	for order, want := range map[string][]int64{
		"asc":  {ids[4], ids[1], ids[3], ids[2], ids[0]},
		"desc": {ids[2], ids[0], ids[3], ids[1], ids[4]},
	} {
		t.Run(order, func(t *testing.T) {
			var got []int64
			for page := 1; page <= 3; page++ {
				rows, total, err := r.List(ctx, service.VPNFilter{Page: page, PageSize: 2, SortBy: "used_bytes", SortOrder: order})
				require.NoError(t, err)
				require.Equal(t, 5, total)
				for _, row := range rows {
					got = append(got, row.ID)
				}
			}
			require.Equal(t, want, got)
		})
	}
	for _, filter := range []service.VPNFilter{{SortBy: "used_bytes;SELECT 1"}, {SortBy: "used_bytes", SortOrder: "sideways"}} {
		_, _, err := r.List(ctx, filter)
		require.ErrorIs(t, err, service.ErrVPNInvalid)
	}
}
