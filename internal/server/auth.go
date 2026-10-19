package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"socat-webui/internal/store"
)

const (
	passwordSettingKey = "password_hash"
	tokenTTL           = 24 * time.Hour
	maxLoginFails      = 5
	loginWindow        = time.Minute
)

// ErrLogin 登录失败（密码错误或限速），不区分细节以防探测。
var ErrLogin = errors.New("密码错误或尝试过于频繁")

// AuthService 单密码认证：bcrypt 哈希落库、
// token 存内存（重启失效）、按来源 IP 限速。
type AuthService struct {
	mu       sync.Mutex
	st       *store.Store
	sessions map[string]time.Time
	fails    map[string]*failState
	now      func() time.Time
}

type failState struct {
	count       int
	windowStart time.Time
}

func NewAuthService(st *store.Store, now func() time.Time) *AuthService {
	if now == nil {
		now = time.Now
	}
	return &AuthService{
		st:       st,
		sessions: make(map[string]time.Time),
		fails:    make(map[string]*failState),
		now:      now,
	}
}

// BootstrapPassword 确保存在密码哈希：环境变量优先写入；
// 无任何来源时生成随机初始密码并打印一次到 stdout。
func (a *AuthService) BootstrapPassword(envPassword string) error {
	if envPassword != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(envPassword), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("哈希密码: %w", err)
		}
		if stored, err := a.st.GetSetting(passwordSettingKey); err != nil || stored != string(hash) {
			return a.st.SetSetting(passwordSettingKey, string(hash))
		}
		return nil
	}
	if _, err := a.st.GetSetting(passwordSettingKey); err == nil {
		return nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	password, err := randomToken(9)
	if err != nil {
		return fmt.Errorf("生成初始密码: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("哈希密码: %w", err)
	}
	if err := a.st.SetSetting(passwordSettingKey, string(hash)); err != nil {
		return err
	}
	fmt.Printf("未设置面板密码，已生成初始密码: %s（请通过 SOCAT_WEBUI_PASSWORD 环境变量或 install --password 修改）\n", password)
	return nil
}

func (a *AuthService) Login(password, clientIP string) (string, error) {
	a.mu.Lock()
	if fs := a.fails[clientIP]; fs != nil && a.now().Sub(fs.windowStart) < loginWindow && fs.count >= maxLoginFails {
		a.mu.Unlock()
		return "", ErrLogin
	}
	a.mu.Unlock()

	hash, err := a.st.GetSetting(passwordSettingKey)
	if err != nil {
		return "", fmt.Errorf("读取密码配置: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		a.recordFail(clientIP)
		return "", ErrLogin
	}

	token, err := randomToken(32)
	if err != nil {
		return "", fmt.Errorf("生成 token: %w", err)
	}
	a.mu.Lock()
	delete(a.fails, clientIP)
	a.sessions[token] = a.now().Add(tokenTTL)
	a.mu.Unlock()
	return token, nil
}

func (a *AuthService) Logout(token string) {
	a.mu.Lock()
	delete(a.sessions, token)
	a.mu.Unlock()
}

func (a *AuthService) Verify(token string) bool {
	if token == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	expiry, ok := a.sessions[token]
	if !ok {
		return false
	}
	if !a.now().Before(expiry) {
		delete(a.sessions, token)
		return false
	}
	return true
}

func (a *AuthService) recordFail(clientIP string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	fs := a.fails[clientIP]
	if fs == nil || now.Sub(fs.windowStart) >= loginWindow {
		fs = &failState{windowStart: now}
		a.fails[clientIP] = fs
	}
	fs.count++
	if fs.count >= maxLoginFails {
		slog.Warn("登录失败次数过多，暂时锁定来源", "clientIP", clientIP)
	}
}

// Middleware 校验 Authorization: Bearer <token>；登录接口与静态资源不走此中间件。
func (a *AuthService) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !a.Verify(trimBearer(c.GetHeader("Authorization"))) {
			c.AbortWithStatusJSON(401, gin.H{"code": "unauthorized", "message": "未登录或会话已过期"})
			return
		}
		c.Next()
	}
}

func trimBearer(header string) string {
	return strings.TrimPrefix(header, "Bearer ")
}

func randomToken(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
