package server

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"socat-webui/web"
)

func mountSPA(r *gin.Engine) {
	dist, err := fs.Sub(web.DistFS, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(dist))

	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "message": "接口不存在"})
			return
		}
		name := strings.TrimPrefix(c.Request.URL.Path, "/")
		if name != "" {
			if f, err := dist.Open(name); err == nil {
				f.Close()
				fileServer.ServeHTTP(c.Writer, c.Request)
				return
			}
		}
		index, err := fs.ReadFile(dist, "index.html")
		if err != nil || len(index) == 0 {
			c.String(http.StatusNotFound, "前端资源未构建")
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	})
}
