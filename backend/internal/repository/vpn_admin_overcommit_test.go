//go:build vpnintegration

package repository

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestVPNAdminOvercommitRequiresConfirmationAndSurvivesWorker(t *testing.T) {
	r, db := vpnTestRepository(t)
	ctx := context.Background()
	q := service.VPNDefaultQuota
	a := vpnAllocationServer(t, r, q*2, q, nil)
	b := vpnAllocationServer(t, r, q*4, q*3, nil)
	_, err := db.Exec(`UPDATE vpn_servers SET traffic_multiplier=2`)
	require.NoError(t, err)
	user := vpnTestUser(t, db, 0)
	_, err = r.ReserveAdmin(ctx, user, 1, uuid.NewString(), service.VPNCreateOptions{})
	require.ErrorIs(t, err, service.ErrVPNOvercommitRequired)
	var appErr *infraerrors.ApplicationError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, strconv.FormatInt(a.ID, 10), appErr.Metadata["server_id"])
	require.Equal(t, strconv.FormatInt(q, 10), appErr.Metadata["required_quota_bytes"])
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM vpn_subscriptions`).Scan(&count))
	require.Zero(t, count, "未确认时不能预占或创建订阅")
	self := vpnTestUser(t, db, 1)
	_, err = r.Reserve(ctx, self, false, self, uuid.NewString())
	require.ErrorIs(t, err, service.ErrVPNNoServer)
	allowed, reason, err := r.Eligibility(ctx, self)
	require.NoError(t, err)
	require.False(t, allowed)
	require.Equal(t, "capacity_insufficient", reason)
	opts := service.VPNCreateOptions{AllowOvercommit: true, TargetServerID: a.ID, ExpectedQuotaBytes: q}
	sub, err := r.ReserveAdmin(ctx, user, 1, uuid.NewString(), opts)
	require.NoError(t, err)
	require.Equal(t, a.ID, sub.ServerID)
	op, err := r.Claim(ctx)
	require.NoError(t, err)
	require.NotNil(t, op)
	prepared, err := r.PrepareOperation(ctx, op)
	require.NoError(t, err)
	var payload service.VPNOperationPayload
	require.NoError(t, json.Unmarshal(prepared.Payload, &payload))
	require.True(t, payload.AllowOvercommit)
	require.Equal(t, a.ID, payload.TargetServerID)
	require.NoError(t, r.MarkOperationDispatched(ctx, prepared))
	_, err = db.Exec(`UPDATE vpn_servers SET enabled=false WHERE id=$1`, a.ID)
	require.NoError(t, err)
	_, err = r.PrepareOperation(ctx, prepared)
	require.ErrorIs(t, err, service.ErrVPNNoServer)
	current, err := r.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, a.ID, current.ServerID, "确认的节点失效后不能借用超配授权改派")
	require.NotEqual(t, b.ID, current.ServerID)
}

func TestVPNAdminOvercommitDoesNotBypassNodeSafety(t *testing.T) {
	for _, state := range []string{"disabled", "unhealthy", "stale", "unknown", "exhausted", "quota_changed"} {
		t.Run(state, func(t *testing.T) {
			r, db := vpnTestRepository(t)
			q := service.VPNDefaultQuota
			node := vpnAllocationServer(t, r, q, q, nil)
			switch state {
			case "disabled":
				_, err := db.Exec(`UPDATE vpn_servers SET enabled=false`)
				require.NoError(t, err)
			case "unhealthy":
				_, err := db.Exec(`UPDATE vpn_servers SET healthy=false`)
				require.NoError(t, err)
			case "stale":
				_, err := db.Exec(`UPDATE vpn_servers SET last_checked_at=now()-interval '3 minutes'`)
				require.NoError(t, err)
			case "unknown":
				_, err := db.Exec(`UPDATE vpn_servers SET allocation_snapshot=NULL`)
				require.NoError(t, err)
			case "exhausted":
				now := time.Now().UTC()
				local := now.In(time.FixedZone("Asia/Shanghai", 8*3600))
				start := time.Date(local.Year(), local.Month(), 22, 0, 0, 0, 0, local.Location())
				if local.Before(start) {
					start = start.AddDate(0, -1, 0)
				}
				end := start.AddDate(0, 1, 0)
				data, err := json.Marshal(service.VPNNodeTraffic{UsedBytes: &q, PeriodStart: &start, PeriodEnd: &end, SampledAt: &now, AccountingStatus: "ok"})
				require.NoError(t, err)
				_, err = db.Exec(`UPDATE vpn_servers SET traffic_snapshot=$1`, string(data))
				require.NoError(t, err)
			}
			user := vpnTestUser(t, db, 0)
			opts := service.VPNCreateOptions{AllowOvercommit: true, TargetServerID: node.ID, ExpectedQuotaBytes: q}
			if state == "quota_changed" {
				opts.ExpectedQuotaBytes = q * 2
			}
			_, err := r.ReserveAdmin(context.Background(), user, 1, uuid.NewString(), opts)
			require.Error(t, err)
			var count int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM vpn_subscriptions`).Scan(&count))
			require.Zero(t, count)
		})
	}
}

