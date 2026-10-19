package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"socat-webui/internal/store"
)

const testPassword = "correct-horse-battery"

func newTestAuth(t *testing.T, now func() time.Time) *AuthService {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	auth := NewAuthService(st, now)
	if err := auth.BootstrapPassword(testPassword); err != nil {
		t.Fatalf("初始化密码失败: %v", err)
	}
	return auth
}

func TestLoginSuccessAndWrongPassword(t *testing.T) {
	auth := newTestAuth(t, nil)

	token, err := auth.Login(testPassword, "1.2.3.4")
	if err != nil || token == "" {
		t.Fatalf("正确密码应登录成功: %v", err)
	}
	if !auth.Verify(token) {
		t.Fatal("新 token 应有效")
	}
	if _, err := auth.Login("wrong", "1.2.3.4"); err != ErrLogin {
		t.Fatalf("错误密码应返回 ErrLogin: %v", err)
	}
}

func TestLoginRateLimit(t *testing.T) {
	auth := newTestAuth(t, nil)

	for i := 0; i < 5; i++ {
		if _, err := auth.Login("wrong", "9.9.9.9"); err == nil {
			t.Fatal("错误密码不应成功")
		}
	}
	// 同 IP 连正确密码也应被限速拒绝
	if _, err := auth.Login(testPassword, "9.9.9.9"); err != ErrLogin {
		t.Fatalf("限速应生效: %v", err)
	}
	// 其他 IP 不受影响
	if _, err := auth.Login(testPassword, "8.8.8.8"); err != nil {
		t.Fatalf("其他 IP 不应被限速: %v", err)
	}
}

func TestTokenExpiry(t *testing.T) {
	now := time.Now()
	auth := newTestAuth(t, func() time.Time { return now })

	token, err := auth.Login(testPassword, "1.1.1.1")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if !auth.Verify(token) {
		t.Fatal("有效期内 token 应通过")
	}
	now = now.Add(25 * time.Hour)
	if auth.Verify(token) {
		t.Fatal("过期 token 应失效")
	}
}

func TestLogoutInvalidatesToken(t *testing.T) {
	auth := newTestAuth(t, nil)

	token, _ := auth.Login(testPassword, "1.1.1.1")
	auth.Logout(token)
	if auth.Verify(token) {
		t.Fatal("登出后 token 应失效")
	}
}

func TestAuthMiddleware(t *testing.T) {
	auth := newTestAuth(t, nil)
	r := gin.New()
	r.GET("/api/ping", auth.Middleware(), func(c *gin.Context) { c.Status(200) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/ping", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无 token 应 401, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/ping", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无效 token 应 401, got %d", rec.Code)
	}

	token, err := auth.Login(testPassword, "1.1.1.1")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/ping", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("有效 token 应 200, got %d", rec.Code)
	}
}
