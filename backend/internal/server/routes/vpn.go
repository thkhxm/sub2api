package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

// VPN 路由挂在现有用户/管理员鉴权和审计之后，避免额外暴露管理入口。
func registerVPNUserRoutes(g *gin.RouterGroup, h *handler.Handlers) {
	v := g.Group("/vpn")
	v.GET("/subscription", h.VPN.Mine)
	v.POST("/subscription", h.VPN.CreateMine)
	v.POST("/subscription/refresh", h.VPN.RefreshMine)
}
func registerVPNAdminRoutes(g *gin.RouterGroup, h *handler.Handlers) {
	v := g.Group("/vpn")
	v.GET("/summary", h.VPN.Summary)
	v.GET("/servers", h.VPN.Servers)
	v.POST("/servers", h.VPN.SaveServer)
	v.PUT("/servers/:id", h.VPN.SaveServer)
	v.POST("/servers/:id/probe", h.VPN.Probe)
	v.GET("/subscriptions", h.VPN.AdminList)
	v.POST("/subscriptions", h.VPN.AdminCreate)
	v.PUT("/subscriptions/:id", h.VPN.AdminUpdate)
	v.POST("/subscriptions/:id/revoke", h.VPN.AdminRevoke)
	v.POST("/subscriptions/:id/refresh", h.VPN.AdminRefresh)
	v.POST("/subscriptions/:id/retry", h.VPN.AdminRetry)
}
