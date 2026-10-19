package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"socat-webui/internal/config"
	"socat-webui/internal/proxy"
	"socat-webui/internal/store"
)

type testEnv struct {
	ts         *httptest.Server
	token      string
	remotePort int
	cleanup    func()
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	if _, err := exec.LookPath("socat"); err != nil {
		t.Skip("本机未安装 socat，跳过真实转发集成测试")
	}

	remotePort, stopRemote := startEchoServer(t)
	st, err := store.Open(t.TempDir() + "/api.db")
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	mgr := proxy.NewManager(st, proxy.ManagerConfig{SelfPort: 15200}, nil)
	auth := NewAuthService(st, nil)
	if err := auth.BootstrapPassword(testPassword); err != nil {
		t.Fatalf("初始化密码失败: %v", err)
	}
	ts := httptest.NewServer(New(config.Config{Port: 15200}, mgr, auth))

	token := loginOK(t, ts.URL, testPassword)
	return &testEnv{
		ts:         ts,
		token:      token,
		remotePort: remotePort,
		cleanup: func() {
			ts.Close()
			mgr.Shutdown()
			stopRemote()
			_ = st.Close()
		},
	}
}

func startEchoServer(t *testing.T) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听假远端失败: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					if _, err := c.Write(buf[:n]); err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, func() { ln.Close() }
}

func freePortForAPI(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func newHandlerForTest(t *testing.T) (*gin.Engine, *AuthService, func()) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/handler.db")
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	mgr := proxy.NewManager(st, proxy.ManagerConfig{SocatPath: "/nonexistent/socat", SelfPort: 15200}, nil)
	auth := NewAuthService(st, nil)
	if err := auth.BootstrapPassword(testPassword); err != nil {
		t.Fatalf("初始化密码失败: %v", err)
	}
	return New(config.Config{Port: 15200}, mgr, auth), auth, func() {
		mgr.Shutdown()
		_ = st.Close()
	}
}

func TestLoginIgnoresUntrustedForwardedFor(t *testing.T) {
	r, auth, cleanup := newHandlerForTest(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]string{"password": "wrong"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.RemoteAddr = "192.0.2.10:12345"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "198.51.100.20")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("错误密码应返回 401, got %d", rec.Code)
	}
	if _, ok := auth.fails["192.0.2.10"]; !ok {
		t.Fatalf("限速应记录直连来源，当前记录: %+v", auth.fails)
	}
	if _, ok := auth.fails["198.51.100.20"]; ok {
		t.Fatal("不应信任 X-Forwarded-For")
	}
}

func TestAPIRequestBodyLimit(t *testing.T) {
	r, _, cleanup := newHandlerForTest(t)
	defer cleanup()

	body := bytes.Repeat([]byte("x"), int(maxAPIRequestBodyBytes)+1)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超限请求体应返回 413, got %d", rec.Code)
	}
}

func loginOK(t *testing.T, baseURL, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"password": password})
	resp, err := http.Post(baseURL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("登录请求失败: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Token == "" {
		t.Fatalf("登录应返回 token: %v", err)
	}
	return out.Token
}

// do 执行带 token 的 JSON 请求，返回状态码与解析后的 body（对象或数组）。
func (e *testEnv) do(t *testing.T, method, path string, body any, withToken bool) (int, any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, reader)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if withToken {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 %s %s 失败: %v", method, path, err)
	}
	defer resp.Body.Close()
	var out any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		out = map[string]any{}
	}
	return resp.StatusCode, out
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("响应应为 JSON 对象: %v", v)
	}
	return m
}

