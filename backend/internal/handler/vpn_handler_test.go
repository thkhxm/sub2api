package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type vpnHandlerTestRepo struct {
	service.VPNRepository
	actor, userID, refreshID int64
	admin                    bool
	reserveError             error
}

func (r *vpnHandlerTestRepo) GetUserSubscription(_ context.Context, id int64) (*service.VPNSubscription, error) {
	r.userID = id
	return &service.VPNSubscription{ID: 12, UserID: id, RemoteUsername: "own-subscription", Status: "provisioning", ApplyStatus: "pending", QuotaBytes: service.VPNDefaultQuota}, nil
}
func (r *vpnHandlerTestRepo) Reserve(_ context.Context, id int64, admin bool, actor int64, owner string) (*service.VPNSubscription, error) {
	r.userID = id
	r.actor = actor
	r.admin = admin
	if r.reserveError != nil {
		return nil, r.reserveError
	}
	return &service.VPNSubscription{ID: 12, UserID: id, Status: "provisioning", ApplyStatus: "pending", QuotaBytes: service.VPNDefaultQuota}, nil
}
func (r *vpnHandlerTestRepo) RequestRefresh(_ context.Context, id int64) (bool, error) {
	r.refreshID = id
	return false, nil
}
func (r *vpnHandlerTestRepo) GetSubscription(_ context.Context, id int64) (*service.VPNSubscription, error) {
	return &service.VPNSubscription{ID: id, UserID: 41, Status: "provisioning", ApplyStatus: "pending"}, nil
}
func vpnTestRoute(h gin.HandlerFunc, subject bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if subject {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 41})
			c.Set(string(middleware.ContextKeyUserRole), service.RoleUser)
		}
		c.Next()
	})
	r.Any("/vpn/:id", h)
	return r
}
func TestVPNHandlerRequiresIdentity(t *testing.T) {
	h := NewVPNHandler(nil)
	for name, fn := range map[string]gin.HandlerFunc{"mine": h.Mine, "create": h.CreateMine, "refresh": h.RefreshMine} {
		t.Run(name, func(t *testing.T) {
			r := vpnTestRoute(fn, false)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("POST", "/vpn/999?user_id=999", strings.NewReader(`{"user_id":999}`)))
			require.Equal(t, http.StatusUnauthorized, w.Code)
		})
	}
}
func TestVPNHandlerUserCannotOverrideSubject(t *testing.T) {
	for _, name := range []string{"mine", "create", "refresh"} {
		t.Run(name, func(t *testing.T) {
			repo := &vpnHandlerTestRepo{}
			h := NewVPNHandler(service.NewVPNService(repo, nil, nil))
			fn := h.Mine
			if name == "create" {
				fn = h.CreateMine
			}
			if name == "refresh" {
				fn = h.RefreshMine
			}
			r := vpnTestRoute(fn, true)
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/vpn/999?user_id=999", strings.NewReader(`{"user_id":999,"admin":true}`))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			expected := http.StatusOK
			if name == "create" {
				expected = http.StatusAccepted
			}
			require.Equal(t, expected, w.Code, w.Body.String())
			require.Equal(t, int64(41), repo.userID)
			require.NotContains(t, w.Body.String(), `"user_id":999`)
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			if name == "create" {
				require.Equal(t, int64(41), repo.actor)
				require.False(t, repo.admin)
			}
			if name == "refresh" {
				require.Equal(t, int64(12), repo.refreshID)
			}
		})
	}
}
func TestVPNHandlerCreateErrorStatus(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{{service.ErrVPNBusy, 409}, {service.ErrVPNBalance, 403}, {service.ErrVPNNoServer, 503}} {
		repo := &vpnHandlerTestRepo{reserveError: tc.err}
		h := NewVPNHandler(service.NewVPNService(repo, nil, nil))
		w := httptest.NewRecorder()
		vpnTestRoute(h.CreateMine, true).ServeHTTP(w, httptest.NewRequest("POST", "/vpn/1", nil))
		require.Equal(t, tc.code, w.Code)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
}
func TestVPNHandlerAdminBoundaryAndValidation(t *testing.T) {
	// 使用真实管理员鉴权中间件，匿名请求不能进入管理 handler。
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewVPNHandler(nil)
	r.Use(gin.HandlerFunc(middleware.NewAdminAuthMiddleware(nil, nil, nil, nil)))
	r.GET("/admin/vpn/servers", h.Servers)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/admin/vpn/servers", nil))
	require.Equal(t, 401, w.Code)
	for _, id := range []string{"0", "-1", "abc", "99999999999999999999999"} {
		w := httptest.NewRecorder()
		vpnTestRoute(h.AdminUpdate, true).ServeHTTP(w, httptest.NewRequest("PUT", "/vpn/"+id, strings.NewReader(`{}`)))
		require.Equal(t, 400, w.Code)
	}
}
