//go:build unit

package middleware

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 全站订阅、财务或代理权限不能隐式覆盖同名的定制 VPN 和审批接口。
func TestScopedAdminKeyCannotAccessPunkcodeManagement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	policy := adminaccess.Policy{}
	for _, permission := range adminaccess.PermissionCatalog() {
		if permission.Scope == "global" {
			policy.GlobalPermissions = append(policy.GlobalPermissions, permission.ID)
		}
	}
	settings := service.NewSettingService(nil, nil)
	settings.SetScopedAdminKeyRepository(&adminAuthKeyRepo{})
	key, _, err := settings.CreateScopedAdminKey(context.Background(), service.ScopedAdminKeyInput{Name: "定制路由隔离验收", Enabled: true, Policy: policy}, 9)
	require.NoError(t, err)
	users := service.NewUserService(&stubUserRepo{getByID: func(context.Context, int64) (*service.User, error) {
		return &service.User{ID: 9, Role: service.RoleAdmin, Status: service.StatusActive}, nil
	}}, nil, nil, nil)
	r := gin.New()
	r.Use(gin.HandlerFunc(NewAdminAuthMiddleware(nil, users, settings, nil)))
	cases := []struct{ method, route, path string }{
		{"GET", "/vpn/summary", "/vpn/summary"},
		{"GET", "/vpn/subscriptions", "/vpn/subscriptions"},
		{"POST", "/vpn/subscriptions", "/vpn/subscriptions"},
		{"POST", "/vpn/subscriptions/:id/revoke", "/vpn/subscriptions/1/revoke"},
		{"GET", "/vpn/servers/:id/egress", "/vpn/servers/1/egress"},
		{"GET", "/balance-requests", "/balance-requests"},
		{"POST", "/balance-requests/:id/approve", "/balance-requests/1/approve"},
		{"GET", "/im-webhook/config", "/im-webhook/config"},
		{"PUT", "/im-webhook/config", "/im-webhook/config"},
	}
	for _, tc := range cases {
		r.Handle(tc.method, "/api/v1/admin"+tc.route, func(c *gin.Context) { t.Fatal("受限密钥不应到达定制处理器") })
	}
	for _, tc := range cases {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/api/v1/admin"+tc.path, nil)
			req.Header.Set("x-api-key", key)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, 403, w.Code)
			require.Contains(t, w.Body.String(), "FORBIDDEN")
		})
	}
}
