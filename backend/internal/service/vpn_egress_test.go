package service

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

type vpnEgressTestRemote struct {
	VPNRemote
	result *VPNEgress
	ensure []bool
}

func (r *vpnEgressTestRemote) Egress(_ context.Context, _ *VPNServer, _ VPNCredentials, ensure bool) (*VPNEgress, error) {
	r.ensure = append(r.ensure, ensure)
	copy := *r.result
	return &copy, nil
}

func vpnEgressFixture(t *testing.T) (*VPNService, *vpnEgressTestRemote) {
	t.Helper()
	svc, _, _ := vpnServiceFixture(t)
	remote := &vpnEgressTestRemote{result: &VPNEgress{Username: "sub2api", Status: "active", ApplyStatus: "applied", AccessState: "allowed",
		Unlimited: true, SubscriptionURL: "https://node.invalid/sub/egress-private-token"}}
	svc.remote = remote
	return svc, remote
}

func TestVPNEgressOnlyVerifiedUnlimitedAccountPublishesURLs(t *testing.T) {
	svc, remote := vpnEgressFixture(t)
	for _, ensure := range []bool{false, true} {
		result, err := svc.Egress(context.Background(), 1, ensure)
		require.NoError(t, err)
		require.Equal(t, map[string]string{
			"clash":   "https://node.invalid/sub/egress-private-token/clash-meta",
			"base64":  "https://node.invalid/sub/egress-private-token/v2ray",
			"singbox": "https://node.invalid/sub/egress-private-token/sing-box",
		}, result.SubscriptionURLs)
		encoded, err := json.Marshal(result)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), `"subscription_url":`)
	}
	require.Equal(t, []bool{false, true}, remote.ensure)
	for _, change := range []func(*VPNEgress){func(x *VPNEgress) { x.Username = "another" }, func(x *VPNEgress) { x.Unlimited = false }} {
		svc, remote := vpnEgressFixture(t)
		change(remote.result)
		result, err := svc.Egress(context.Background(), 1, true)
		require.Error(t, err)
		require.Nil(t, result)
		require.NotContains(t, err.Error(), "egress-private-token")
	}
}

func TestVPNEgressRejectsForeignOrMalformedPrivateURL(t *testing.T) {
	for _, raw := range []string{"", "http://node.invalid/sub/secret", "https://evil.invalid/sub/secret", "https://node.invalid:8443/sub/secret",
		"https://name:secret@node.invalid/sub/token", "https://node.invalid/sub/token?secret=yes", "https://node.invalid/sub/token#secret",
		"https://node.invalid/dashboard/token", "https://node.invalid/sub/", "https://node.invalid/sub////"} {
		t.Run(raw, func(t *testing.T) {
			svc, remote := vpnEgressFixture(t)
			remote.result.SubscriptionURL = raw
			result, err := svc.Egress(context.Background(), 1, true)
			require.Error(t, err)
			require.Nil(t, result)
			require.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestVPNEgressIncompleteStateClearsInjectedAndCachedLinks(t *testing.T) {
	for _, state := range []struct{ status, apply, access string }{
		{"active", "pending", "allowed"}, {"active", "failed", "unknown"}, {"active", "applied", "blocked"},
		{"disabled", "applied", "blocked"}, {"limited", "applied", "blocked"}, {"provisioning", "pending", "unknown"},
	} {
		t.Run(state.status+"_"+state.apply+"_"+state.access, func(t *testing.T) {
			svc, remote := vpnEgressFixture(t)
			remote.result.Status, remote.result.ApplyStatus, remote.result.AccessState = state.status, state.apply, state.access
			remote.result.SubscriptionURLs = map[string]string{"clash": "https://evil.invalid/sub/injected-private-token"}
			result, err := svc.Egress(context.Background(), 1, true)
			require.NoError(t, err)
			require.Empty(t, result.SubscriptionURLs)
			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "private-token")
		})
	}
}

func TestVPNEgressRemoteUsesAuthenticatedReadAndEnsureMethods(t *testing.T) {
	var methods []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/admin/token" {
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "test-bearer"})
			return
		}
		require.Equal(t, "/api/integration/egress", r.URL.Path)
		require.Equal(t, "Bearer test-bearer", r.Header.Get("Authorization"))
		require.Empty(t, r.URL.RawQuery)
		methods = append(methods, r.Method)
		_ = json.NewEncoder(w).Encode(map[string]any{"username": "sub2api", "status": "active", "apply_status": "applied", "access_state": "allowed", "unlimited": true,
			"subscription_url": "https://node.invalid/sub/private-token"})
	}))
	defer server.Close()
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	client := NewMarzbanVPNClient().(*MarzbanVPNClient)
	for _, ensure := range []bool{false, true} {
		result, err := client.Egress(context.Background(), &VPNServer{BaseURL: server.URL, AdminUsername: "admin"}, VPNCredentials{Password: "private", CAPEM: ca}, ensure)
		require.NoError(t, err)
		require.True(t, result.Unlimited)
		require.Equal(t, "https://node.invalid/sub/private-token", result.SubscriptionURL)
	}
	require.Equal(t, []string{"GET", "POST"}, methods)
}
