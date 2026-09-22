package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestVPNTrafficPeriodAndOffset(t *testing.T) {
	before := time.Date(2026, 9, 21, 15, 59, 59, 0, time.UTC)
	start, end := vpnPeriodBounds(before)
	require.Equal(t, "2026-08-21T16:00:00Z", start.Format(time.RFC3339))
	require.Equal(t, "2026-09-21T16:00:00Z", end.Format(time.RFC3339))
	start, end = vpnPeriodBounds(end)
	require.Equal(t, "2026-09-21T16:00:00Z", start.Format(time.RFC3339))
	now := time.Now()
	start, end = vpnPeriodBounds(now)
	used := int64(30)
	v := VPNServer{TrafficQuotaBytes: 100, TrafficUsedOffsetBytes: 20, TrafficOffsetPeriodStart: &start, Healthy: true, LastCheckedAt: &now, TrafficSnapshot: &VPNNodeTraffic{UsedBytes: &used, PeriodStart: &start, PeriodEnd: &end, SampledAt: &now, AvailableFrom: &start, AccountingStatus: "ok"}}
	hydrateVPNServer(&v, now)
	require.Equal(t, int64(50), *v.TrafficUsedBytes)
	require.Equal(t, int64(50), *v.TrafficRemainingBytes)
	old := start.AddDate(0, -1, 0)
	v.TrafficOffsetPeriodStart = &old
	hydrateVPNServer(&v, now)
	require.Equal(t, int64(70), *v.TrafficRemainingBytes)
	require.Zero(t, v.TrafficUsedOffsetBytes)
	used = 150
	hydrateVPNServer(&v, now)
	require.Zero(t, *v.TrafficRemainingBytes)
	hydrateVPNServer(&v, now.Add(3*time.Minute))
	require.Nil(t, v.TrafficRemainingBytes)
	require.Equal(t, "stale", v.TrafficAccountingStatus)
	v.TrafficSnapshot = nil
	hydrateVPNServer(&v, now)
	require.Nil(t, v.TrafficRemainingBytes)
}

type trafficTestRepo struct {
	VPNRepository
	servers []VPNServer
	refs    map[int64][]string
}

func (r *trafficTestRepo) ListServers(context.Context) ([]VPNServer, error) { return r.servers, nil }
func (r *trafficTestRepo) UserOwnerRefs(context.Context, int64) (map[int64][]string, error) {
	return r.refs, nil
}

type trafficTestRemote struct {
	VPNRemote
	calls   chan []string
	failAll bool
}

func (r *trafficTestRemote) Traffic(_ context.Context, s *VPNServer, _ VPNCredentials, f VPNTrafficFilter, refs []string) (*VPNRemoteTraffic, error) {
	r.calls <- refs
	if r.failAll || s.ID == 2 {
		return nil, fmt.Errorf("offline")
	}
	now := time.Now()
	value := int64(25)
	return &VPNRemoteTraffic{Timezone: "Asia/Shanghai", StartDate: f.StartDate, EndDate: f.EndDate, Days: []VPNTrafficDay{{Date: f.StartDate}, {Date: f.EndDate, UsedBytes: &value}}, TotalBytes: 25, SampledAt: &now, AccountingStatus: "ok"}, nil
}
func TestVPNTrafficDailyPartialHistoryAndUserScope(t *testing.T) {
	cipher := &liveAttestationAES{key: [32]byte{1}}
	encrypted, err := cipher.Encrypt(`vpn-credentials:{"password":"test"}`)
	require.NoError(t, err)
	repo := &trafficTestRepo{servers: []VPNServer{{ID: 1, CredentialsEncrypted: encrypted}, {ID: 2, CredentialsEncrypted: encrypted}}, refs: map[int64][]string{1: {"old-deleted-owner", "new-owner"}}}
	remote := &trafficTestRemote{calls: make(chan []string, 20)}
	svc := NewVPNService(repo, cipher, remote)
	f := VPNTrafficFilter{StartDate: "2026-09-21", EndDate: "2026-09-22"}
	v, err := svc.DailyTraffic(context.Background(), f)
	require.NoError(t, err)
	require.True(t, v.Partial)
	require.Equal(t, 1, v.UnavailableServers)
	require.Nil(t, v.Days[0].UsedBytes)
	require.Equal(t, int64(25), v.TotalBytes)
	<-remote.calls
	<-remote.calls
	f.UserID = 7
	v, err = svc.DailyTraffic(context.Background(), f)
	require.NoError(t, err)
	require.Zero(t, v.UnavailableServers)
	require.ElementsMatch(t, repo.refs[1], <-remote.calls)
	f.ServerID = 3
	_, err = svc.DailyTraffic(context.Background(), f)
	require.ErrorIs(t, err, ErrVPNNotFound)
	f.ServerID = 0
	remote.failAll = true
	_, err = svc.DailyTraffic(context.Background(), f)
	require.ErrorIs(t, err, ErrVPNNoServer)
	for _, bad := range []VPNTrafficFilter{{StartDate: "2026-02-30", EndDate: "2026-03-01"}, {StartDate: "2026-09-22", EndDate: "2026-09-21"}, {StartDate: "2026-01-01", EndDate: "2026-04-04"}, {StartDate: "2026-09-21", EndDate: "2026-09-22", UserID: -1}} {
		_, err = svc.DailyTraffic(context.Background(), bad)
		require.ErrorIs(t, err, ErrVPNInvalid)
	}
}
func TestVPNDeleteRequiresActualBlockedSnapshot(t *testing.T) {
	svc, repo, remote := vpnServiceFixture(t)
	op := &VPNOperation{ID: remote.snapshot.LastOperationID, SubscriptionID: 1, Payload: json.RawMessage(`{"action":"delete"}`)}
	svc.process(context.Background(), op)
	require.Equal(t, "pending", repo.finishState)
	remote.snapshot.Status = "deleted"
	remote.snapshot.AccessState = "blocked"
	remote.snapshot.SubscriptionURL = ""
	svc.process(context.Background(), op)
	require.Equal(t, "succeeded", repo.finishState)
	now := time.Now()
	sub := &VPNSubscription{Status: "active", ApplyStatus: "applied", AccessState: "allowed", DeleteRequestedAt: &now, URLEncrypted: "invalid-old-cipher"}
	require.NoError(t, svc.hydrate(sub))
	require.Equal(t, "deleting", sub.Status)
	require.Nil(t, sub.SubscriptionURLs)
	sub = &VPNSubscription{Status: "active", ApplyStatus: "applied", AccessState: "allowed", OperationStatus: "succeeded", QuotaSyncNeeded: true, URLEncrypted: "old-link"}
	require.NoError(t, svc.hydrate(sub))
	require.Equal(t, "pending", sub.OperationStatus)
	require.Equal(t, "pending", sub.ApplyStatus)
	require.Nil(t, sub.SubscriptionURLs)
}
