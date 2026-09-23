//go:build vpnintegration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const vpnAllocationGiB int64 = 1024 * 1024 * 1024

func vpnAllocationServer(t *testing.T, r *vpnRepository, quota, allocated int64, managed map[string]int64) *service.VPNServer {
	t.Helper()
	v, err := r.SaveServer(context.Background(), &service.VPNServer{Name: "额度测试", BaseURL: "https://" + uuid.NewString() + ".invalid", AdminUsername: "integration", CredentialsEncrypted: "fixture", Enabled: true, TrafficQuotaBytes: quota})
	require.NoError(t, err)
	vpnAllocationSnapshot(t, r.db, v.ID, allocated, managed)
	return v
}

func vpnAllocationSnapshot(t *testing.T, db *sql.DB, id, allocated int64, managed map[string]int64) {
	t.Helper()
	if managed == nil {
		managed = map[string]int64{}
	}
	b, err := json.Marshal(service.VPNNodeAllocation{AllocatedQuotaBytes: allocated, ManagedQuotaBytes: managed})
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE vpn_servers SET allocation_snapshot=$2,healthy=true,last_checked_at=now() WHERE id=$1`, id, string(b))
	require.NoError(t, err)
}

func vpnAllocationSub(t *testing.T, db *sql.DB, serverID, quota int64, owner string) int64 {
	t.Helper()
	user := vpnTestUser(t, db, 1)
	var id int64
	err := db.QueryRow(`INSERT INTO vpn_subscriptions(user_id,server_id,owner_ref,remote_username,group_id,quota_bytes)
VALUES($1,$2,$3,$3,(SELECT id FROM vpn_user_groups WHERE is_default),$4) RETURNING id`, user, serverID, owner, quota).Scan(&id)
	require.NoError(t, err)
	return id
}

func vpnAllocationSelect(t *testing.T, db *sql.DB, req vpnNodeRequest) (int64, error) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.Exec(`SELECT pg_advisory_xact_lock(821407238)`)
	require.NoError(t, err)
	id, err := selectVPNServerByQuota(context.Background(), tx, req)
	if err == nil {
		require.NoError(t, tx.Commit())
	}
	return id, err
}

func vpnAllocationTotals(t *testing.T, r *vpnRepository) map[int64]int64 {
	t.Helper()
	servers, err := r.ListServers(context.Background())
	require.NoError(t, err)
	require.NoError(t, hydrateVPNAllocations(context.Background(), r.db, servers, ""))
	totals := map[int64]int64{}
	for _, server := range servers {
		if server.AllocationQuotaBytes != nil {
			totals[server.ID] = *server.AllocationQuotaBytes
		}
	}
	return totals
}

func TestVPNAllocationRatioAndFullQuotaAdmission(t *testing.T) {
	r, db := vpnTestRepository(t)
	a := vpnAllocationServer(t, r, 1024*vpnAllocationGiB, 900*vpnAllocationGiB, nil)
	b := vpnAllocationServer(t, r, 2048*vpnAllocationGiB, 1000*vpnAllocationGiB, nil)
	_, err := db.Exec(`UPDATE vpn_servers SET personal_user_count=100 WHERE id=$1`, b.ID)
	require.NoError(t, err)
	id, err := vpnAllocationSelect(t, db, vpnNodeRequest{RequiredQuota: 80 * vpnAllocationGiB})
	require.NoError(t, err)
	require.Equal(t, b.ID, id)
	vpnAllocationSnapshot(t, db, a.ID, 954*vpnAllocationGiB, nil)
	_, err = vpnAllocationSelect(t, db, vpnNodeRequest{RequiredQuota: 80 * vpnAllocationGiB, RequestedID: a.ID})
	require.ErrorIs(t, err, service.ErrVPNNoServer)
	_, err = vpnAllocationSelect(t, db, vpnNodeRequest{RequiredQuota: 1, SourceID: b.ID, ExcludedID: a.ID})
	require.ErrorIs(t, err, service.ErrVPNNoServer)
	vpnAllocationSnapshot(t, db, a.ID, 1100*vpnAllocationGiB, nil)
	_, err = vpnAllocationSelect(t, db, vpnNodeRequest{RequiredQuota: 1, RequestedID: a.ID})
	require.ErrorIs(t, err, service.ErrVPNNoServer)
}

func TestVPNAllocationRemoteAndLocalQuotaReconcile(t *testing.T) {
	r, db := vpnTestRepository(t)
	a := vpnAllocationServer(t, r, 300, 120, map[string]int64{"managed": 80})
	subID := vpnAllocationSub(t, db, a.ID, 80, "managed")
	require.Equal(t, int64(120), vpnAllocationTotals(t, r)[a.ID])
	for _, test := range []struct{ local, expected int64 }{{100, 140}, {20, 120}} {
		_, err := db.Exec(`UPDATE vpn_subscriptions SET quota_bytes=$2,snapshot='{"used_traffic":999999}' WHERE id=$1`, subID, test.local)
		require.NoError(t, err)
		require.Equal(t, test.expected, vpnAllocationTotals(t, r)[a.ID])
	}
	// 本地删除确认不能代替节点确认；非平台有限账号的 40 字节始终保留。
	_, err := db.Exec(`UPDATE vpn_subscriptions SET deleted_at=now() WHERE id=$1`, subID)
	require.NoError(t, err)
	require.Equal(t, int64(120), vpnAllocationTotals(t, r)[a.ID])
	vpnAllocationSnapshot(t, db, a.ID, 40, nil)
	require.Equal(t, int64(40), vpnAllocationTotals(t, r)[a.ID])
	_, err = db.Exec(`UPDATE vpn_servers SET personal_user_count=1000 WHERE id=$1`, a.ID)
	require.NoError(t, err)
	require.Equal(t, int64(40), vpnAllocationTotals(t, r)[a.ID])
	// 改组降额不能释放先前已排队、仍可能执行的高额度请求。
	queued := vpnAllocationSub(t, db, a.ID, 20, "queued")
	opID := uuid.NewString()
	for _, action := range []string{"create", "update"} {
		_, err = db.Exec(`INSERT INTO vpn_operations(id,subscription_id,action,payload,status)
VALUES($1,$2,$3,'{"owner_ref":"queued","data_limit":80}','failed')
ON CONFLICT(id) DO UPDATE SET action=excluded.action`, opID, queued, action)
		require.NoError(t, err)
		require.Equal(t, int64(120), vpnAllocationTotals(t, r)[a.ID])
	}
	_, err = db.Exec(`UPDATE vpn_operations SET status='succeeded' WHERE id=$1`, opID)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE vpn_subscriptions SET snapshot='{"data_limit":80}' WHERE id=$1`, queued)
	require.NoError(t, err)
	require.Equal(t, int64(120), vpnAllocationTotals(t, r)[a.ID])
	_, err = db.Exec(`UPDATE vpn_subscriptions SET snapshot='{"data_limit":20}' WHERE id=$1`, queued)
	require.NoError(t, err)
	require.Equal(t, int64(60), vpnAllocationTotals(t, r)[a.ID])
}

