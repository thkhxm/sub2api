//go:build vpnintegration

package repository

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func vpnApplyOperation(t *testing.T, r *vpnRepository, sub *service.VPNSubscription, op *service.VPNOperation, quota int64, status, access string) {
	t.Helper()
	now := time.Now()
	snap := service.VPNSnapshot{Username: sub.RemoteUsername, OwnerRef: sub.OwnerRef, Status: status, ApplyStatus: "applied", AccessState: access, DataLimit: quota, UsedTraffic: 11, UploadBytes: 4, DownloadBytes: 7, SampledAt: &now, LastOperationID: op.ID, AccountingStatus: "ok"}
	require.NoError(t, r.SaveSnapshot(context.Background(), sub.ID, snap, ""))
	require.NoError(t, r.Finish(context.Background(), op, "succeeded", ""))
}
func TestVPNDeleteRetainsHistoryUntilConfirmedAndAllowsNewBinding(t *testing.T) {
	r, db := vpnTestRepository(t)
	ctx := context.Background()
	vpnTestServer(t, r, 0)
	user := vpnTestUser(t, db, 1)
	sub := vpnReserve(t, r, user)
	old, err := r.Claim(ctx)
	require.NoError(t, err)
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- r.Queue(ctx, sub.ID, 1, service.VPNOperationPayload{Action: "delete"})
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM vpn_operations WHERE subscription_id=$1 AND action='delete'`, sub.ID).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, r.Finish(ctx, old, "succeeded", ""))
	_, err = r.Reserve(ctx, user, false, user, uuid.NewString())
	require.ErrorIs(t, err, service.ErrVPNBusy)
	op, err := r.Claim(ctx)
	require.NoError(t, err)
	require.NotEqual(t, old.ID, op.ID)
	require.NoError(t, r.Finish(ctx, op, "failed", "offline"))
	fresh, err := r.GetUserSubscription(ctx, user)
	require.NoError(t, err)
	require.Nil(t, fresh.DeletedAt)
	require.NotNil(t, fresh.DeleteRequestedAt)
	require.NoError(t, r.Retry(ctx, sub.ID))
	retry, err := r.Claim(ctx)
	require.NoError(t, err)
	require.Equal(t, op.ID, retry.ID)
	vpnApplyOperation(t, r, sub, retry, service.VPNDefaultQuota, "deleted", "blocked")
	fresh, err = r.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	require.NotNil(t, fresh.DeletedAt)
	require.Equal(t, int64(11), fresh.Snapshot.UsedTraffic)
	_, err = r.GetUserSubscription(ctx, user)
	require.ErrorIs(t, err, service.ErrVPNNotFound)
	_, total, err := r.List(ctx, service.VPNFilter{Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.Zero(t, total)
	_, total, err = r.List(ctx, service.VPNFilter{Page: 1, PageSize: 20, Status: "deleted"})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	recreated := vpnReserve(t, r, user)
	require.NotEqual(t, sub.OwnerRef, recreated.OwnerRef)
	refs, err := r.UserOwnerRefs(ctx, user)
	require.NoError(t, err)
	require.Len(t, refs[sub.ServerID], 2)
	require.NoError(t, r.Queue(ctx, sub.ID, 1, service.VPNOperationPayload{Action: "delete"}))
	still, err := r.GetUserSubscription(ctx, user)
	require.NoError(t, err)
	require.Equal(t, recreated.ID, still.ID)
}
func TestVPNGroupsDefaultPreservesExistingAndEventuallyAppliesLatestQuota(t *testing.T) {
	r, db := vpnTestRepository(t)
	ctx := context.Background()
	vpnTestServer(t, r, 0)
	user := vpnTestUser(t, db, 1)
	sub := vpnReserve(t, r, user)
	require.Equal(t, int64(80*1024*1024*1024), sub.QuotaBytes)
	op, err := r.Claim(ctx)
	require.NoError(t, err)
	vpnApplyOperation(t, r, sub, op, sub.QuotaBytes, "active", "allowed")
	legacy := int64(30 * 1024 * 1024 * 1024)
	_, err = db.Exec(`UPDATE vpn_subscriptions SET quota_bytes=$2,snapshot=jsonb_set(snapshot,'{data_limit}',to_jsonb($2::bigint)) WHERE id=$1`, sub.ID, legacy)
	require.NoError(t, err)
	name := "改名不改额度"
	_, err = r.SaveVPNGroup(ctx, sub.GroupID, service.VPNGroupInput{Name: &name})
	require.NoError(t, err)
	fresh, err := r.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, legacy, fresh.QuotaBytes)
	require.False(t, fresh.QuotaSyncNeeded)
	next := int64(96 * 1024 * 1024 * 1024)
	group, err := r.SaveVPNGroup(ctx, sub.GroupID, service.VPNGroupInput{QuotaBytes: &next})
	require.NoError(t, err)
	require.Equal(t, 1, group.PendingCount)
	require.NoError(t, r.QueueQuotaSync(ctx, sub.ID))
	op, err = r.Claim(ctx)
	require.NoError(t, err)
	var p service.VPNOperationPayload
	require.NoError(t, json.Unmarshal(op.Payload, &p))
	require.Equal(t, next, *p.DataLimit)
	require.Nil(t, p.Enabled)
	customName := "高级组"
	customQuota := int64(128 * 1024 * 1024 * 1024)
	custom, err := r.SaveVPNGroup(ctx, 0, service.VPNGroupInput{Name: &customName, QuotaBytes: &customQuota})
	require.NoError(t, err)
	_, err = r.SetUserVPNGroup(ctx, user, custom.ID)
	require.NoError(t, err)
	vpnApplyOperation(t, r, sub, op, next, "active", "allowed")
	fresh, err = r.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	require.True(t, fresh.QuotaSyncNeeded)
	require.Equal(t, customQuota, fresh.QuotaBytes)
	require.Equal(t, int64(11), fresh.Snapshot.UsedTraffic)
	require.NoError(t, r.QueueQuotaSync(ctx, sub.ID))
	op, err = r.Claim(ctx)
	require.NoError(t, err)
	vpnApplyOperation(t, r, sub, op, customQuota, "active", "allowed")
	fresh, err = r.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	require.False(t, fresh.QuotaSyncNeeded)
	require.NoError(t, r.Queue(ctx, sub.ID, 1, service.VPNOperationPayload{Action: "delete"}))
	op, err = r.Claim(ctx)
	require.NoError(t, err)
	vpnApplyOperation(t, r, sub, op, customQuota, "deleted", "blocked")
	recreated := vpnReserve(t, r, user)
	require.Equal(t, custom.ID, recreated.GroupID)
	require.Equal(t, customQuota, recreated.QuotaBytes)
}
