package service

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVPNLoginIsolationAndCancelableWait(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	slow := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/token" {
			select {
			case <-entered:
			default:
				close(entered)
			}
			<-release
		}
		_, _ = w.Write([]byte(`{"access_token":"slow"}`))
	}))
	defer slow.Close()
	defer close(release)
	fast := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"fast","healthy":true}`))
	}))
	defer fast.Close()
	creds := func(s *httptest.Server) VPNCredentials {
		return VPNCredentials{Password: "fixture", CAPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}))}
	}
	client := NewMarzbanVPNClient()
	done := make(chan error, 1)
	slowCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() { _, e := client.Server(slowCtx, &VPNServer{BaseURL: slow.URL}, creds(slow)); done <- e }()
	<-entered
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	result, err := client.Server(ctx, &VPNServer{BaseURL: fast.URL}, creds(fast))
	require.NoError(t, err)
	require.True(t, result.Healthy)
	waitCtx, waitCancel := context.WithCancel(context.Background())
	waitCancel()
	_, err = client.Server(waitCtx, &VPNServer{BaseURL: slow.URL}, creds(slow))
	require.ErrorIs(t, err, context.Canceled)
	cancel()
	require.Error(t, <-done)
}
func TestVPNOfflineMigrationPreservesUsageAndRequiresConsent(t *testing.T) {
	for _, tc := range []struct {
		name            string
		allow, disabled bool
		failure         error
		wantTarget      bool
	}{
		{"explicit_offline", true, true, ErrVPNConnection, true},
		{"no_consent", false, true, ErrVPNConnection, false},
		{"enabled_source", true, false, ErrVPNConnection, false},
		{"authentication_failure", true, true, &vpnHTTPError{Status: 401}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, remote := vpnMigrationFixture(t)
			m := repo.migration(t)
			m.AllowOfflineMigration = tc.allow
			repo.persist(repo.operation, m)
			repo.servers[1].Enabled = !tc.disabled
			snap := &repo.sub.Snapshot
			now := time.Now()
			snap.OwnerRef = m.SourceOwnerRef
			snap.Username = m.SourceUsername
			snap.SampledAt = &now
			remote.submitErr[1] = tc.failure
			svc.process(context.Background(), repo.operation)
			stored := repo.migration(t)
			if !tc.wantTarget {
				require.Equal(t, "revoke_source", stored.Stage)
				require.False(t, stored.SourceRevocationPending)
				require.Zero(t, repo.completeCount)
				return
			}
			require.Equal(t, "create_target", stored.Stage)
			require.True(t, stored.SourceRevocationPending)
			require.Equal(t, int64(70), stored.InitialUsage.UsedTraffic)
			require.Equal(t, "gap_detected", stored.InitialUsage.AccountingStatus)
			svc.process(context.Background(), repo.operation)
			require.Equal(t, "succeeded", repo.finishState)
			require.Equal(t, int64(2), repo.sub.ServerID)
			require.Equal(t, int64(70), repo.sub.Snapshot.UsedTraffic)
			// 重新读取已完成操作不会额外创建目标账号。
			count := len(remote.calls)
			svc.process(context.Background(), repo.operation)
			require.Len(t, remote.calls, count)
		})
	}
}
func TestVPNDeferredRevocationOnlyCompletesAfterBlockedSnapshot(t *testing.T) {
	svc, repo, remote := vpnMigrationFixture(t)
	m := repo.migration(t)
	item := &VPNCredentialRevocation{ID: VPNSourceRevocationID(repo.operation.ID), ServerID: 1, OwnerRef: m.SourceOwnerRef, Username: m.SourceUsername}
	remote.submitErr[1] = ErrVPNConnection
	require.ErrorIs(t, svc.revokeOfflineSource(context.Background(), item), ErrVPNConnection)
	remote.submitErr[1] = nil
	remote.mutateUser = func(_ int64, x *VPNSnapshot) { x.AccessState = "allowed" }
	require.Error(t, svc.revokeOfflineSource(context.Background(), item))
	remote.mutateUser = nil
	require.NoError(t, svc.revokeOfflineSource(context.Background(), item))
	require.Equal(t, m.SourceOwnerRef, repo.sub.OwnerRef)
	for _, call := range remote.calls {
		require.Equal(t, item.ID, call.payload.OperationID)
		require.Equal(t, "delete", call.payload.Action)
	}
}
func TestVPNHostTrafficConversionDoesNotModifyRawSnapshot(t *testing.T) {
	now := time.Now()
	start, end := vpnPeriodBounds(now)
	raw := int64(40)
	node := VPNServer{TrafficMultiplier: 2, TrafficQuotaBytes: 100, TrafficUsedOffsetBytes: 10, TrafficOffsetPeriodStart: &start, Healthy: true, LastCheckedAt: &now, TrafficSnapshot: &VPNNodeTraffic{UsedBytes: &raw, PeriodStart: &start, PeriodEnd: &end, SampledAt: &now, AccountingStatus: "ok"}}
	for i := 0; i < 2; i++ {
		hydrateVPNServer(&node, now)
		require.Equal(t, int64(90), *node.TrafficUsedBytes)
		require.Equal(t, int64(10), *node.TrafficRemainingBytes)
		require.Equal(t, int64(40), *node.TrafficSnapshot.UsedBytes)
	}
	require.False(t, node.TrafficExhausted(now))
	raw = 45
	require.True(t, node.TrafficExhausted(now))
	require.False(t, node.TrafficExhausted(end))
	raw = VPNMaxQuota
	hydrateVPNServer(&node, now)
	require.Equal(t, "invalid", node.TrafficAccountingStatus)
	require.Nil(t, node.TrafficRemainingBytes)
}
func TestVPNRotationOfflineFlagStaysAtPlatform(t *testing.T) {
	svc, _, remote := vpnServiceFixture(t)
	body, _ := json.Marshal(VPNOperationPayload{Action: "revoke", AllowOfflineMigration: true})
	svc.process(context.Background(), &VPNOperation{ID: remote.snapshot.LastOperationID, SubscriptionID: 1, Payload: body})
	require.Len(t, remote.payloads, 1)
	require.NotContains(t, remote.payloads[0], "allow_offline_migration")
}

func TestVPNDailyNodeAndUserAccountingBasis(t *testing.T) {
	cipher := &liveAttestationAES{key: [32]byte{1}}
	encrypted, err := cipher.Encrypt(`vpn-credentials:{"password":"fixture"}`)
	require.NoError(t, err)
	repo := &trafficTestRepo{servers: []VPNServer{{ID: 1, TrafficMultiplier: 1, CredentialsEncrypted: encrypted}, {ID: 3, TrafficMultiplier: 2, CredentialsEncrypted: encrypted}}, refs: map[int64][]string{1: {"old"}, 3: {"new"}}}
	remote := &trafficTestRemote{calls: make(chan []string, 10)}
	svc := NewVPNService(repo, cipher, remote)
	f := VPNTrafficFilter{StartDate: "2026-10-20", EndDate: "2026-10-21"}
	result, err := svc.DailyTraffic(context.Background(), f)
	require.NoError(t, err)
	require.Equal(t, int64(75), result.TotalBytes)
	require.Equal(t, "node_transfer", result.AccountingBasis)
	f.ServerID = 3
	result, err = svc.DailyTraffic(context.Background(), f)
	require.NoError(t, err)
	require.Equal(t, int64(50), result.TotalBytes)
	f.ServerID = 0
	f.UserID = 7
	result, err = svc.DailyTraffic(context.Background(), f)
	require.NoError(t, err)
	require.Equal(t, int64(50), result.TotalBytes)
	require.Equal(t, "user_proxy", result.AccountingBasis)
}
