//go:build linux

package systemd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"

	"golang.org/x/crypto/bcrypt"

	"socat-webui/internal/store"
)

const (
	unitName    = "socat-webui"
	unitPath    = "/etc/systemd/system/socat-webui.service"
	defaultData = "/var/lib/socat-webui"
)

// unitTemplate 不设置 User=：程序设计为 root 运行（D-010）。
const unitTemplate = `[Unit]
Description=socat-webui 端口映射控制面板
After=network-online.target
Wants=network-online.target

[Service]
ExecStart={{.BinPath}} serve --listen {{.Listen}} --port {{.Port}} --db {{.DBPath}}
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
`

// Install 注册并启动 systemd 服务：root/systemd 校验 → 数据目录与密码 → 写 unit → enable --now。
func Install(opts InstallOptions) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("install 需要 root 权限，请使用 sudo 执行")
	}
	if err := requireSystemd(); err != nil {
		return err
	}

	bin := opts.BinPath
	if bin == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("获取当前程序路径: %w", err)
		}
		bin = exe
	}
	absBin, err := filepath.Abs(bin)
	if err != nil {
		return fmt.Errorf("解析程序绝对路径: %w", err)
	}

	if opts.DBPath == "" {
		opts.DBPath = filepath.Join(defaultData, "proxies.db")
	}
	if err := os.MkdirAll(filepath.Dir(opts.DBPath), 0o700); err != nil {
		return fmt.Errorf("创建数据目录: %w", err)
	}
	if opts.Password != "" {
		if err := writePasswordHash(opts.DBPath, opts.Password); err != nil {
			return err
		}
	}

	unit, err := renderUnit(absBin, opts)
	if err != nil {
		return err
	}
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("写入 unit 文件 %s: %w", unitPath, err)
	}
	if err := runSystemctl("daemon-reload"); err != nil {
		return err
	}
	if err := runSystemctl("enable", "--now", unitName); err != nil {
		return err
	}
	fmt.Printf("已安装并启动 %s 服务（unit: %s，数据: %s）\n", unitName, unitPath, opts.DBPath)
	if opts.Password == "" {
		fmt.Println("未设置密码：首次启动将自动生成随机密码，使用 `journalctl -u socat-webui -b | grep 初始密码` 查看")
	}
	return nil
}

// Uninstall 停用并移除 systemd 服务；数据目录保留。
func Uninstall() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("uninstall 需要 root 权限，请使用 sudo 执行")
	}
	if err := requireSystemd(); err != nil {
		return err
	}
	if err := runSystemctl("disable", "--now", unitName); err != nil {
		fmt.Printf("警告：停用服务失败（可能未安装）：%v\n", err)
	}
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除 unit 文件: %w", err)
	}
	if err := runSystemctl("daemon-reload"); err != nil {
		return err
	}
	fmt.Printf("已卸载 %s 服务（数据目录 %s 保留，可手动删除）\n", unitName, defaultData)
	return nil
}

func writePasswordHash(dbPath, password string) error {
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("哈希密码: %w", err)
	}
	if err := st.SetSetting("password_hash", string(hash)); err != nil {
		return fmt.Errorf("写入密码哈希: %w", err)
	}
	return nil
}

func renderUnit(bin string, opts InstallOptions) (string, error) {
	tpl, err := template.New("unit").Parse(unitTemplate)
	if err != nil {
		return "", fmt.Errorf("解析 unit 模板: %w", err)
	}
	var buf bytes.Buffer
	data := struct {
		BinPath string
		Listen  string
		Port    int
		DBPath  string
	}{bin, opts.Listen, opts.Port, opts.DBPath}
	if err := tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("渲染 unit: %w", err)
	}
	return buf.String(), nil
}

func requireSystemd() error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("未找到 systemctl，无法注册 systemd 服务")
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return fmt.Errorf("systemd 未运行（/run/systemd/system 不存在）；容器内需以 systemd 作为 PID 1")
	}
	return nil
}

func runSystemctl(args ...string) error {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %v 失败: %w: %s", args, err, bytes.TrimSpace(out))
	}
	return nil
}