func TestVPNAllocationExistingOwnerIsNotReservedTwice(t *testing.T) {
	r, db := vpnTestRepository(t)
	a := vpnAllocationServer(t, r, 100, 80, map[string]int64{"existing": 80})
	vpnAllocationSub(t, db, a.ID, 90, "existing")
	id, err := vpnAllocationSelect(t, db, vpnNodeRequest{RequiredQuota: 100, OwnerRef: "existing"})
	require.NoError(t, err)
	require.Equal(t, a.ID, id)
	_, err = vpnAllocationSelect(t, db, vpnNodeRequest{RequiredQuota: 101, OwnerRef: "existing"})
	require.ErrorIs(t, err, service.ErrVPNNoServer)
	_, err = vpnAllocationSelect(t, db, vpnNodeRequest{RequiredQuota: 11, OwnerRef: "new-owner"})
	require.ErrorIs(t, err, service.ErrVPNNoServer)
}

func TestVPNAllocationConcurrentReservationsDoNotOversell(t *testing.T) {
	r, db := vpnTestRepository(t)
	vpnAllocationServer(t, r, 200, 0, nil)
	users := make([]int64, 20)
	for i := range users {
		users[i] = vpnTestUser(t, db, 1)
	}
	results := make(chan error, len(users))
	var wg sync.WaitGroup
	for _, user := range users {
		wg.Add(1)
		go func(user int64) {
			defer wg.Done()
			ctx := context.Background()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				results <- err
				return
			}
			defer tx.Rollback()
			_, err = tx.Exec(`SELECT pg_advisory_xact_lock(821407238)`)
			var server int64
			owner := uuid.NewString()
			if err == nil {
				server, err = selectVPNServerByQuota(ctx, tx, vpnNodeRequest{RequiredQuota: 80, OwnerRef: owner})
			}
			if err == nil {
				_, err = tx.Exec(`INSERT INTO vpn_subscriptions(user_id,server_id,owner_ref,remote_username,group_id,quota_bytes) VALUES($1,$2,$3,$4,(SELECT id FROM vpn_user_groups WHERE is_default),80)`, user, server, owner, owner[:28])
			}
			if err == nil {
				err = tx.Commit()
			}
			results <- err
		}(user)
	}
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else {
			require.True(t, errors.Is(err, service.ErrVPNNoServer), "%v", err)
		}
	}
	require.Equal(t, 2, succeeded)
	var total int64
	require.NoError(t, db.QueryRow(`SELECT sum(quota_bytes) FROM vpn_subscriptions`).Scan(&total))
	require.Equal(t, int64(160), total)
}