func waitForDial(t *testing.T, port int, want bool, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			if want {
				return true
			}
		} else if !want {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func TestProxyLifecycleWithRealSocat(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	// 未认证访问被拒
	code, _ := env.do(t, "GET", "/api/proxies", nil, false)
	if code != http.StatusUnauthorized {
		t.Fatalf("无 token 应 401, got %d", code)
	}
	// 错误密码登录失败
	if token := tryLogin(t, env.ts.URL, "wrong"); token != "" {
		t.Fatal("错误密码不应获得 token")
	}

	// health：socat 可用
	code, raw := env.do(t, "GET", "/api/health", nil, true)
	health := asMap(t, raw)
	if code != 200 || health["socat"].(map[string]any)["available"] != true {
		t.Fatalf("health 应显示 socat 可用: %v", health)
	}

	localPort := freePortForAPI(t)

	// precheck 全通
	code, raw = env.do(t, "POST", "/api/proxies/precheck", map[string]any{
		"name": "t", "localPort": localPort, "remoteHost": "127.0.0.1", "remotePort": env.remotePort,
	}, true)
	pc := asMap(t, raw)
	if code != 200 || pc["remoteOk"] != true || pc["localFree"] != true {
		t.Fatalf("precheck 应全通: %d %v", code, pc)
	}

	// 创建并启动
	code, raw = env.do(t, "POST", "/api/proxies", map[string]any{
		"name": "echo-远端", "localPort": localPort, "remoteHost": "127.0.0.1", "remotePort": env.remotePort,
	}, true)
	created := asMap(t, raw)
	if code != http.StatusCreated {
		t.Fatalf("创建应 201, got %d: %v", code, created)
	}
	runtime := created["runtime"].(map[string]any)
	if runtime["status"] != "running" {
		t.Fatalf("创建后应 running: %v", created)
	}
	proxyID := int(created["id"].(float64))

	// 数据经 socat 真实转发
	if !waitForDial(t, localPort, true, 3*time.Second) {
		t.Fatal("本地映射端口应可连通")
	}
	if got := echoOnce(t, localPort); got != "ping" {
		t.Fatalf("转发数据不符: %q", got)
	}

	// 列表
	code, listArr := env.do(t, "GET", "/api/proxies", nil, true)
	if code != 200 || len(listArr.([]any)) != 1 {
		t.Fatalf("列表应含 1 条: %d %v", code, listArr)
	}

	// 重复本地端口 → 409
	code, raw = env.do(t, "POST", "/api/proxies", map[string]any{
		"name": "dup", "localPort": localPort, "remoteHost": "127.0.0.1", "remotePort": env.remotePort,
	}, true)
	dup := asMap(t, raw)
	if code != http.StatusConflict || dup["code"] != "port_conflict" {
		t.Fatalf("重复端口应 409 port_conflict: %d %v", code, dup)
	}

	// 远端不可达 → 400
	code, raw = env.do(t, "POST", "/api/proxies", map[string]any{
		"name": "dead", "localPort": freePortForAPI(t), "remoteHost": "127.0.0.1", "remotePort": freePortForAPI(t),
	}, true)
	unreach := asMap(t, raw)
	if code != http.StatusBadRequest || unreach["code"] != "remote_unreachable" {
		t.Fatalf("远端不通应 400 remote_unreachable: %d %v", code, unreach)
	}

	// 本地端口被第三方占用 → 409
	ln, _ := net.Listen("tcp", ":0")
	occupied := ln.Addr().(*net.TCPAddr).Port
	code, raw = env.do(t, "POST", "/api/proxies", map[string]any{
		"name": "busy", "localPort": occupied, "remoteHost": "127.0.0.1", "remotePort": env.remotePort,
	}, true)
	inUse := asMap(t, raw)
	ln.Close()
	if code != http.StatusConflict || inUse["code"] != "port_in_use" {
		t.Fatalf("端口被占应 409 port_in_use: %d %v", code, inUse)
	}

	// 停用 → 端口关闭
	code, _ = env.do(t, "POST", fmt.Sprintf("/api/proxies/%d/disable", proxyID), nil, true)
	if code != 200 {
		t.Fatalf("停用应 200, got %d", code)
	}
	if !waitForDial(t, localPort, false, 3*time.Second) {
		t.Fatal("停用后端口应关闭")
	}

	// 启用 → 恢复转发
	code, raw = env.do(t, "POST", fmt.Sprintf("/api/proxies/%d/enable", proxyID), nil, true)
	enabled := asMap(t, raw)
	if code != 200 || enabled["runtime"].(map[string]any)["status"] != "running" {
		t.Fatalf("启用后应 running: %v", enabled)
	}
	if !waitForDial(t, localPort, true, 3*time.Second) {
		t.Fatal("启用后端口应恢复")
	}

	// 修改名称（运行中更新 → 停止 → 重启）
	code, raw = env.do(t, "PUT", fmt.Sprintf("/api/proxies/%d", proxyID), map[string]any{
		"name": "echo-renamed", "localPort": localPort, "remoteHost": "127.0.0.1", "remotePort": env.remotePort,
	}, true)
	updated := asMap(t, raw)
	if code != 200 || updated["name"] != "echo-renamed" {
		t.Fatalf("修改应生效: %d %v", code, updated)
	}
	if !waitForDial(t, localPort, true, 3*time.Second) {
		t.Fatal("修改后重启应恢复转发")
	}

	// 详情含事件
	code, raw = env.do(t, "GET", fmt.Sprintf("/api/proxies/%d", proxyID), nil, true)
	detail := asMap(t, raw)
	if code != 200 || len(detail["events"].([]any)) == 0 {
		t.Fatalf("详情应含事件: %d %v", code, detail)
	}

	// 登出后 token 失效
	code, _ = env.do(t, "POST", "/api/auth/logout", nil, true)
	if code != 200 {
		t.Fatalf("登出应 200, got %d", code)
	}
	code, _ = env.do(t, "GET", "/api/proxies", nil, true)
	if code != http.StatusUnauthorized {
		t.Fatalf("登出后旧 token 应 401, got %d", code)
	}

	// 重新登录后删除代理
	env.token = loginOK(t, env.ts.URL, testPassword)
	code, _ = env.do(t, "DELETE", fmt.Sprintf("/api/proxies/%d", proxyID), nil, true)
	if code != 200 {
		t.Fatalf("删除应 200, got %d", code)
	}
	if !waitForDial(t, localPort, false, 3*time.Second) {
		t.Fatal("删除后端口应关闭")
	}
	code, list2 := env.do(t, "GET", "/api/proxies", nil, true)
	if code != 200 || len(list2.([]any)) != 0 {
		t.Fatalf("删除后列表应为空: %v", list2)
	}
}

func tryLogin(t *testing.T, baseURL, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"password": password})
	resp, err := http.Post(baseURL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("登录请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ""
	}
	var out struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.Token
}

func echoOnce(t *testing.T, port int) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 2*time.Second)
	if err != nil {
		t.Fatalf("连接映射端口失败: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("读取回显失败: %v", err)
	}
	return string(buf[:n])
}
