package server

import (
	"github.com/gin-gonic/gin"

	"socat-webui/internal/config"
	"socat-webui/internal/proxy"
)

// New 构建 Gin 引擎：/api 路由组（登录免认证，其余 Bearer 保护）+ 嵌入式 SPA 兜底。
func New(cfg config.Config, mgr *proxy.Manager, auth *AuthService) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	api := r.Group("/api")
	api.POST("/auth/login", handleLogin(auth))

	protected := api.Group("", auth.Middleware())
	protected.POST("/auth/logout", handleLogout(auth))
	protected.GET("/health", handleHealth(mgr))
	protected.GET("/proxies", handleListProxies(mgr))
	protected.GET("/proxies/:id", handleGetProxy(mgr))
	protected.POST("/proxies", handleCreateProxy(mgr))
	protected.POST("/proxies/precheck", handlePrecheck(mgr))
	protected.PUT("/proxies/:id", handleUpdateProxy(mgr))
	protected.POST("/proxies/:id/enable", handleEnableProxy(mgr))
	protected.POST("/proxies/:id/disable", handleDisableProxy(mgr))
	protected.DELETE("/proxies/:id", handleDeleteProxy(mgr))

	mountSPA(r)
	return r
}