func TestVPN40GiBMigrationPreservesUsageAndQueuesQuotaOnly(t *testing.T) {
	r, db := vpnTestRepository(t)
	ctx := context.Background()
	node := vpnTestServer(t, r, 0)
	var ids []int64
	for _, amount := range []int64{30, 80} {
		u := vpnTestUser(t, db, 1)
		s := vpnReserve(t, r, u)
		op, err := r.Claim(ctx)
		require.NoError(t, err)
		vpnApplyOperation(t, r, s, op, s.QuotaBytes, "active", "allowed")
		_, err = db.Exec(`UPDATE vpn_subscriptions SET quota_bytes=$2,url_encrypted='preserved',snapshot=jsonb_set(jsonb_set(snapshot,'{used_traffic}','12345'),'{data_limit}',to_jsonb($2::bigint)) WHERE id=$1`, s.ID, amount*vpnAllocationGiB)
		require.NoError(t, err)
		ids = append(ids, s.ID)
	}
	_, err := db.Exec(`UPDATE vpn_user_groups SET quota_bytes=85899345920 WHERE is_default`)
	require.NoError(t, err)
	sql, err := os.ReadFile("../../migrations/247_vpn_default_quota_40g.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(sql))
	require.NoError(t, err)
	for _, id := range ids {
		s, err := r.GetSubscription(ctx, id)
		require.NoError(t, err)
		require.Equal(t, service.VPNDefaultQuota, s.QuotaBytes)
		require.Equal(t, int64(12345), s.Snapshot.UsedTraffic)
		require.Equal(t, "preserved", s.URLEncrypted)
		require.Equal(t, node.ID, s.ServerID)
		require.True(t, s.QuotaSyncNeeded)
		require.NoError(t, r.QueueQuotaSync(ctx, id))
		op, err := r.Claim(ctx)
		require.NoError(t, err)
		require.NotNil(t, op)
		var payload service.VPNOperationPayload
		require.NoError(t, json.Unmarshal(op.Payload, &payload))
		require.Equal(t, "update", payload.Action)
		require.Equal(t, service.VPNDefaultQuota, *payload.DataLimit)
		require.Nil(t, payload.InitialUsage)
		require.Nil(t, payload.Enabled)
		require.NoError(t, r.Finish(ctx, op, "succeeded", ""))
	}
	_, err = db.Exec(string(sql))
	require.NoError(t, err)
	var quota int64
	require.NoError(t, db.QueryRow(`SELECT quota_bytes FROM vpn_user_groups WHERE is_default`).Scan(&quota))
	require.Equal(t, service.VPNDefaultQuota, quota)
}