func TestVPNAllocationMigrationReservationLifecycle(t *testing.T) {
	r, db := vpnTestRepository(t)
	source := vpnAllocationServer(t, r, 500, 80, map[string]int64{"source": 80})
	target := vpnAllocationServer(t, r, 500, 0, nil)
	subID := vpnAllocationSub(t, db, source.ID, 100, "source")
	m := &service.VPNMigration{SourceServerID: source.ID, SourceOwnerRef: "source", TargetServerID: target.ID, TargetOwnerRef: "target", QuotaBytes: 80, Stage: "revoke_source"}
	opID := uuid.NewString()
	store := func() {
		b, err := json.Marshal(service.VPNOperationPayload{Action: "migrate", Migration: m})
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO vpn_operations(id,subscription_id,action,payload,status) VALUES($1,$2,'migrate',$3,'failed') ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, opID, subID, string(b))
		require.NoError(t, err)
	}
	store()
	totals := vpnAllocationTotals(t, r)
	require.Equal(t, int64(100), totals[source.ID])
	require.Equal(t, int64(100), totals[target.ID])
	m.Stage = "create_target"
	now := time.Now()
	m.InitialUsage = &service.VPNInitialUsage{UsedTraffic: 70, PeriodStart: now.Add(-time.Hour), PeriodEnd: now.Add(time.Hour)}
	store()
	vpnAllocationSnapshot(t, db, target.ID, 80, map[string]int64{"target": 80})
	totals = vpnAllocationTotals(t, r)
	require.Equal(t, int64(80), totals[source.ID])
	require.Equal(t, int64(100), totals[target.ID])
	servers, err := r.ListServers(context.Background())
	require.NoError(t, err)
	require.NoError(t, hydrateVPNAllocations(context.Background(), db, servers, opID))
	for _, server := range servers {
		if server.ID == target.ID {
			require.Equal(t, int64(80), *server.AllocationQuotaBytes)
		}
	}
	m.TargetRetired = true
	store()
	require.Equal(t, int64(80), vpnAllocationTotals(t, r)[target.ID])
	m.TargetRetired, m.Stage = false, "completed"
	store()
	_, err = db.Exec(`UPDATE vpn_subscriptions SET server_id=$2,owner_ref='target' WHERE id=$1`, subID, target.ID)
	require.NoError(t, err)
	require.Equal(t, int64(100), vpnAllocationTotals(t, r)[target.ID])
}

func TestVPNAllocationPausedAndDeletingKeepReservation(t *testing.T) {
	r, db := vpnTestRepository(t)
	a := vpnAllocationServer(t, r, 100, 0, nil)
	subID := vpnAllocationSub(t, db, a.ID, 80, "paused")
	_, err := db.Exec(`UPDATE vpn_subscriptions SET enabled=false,status='disabled',delete_requested_at=now() WHERE id=$1`, subID)
	require.NoError(t, err)
	require.Equal(t, int64(80), vpnAllocationTotals(t, r)[a.ID])
	_, err = vpnAllocationSelect(t, db, vpnNodeRequest{RequiredQuota: 30})
	require.ErrorIs(t, err, service.ErrVPNNoServer)
}

func TestVPNAllocationUnknownAndStaleNodeAreUnavailable(t *testing.T) {
	r, db := vpnTestRepository(t)
	a := vpnAllocationServer(t, r, 100, 0, nil)
	for _, snapshot := range []string{"null", `{}`, `{"allocated_quota_bytes":5,"managed_quota_bytes":{"x":10}}`, `{"allocated_quota_bytes":-1,"managed_quota_bytes":{}}`} {
		_, err := db.Exec(`UPDATE vpn_servers SET allocation_snapshot=$2 WHERE id=$1`, a.ID, snapshot)
		require.NoError(t, err)
		_, err = vpnAllocationSelect(t, db, vpnNodeRequest{RequiredQuota: 1})
		require.ErrorIs(t, err, service.ErrVPNNoServer)
	}
	vpnAllocationSnapshot(t, db, a.ID, 0, nil)
	_, err := db.Exec(`UPDATE vpn_servers SET last_checked_at=now()-interval '3 minutes' WHERE id=$1`, a.ID)
	require.NoError(t, err)
	_, err = vpnAllocationSelect(t, db, vpnNodeRequest{RequiredQuota: 1})
	require.ErrorIs(t, err, service.ErrVPNNoServer)
	vpnAllocationSnapshot(t, db, a.ID, 0, nil)
	_, err = db.Exec(`UPDATE vpn_servers SET traffic_quota_bytes=0 WHERE id=$1`, a.ID)
	require.NoError(t, err)
	_, err = vpnAllocationSelect(t, db, vpnNodeRequest{RequiredQuota: 1})
	require.ErrorIs(t, err, service.ErrVPNNoServer)
}

func TestVPNAllocationRatioComparisonAndTieBreak(t *testing.T) {
	aUsed, bUsed := service.VPNMaxQuota-2, service.VPNMaxQuota-1
	a := service.VPNServer{ID: 1, TrafficQuotaBytes: service.VPNMaxQuota, AllocationQuotaBytes: &aUsed}
	b := service.VPNServer{ID: 2, TrafficQuotaBytes: service.VPNMaxQuota, AllocationQuotaBytes: &bUsed}
	require.True(t, lessVPNAllocation(&a, &b))
	bUsed = aUsed
	now := time.Now()
	a.LastAssignedAt = &now
	require.True(t, lessVPNAllocation(&b, &a))
	b.LastAssignedAt = &now
	require.True(t, lessVPNAllocation(&a, &b))
}
