package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"socat-webui/internal/config"
)

// New 构建 Gin 引擎：/api 路由组 + 嵌入式 SPA 兜底。
func New(cfg config.Config) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	api := r.Group("/api")
	api.GET("/health", handleHealth)

	mountSPA(r)
	return r
}

func handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"socat":   gin.H{"available": false, "version": "", "path": ""},
		"proxies": gin.H{"running": 0, "failed": 0},
	})
}
