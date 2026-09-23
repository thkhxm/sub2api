package service

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// 服务边界使用可观察仓储；数据库一致性由 vpnintegration 真实 PostgreSQL 测试单独验证。
type vpnServiceTestRepo struct {
	VPNRepository
	server          *VPNServer
	sub             *VPNSubscription
	saved           VPNSnapshot
	encryptedURL    string
	savedCount      int
	finishState     string
	finishOperation string
}

func (r *vpnServiceTestRepo) PrepareOperation(_ context.Context, o *VPNOperation) (*VPNOperation, error) {
	return o, nil
}
func (r *vpnServiceTestRepo) MarkOperationDispatched(_ context.Context, _ *VPNOperation) error {
	return nil
}

func (r *vpnServiceTestRepo) GetServer(context.Context, int64) (*VPNServer, error) {
	v := *r.server
	return &v, nil
}
func (r *vpnServiceTestRepo) SaveServer(_ context.Context, v *VPNServer) (*VPNServer, error) {
	copy := *v
	if copy.ID == 0 {
		copy.ID = 1
	}
	r.server = &copy
	return &copy, nil
}
func (r *vpnServiceTestRepo) UpdateServerHealth(_ context.Context, _ int64, m *VPNServerMeta, message string) error {
	r.server.Healthy = m != nil && m.Healthy
	r.server.HealthError = message
	return nil
}
func (r *vpnServiceTestRepo) GetSubscription(context.Context, int64) (*VPNSubscription, error) {
	v := *r.sub
	return &v, nil
}
func (r *vpnServiceTestRepo) SaveSnapshot(_ context.Context, _ int64, x VPNSnapshot, encrypted string) error {
	r.saved = x
	r.encryptedURL = encrypted
	r.savedCount++
	r.sub.Snapshot = x
	r.sub.ApplyStatus = x.ApplyStatus
	return nil
}
func (r *vpnServiceTestRepo) Finish(_ context.Context, o *VPNOperation, state, message string) error {
	r.finishState = state
	r.finishOperation = o.ID
	return nil
}

type vpnServiceTestRemote struct {
	VPNRemote
	snapshot  *VPNSnapshot
	submit    *VPNRemoteOperation
	polled    *VPNRemoteOperation
	submitErr error
	payloads  []string
}

func (r *vpnServiceTestRemote) User(context.Context, *VPNServer, VPNCredentials, string) (*VPNSnapshot, error) {
	v := *r.snapshot
	return &v, nil
}
func (r *vpnServiceTestRemote) Submit(_ context.Context, _ *VPNServer, _ VPNCredentials, _ string, p json.RawMessage) (*VPNRemoteOperation, error) {
	r.payloads = append(r.payloads, string(p))
	return r.submit, r.submitErr
}
func (r *vpnServiceTestRemote) Operation(context.Context, *VPNServer, VPNCredentials, string) (*VPNRemoteOperation, error) {
	return r.polled, nil
}

