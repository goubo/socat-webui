package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"socat-webui/internal/proxy"
	appversion "socat-webui/internal/version"
)

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func respondError(c *gin.Context, err error) {
	var status int
	var code string
	switch {
	case errors.Is(err, ErrLogin):
		status, code = http.StatusUnauthorized, "invalid_credentials"
	case errors.Is(err, proxy.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, proxy.ErrValidation):
		status, code = http.StatusBadRequest, "validation"
	case errors.Is(err, proxy.ErrPortConflict):
		status, code = http.StatusConflict, "port_conflict"
	case errors.Is(err, proxy.ErrPortInUse):
		status, code = http.StatusConflict, "port_in_use"
	case errors.Is(err, proxy.ErrRemoteUnreachable):
		status, code = http.StatusBadRequest, "remote_unreachable"
	case errors.Is(err, proxy.ErrSocatMissing):
		status, code = http.StatusServiceUnavailable, "socat_missing"
	case errors.Is(err, proxy.ErrAlreadyRunning):
		status, code = http.StatusConflict, "already_running"
	default:
		slog.Error("API 内部错误", "path", c.FullPath(), "err", err)
		status, code = http.StatusInternalServerError, "internal"
	}
	c.JSON(status, errorResponse{Code: code, Message: err.Error()})
}

func pathID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Code: "validation", Message: "非法的代理 ID"})
		return 0, false
	}
	return uint(id), true
}

func handleLogin(auth *AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, errorResponse{Code: "validation", Message: "请求体格式错误"})
			return
		}
		token, err := auth.Login(body.Password, c.ClientIP())
		if err != nil {
			respondError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"token": token})
	}
}

func handleLogout(auth *AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		auth.Logout(bearerToken(c))
		c.JSON(http.StatusOK, gin.H{})
	}
}

func handleHealth(mgr *proxy.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, mgr.Health())
	}
}

func handleVersion() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"version": appversion.Version})
	}
}

func handleListProxies(mgr *proxy.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		list, err := mgr.List()
		if err != nil {
			respondError(c, err)
			return
		}
		c.JSON(http.StatusOK, list)
	}
}

func handleGetProxy(mgr *proxy.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		view, events, err := mgr.Get(id)
		if err != nil {
			respondError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"proxy": view, "events": events})
	}
}

func handleCreateProxy(mgr *proxy.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			proxy.ProxyInput
			Enabled *bool `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, errorResponse{Code: "validation", Message: "请求体格式错误"})
			return
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		view, err := mgr.Create(body.ProxyInput, enabled)
		if err != nil {
			respondError(c, err)
			return
		}
		c.JSON(http.StatusCreated, view)
	}
}

func handlePrecheck(mgr *proxy.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body proxy.ProxyInput
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, errorResponse{Code: "validation", Message: "请求体格式错误"})
			return
		}
		res, err := mgr.Precheck(body)
		if err != nil {
			respondError(c, err)
			return
		}
		c.JSON(http.StatusOK, res)
	}
}

func handleUpdateProxy(mgr *proxy.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		var body proxy.ProxyInput
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, errorResponse{Code: "validation", Message: "请求体格式错误"})
			return
		}
		view, err := mgr.Update(id, body)
		if err != nil {
			respondError(c, err)
			return
		}
		c.JSON(http.StatusOK, view)
	}
}

func handleEnableProxy(mgr *proxy.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		view, err := mgr.Enable(id)
		if err != nil {
			respondError(c, err)
			return
		}
		c.JSON(http.StatusOK, view)
	}
}

func handleDisableProxy(mgr *proxy.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		view, err := mgr.Disable(id)
		if err != nil {
			respondError(c, err)
			return
		}
		c.JSON(http.StatusOK, view)
	}
}

func handleDeleteProxy(mgr *proxy.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := pathID(c)
		if !ok {
			return
		}
		if err := mgr.Delete(id); err != nil {
			respondError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{})
	}
}

func bearerToken(c *gin.Context) string {
	return trimBearer(c.GetHeader("Authorization"))
}
