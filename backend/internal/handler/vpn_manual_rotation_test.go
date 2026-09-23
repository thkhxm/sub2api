package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type vpnManualHandlerRepo struct {
	*vpnHandlerTestRepo
	queued    []service.VPNOperationPayload
	id, actor int64
}

func (r *vpnManualHandlerRepo) Queue(_ context.Context, id, actor int64, p service.VPNOperationPayload) error {
	r.id, r.actor = id, actor
	r.queued = append(r.queued, p)
	return nil
}

func TestVPNManualHandlerTargetRequiredBeforeQueue(t *testing.T) {
	for _, body := range []string{"", `{}`, `null`, `{"target_server_id":0}`, `{"target_server_id":-1}`, `{"target_server_id":"2"}`, `{"target_server_id":1.5}`, `{"target_server_id":9223372036854775808}`, `{"target_server_id":`} {
		t.Run(body, func(t *testing.T) {
			repo := &vpnManualHandlerRepo{vpnHandlerTestRepo: &vpnHandlerTestRepo{}}
			h := NewVPNHandler(service.NewVPNService(repo, nil, nil))
			request := httptest.NewRequest(http.MethodPost, "/vpn/999", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			vpnTestRoute(h.AdminRevoke, true).ServeHTTP(response, request)
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			require.Empty(t, repo.queued)
		})
	}
}

func TestVPNManualHandlerAcceptsExplicitTargetAndActor(t *testing.T) {
	repo := &vpnManualHandlerRepo{vpnHandlerTestRepo: &vpnHandlerTestRepo{}}
	h := NewVPNHandler(service.NewVPNService(repo, nil, nil))
	request := httptest.NewRequest(http.MethodPost, "/vpn/999", strings.NewReader(`{"target_server_id":2}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	vpnTestRoute(h.AdminRevoke, true).ServeHTTP(response, request)
	require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
	require.Equal(t, int64(999), repo.id)
	require.Equal(t, int64(41), repo.actor)
	require.Equal(t, []service.VPNOperationPayload{{Action: "revoke", TargetServerID: 2}}, repo.queued)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
}
