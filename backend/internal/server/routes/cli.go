package routes

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/middleware"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RegisterCliRoutes 注册 PunkcodeAI 桌面端 CLI 模式专用路由。
//
// 路由前缀：/api/v1/cli
//
//   - 公开接口（注册/登录/刷新）走 Redis 限流（fail-close）
//   - 已认证接口（me/api-key/llm/usage）需要 JWT
func RegisterCliRoutes(
	v1 *gin.RouterGroup,
	h *handler.Handlers,
	jwtAuth servermiddleware.JWTAuthMiddleware,
	redisClient *redis.Client,
) {
	rateLimiter := middleware.NewRateLimiter(redisClient)

	cli := v1.Group("/cli")

	// === 公开接口 ===
	{
		cli.POST("/register",
			rateLimiter.LimitWithOptions("cli-register", 5, time.Minute, middleware.RateLimitOptions{
				FailureMode: middleware.RateLimitFailClose,
			}),
			h.Cli.Register,
		)

		cli.POST("/login",
			rateLimiter.LimitWithOptions("cli-login", 20, time.Minute, middleware.RateLimitOptions{
				FailureMode: middleware.RateLimitFailClose,
			}),
			h.Cli.Login,
		)

		cli.POST("/refresh",
			rateLimiter.LimitWithOptions("cli-refresh", 30, time.Minute, middleware.RateLimitOptions{
				FailureMode: middleware.RateLimitFailClose,
			}),
			h.Cli.Refresh,
		)

		cli.POST("/logout", h.Cli.Logout)
	}

	// === 需要认证的接口 ===
	cliAuth := cli.Group("")
	cliAuth.Use(gin.HandlerFunc(jwtAuth))
	{
		cliAuth.GET("/me", h.Cli.GetMe)
		cliAuth.GET("/api-key", h.Cli.GetApiKey)
		cliAuth.GET("/llm", h.Cli.GetLlm)
		cliAuth.GET("/usage", h.Cli.GetUsage)

		// 余额申请：用户提交 + 自己看自己的列表
		cliAuth.POST("/balance-requests", h.BalanceRequest.CreateMine)
		cliAuth.GET("/balance-requests", h.BalanceRequest.ListMine)
	}
}