func vpnServiceFixture(t *testing.T) (*VPNService, *vpnServiceTestRepo, *vpnServiceTestRemote) {
	t.Helper()
	cipher := &liveAttestationAES{key: [32]byte{1, 2, 3}}
	encrypted, e := cipher.Encrypt(`vpn-credentials:{"password":"private-password","ca_pem":""}`)
	require.NoError(t, e)
	id := uuid.NewString()
	owner := uuid.NewString()
	now := time.Now()
	x := &VPNSnapshot{Username: "pc_test", OwnerRef: owner, Status: "active", ApplyStatus: "applied", AccessState: "allowed", SubscriptionURL: "https://node.invalid/sub/private-token", DataLimit: VPNDefaultQuota, UploadBytes: 10, DownloadBytes: 20, UsedTraffic: 30, AccountingStatus: "ok", SampledAt: &now, LastOperationID: id}
	repo := &vpnServiceTestRepo{server: &VPNServer{ID: 1, Enabled: true, BaseURL: "https://node.invalid", CredentialsEncrypted: encrypted}, sub: &VPNSubscription{ID: 1, ServerID: 1, OwnerRef: owner, RemoteUsername: x.Username, QuotaBytes: VPNDefaultQuota}}
	remote := &vpnServiceTestRemote{snapshot: x, submit: &VPNRemoteOperation{OperationID: id, Status: "succeeded"}}
	return NewVPNService(repo, cipher, remote), repo, remote
}
func TestVPNServiceSyncValidationAndEncryption(t *testing.T) {
	svc, repo, remote := vpnServiceFixture(t)
	ctx := context.Background()
	require.NoError(t, svc.sync(ctx, 1))
	require.Equal(t, 1, repo.savedCount)
	require.NotContains(t, repo.encryptedURL, "private-token")
	plain, e := svc.cipher.Decrypt(repo.encryptedURL)
	require.NoError(t, e)
	require.Equal(t, "vpn-url:"+remote.snapshot.SubscriptionURL, plain)
	cases := map[string]func(*VPNSnapshot){"owner": func(x *VPNSnapshot) { x.OwnerRef = uuid.NewString() }, "username": func(x *VPNSnapshot) { x.Username = "other" }, "negative_usage": func(x *VPNSnapshot) { x.UsedTraffic = -1 }, "zero_quota": func(x *VPNSnapshot) { x.DataLimit = 0 }, "foreign_host": func(x *VPNSnapshot) { x.SubscriptionURL = "https://evil.invalid/sub/token" }, "http": func(x *VPNSnapshot) { x.SubscriptionURL = "http://node.invalid/sub/token" }, "userinfo": func(x *VPNSnapshot) { x.SubscriptionURL = "https://user:pass@node.invalid/sub/token" }, "query": func(x *VPNSnapshot) { x.SubscriptionURL = "https://node.invalid/sub/token?x=1" }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s, r, up := vpnServiceFixture(t)
			change(up.snapshot)
			require.Error(t, s.sync(ctx, 1))
			require.Zero(t, r.savedCount)
		})
	}
}
func TestVPNServiceHydrateVisibilityAndStaleness(t *testing.T) {
	svc, _, remote := vpnServiceFixture(t)
	now := time.Now()
	encrypted, e := svc.cipher.Encrypt("vpn-url:" + remote.snapshot.SubscriptionURL)
	require.NoError(t, e)
	for _, state := range []struct {
		status, apply, access string
		visible               bool
	}{{"active", "applied", "allowed", true}, {"active", "pending", "allowed", false}, {"disabled", "applied", "blocked", false}, {"limited", "applied", "blocked", false}, {"active", "failed", "unknown", false}} {
		sub := &VPNSubscription{Status: state.status, ApplyStatus: state.apply, OperationStatus: "succeeded", AccessState: state.access, URLEncrypted: encrypted, QuotaBytes: VPNDefaultQuota, Snapshot: *remote.snapshot, SyncedAt: &now}
		require.NoError(t, svc.hydrate(sub))
		require.Equal(t, state.visible, len(sub.SubscriptionURLs) > 0)
		require.Equal(t, int64(30), sub.UsedBytes)
		require.Equal(t, VPNDefaultQuota-30, sub.RemainingBytes)
		if state.visible {
			require.Equal(t, remote.snapshot.SubscriptionURL+"/clash-meta", sub.SubscriptionURLs["clash"])
			require.Equal(t, remote.snapshot.SubscriptionURL+"/v2ray", sub.SubscriptionURLs["base64"])
		}
		data, e := json.Marshal(sub)
		require.NoError(t, e)
		require.NotContains(t, string(data), encrypted)
	}
	old := time.Now().Add(-time.Hour)
	sub := &VPNSubscription{Snapshot: *remote.snapshot, SyncedAt: &now}
	sub.Snapshot.SampledAt = &old
	require.NoError(t, svc.hydrate(sub))
	require.Equal(t, "stale", sub.AccountingStatus)
	sub.Snapshot.AccountingStatus = "gap_detected"
	require.NoError(t, svc.hydrate(sub))
	require.Equal(t, "gap_detected", sub.AccountingStatus)
	sub.Snapshot.UsedTraffic = VPNDefaultQuota + 1
	require.NoError(t, svc.hydrate(sub))
	require.Zero(t, sub.RemainingBytes)
}
func TestVPNServiceOperationUnknownOutcomeReusesIdentity(t *testing.T) {
	svc, repo, remote := vpnServiceFixture(t)
	id := remote.snapshot.LastOperationID
	payload := json.RawMessage(`{"operation_id":"` + id + `","action":"create"}`)
	op := &VPNOperation{ID: id, SubscriptionID: 1, Payload: payload}
	remote.submitErr = errors.New("connection lost")
	svc.process(context.Background(), op)
	require.Equal(t, "pending", repo.finishState)
	require.Equal(t, id, repo.finishOperation)
	remote.submitErr = nil
	svc.process(context.Background(), op)
	require.Equal(t, "succeeded", repo.finishState)
	require.Len(t, remote.payloads, 2)
	require.Equal(t, remote.payloads[0], remote.payloads[1])
}
func TestVPNServiceOperationRequiresAppliedSnapshot(t *testing.T) {
	svc, repo, remote := vpnServiceFixture(t)
	remote.snapshot.ApplyStatus = "pending"
	svc.process(context.Background(), &VPNOperation{ID: remote.snapshot.LastOperationID, SubscriptionID: 1, Payload: json.RawMessage(`{"action":"update"}`)})
	require.Equal(t, "pending", repo.finishState)
}
func TestVPNServiceRejectsInvalidUpdate(t *testing.T) {
	svc := NewVPNService(nil, nil, nil)
	for _, n := range []int64{0, -1, VPNMaxQuota + 1} {
		_, e := svc.Update(context.Background(), 1, 1, VPNUpdate{QuotaBytes: &n})
		require.ErrorIs(t, e, ErrVPNInvalid)
	}
	_, e := svc.Update(context.Background(), 1, 1, VPNUpdate{})
	require.ErrorIs(t, e, ErrVPNInvalid)
}
func TestVPNServiceTLSClientCredentialsAndErrors(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/admin/token":
			require.NoError(t, r.ParseForm())
			require.Equal(t, "admin-test", r.Form.Get("username"))
			require.Equal(t, "private-password", r.Form.Get("password"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"bearer-test"}`))
		case "/api/integration/server":
			calls++
			require.Equal(t, "Bearer bearer-test", r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(VPNServerMeta{APIVersion: "sub2api-v1", Healthy: true, Protocol: "trojan", InboundTag: "TROJAN_TLS", AccountingStatus: "ok", SampledAt: func() *time.Time { n := time.Now(); return &n }()})
		default:
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("private-error-token"))
		}
	}))
	defer server.Close()
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	repo := &vpnServiceTestRepo{}
	cipher := &liveAttestationAES{key: [32]byte{2}}
	svc := NewVPNService(repo, cipher, NewMarzbanVPNClient())
	saved, e := svc.SaveServer(context.Background(), 0, VPNServerInput{Name: "test", BaseURL: server.URL, AdminUsername: "admin-test", AdminPassword: "private-password", CAPEM: &ca, Enabled: true})
	require.NoError(t, e)
	require.True(t, saved.Healthy)
	require.NotContains(t, saved.CredentialsEncrypted, "private-password")
	_, e = svc.SaveServer(context.Background(), saved.ID, VPNServerInput{Name: "renamed", BaseURL: server.URL, AdminUsername: "admin-test", Enabled: true})
	require.NoError(t, e)
	require.Equal(t, 2, calls)
	k, e := svc.credentials(repo.server)
	require.NoError(t, e)
	require.Equal(t, "private-password", k.Password)
	require.Equal(t, ca, k.CAPEM)
	raw, e := json.Marshal(repo.server)
	require.NoError(t, e)
	require.NotContains(t, string(raw), "private-password")
	require.NotContains(t, string(raw), "BEGIN CERTIFICATE")
	_, e = svc.remote.User(context.Background(), repo.server, k, "unknown")
	require.Error(t, e)
	require.NotContains(t, e.Error(), "private-error-token")
	require.NotContains(t, e.Error(), "bearer-test")
	for _, raw := range []string{"http://node.invalid", "https://user:pass@node.invalid", "https://node.invalid/path", "https://node.invalid?secret=x"} {
		_, e := validateVPNURL(raw)
		require.Error(t, e)
	}
	require.True(t, strings.HasPrefix(server.URL, "https://"))
}

func TestVPNServiceRejectsMismatchedPolledOperation(t *testing.T) {
	svc, repo, remote := vpnServiceFixture(t)
	remote.submit.Status = "pending"
	remote.polled = &VPNRemoteOperation{OperationID: uuid.NewString(), Status: "succeeded"}
	svc.process(context.Background(), &VPNOperation{ID: remote.snapshot.LastOperationID, SubscriptionID: 1, Payload: json.RawMessage(`{"action":"update"}`)})
	require.NotEqual(t, "succeeded", repo.finishState, "其他操作的轮询结果不能完成当前操作")
	require.Zero(t, repo.savedCount, "轮询归属不符时不应消费快照")
}
