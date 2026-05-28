package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"

	"github.com/gin-gonic/gin"
)

// registerBalanceRequestAdminRoutes 注册管理员侧的余额申请审批路由。
//
// 由 admin.go 内统一调用，挂在已经经过 adminAuth 中间件的 group 下。
func registerBalanceRequestAdminRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	g := admin.Group("/balance-requests")
	{
		g.GET("", h.BalanceRequest.AdminList)
		g.POST("/:id/approve", h.BalanceRequest.AdminApprove)
		g.POST("/:id/reject", h.BalanceRequest.AdminReject)
	}
}
