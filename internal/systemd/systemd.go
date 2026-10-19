package systemd

// InstallOptions install 子命令参数；BinPath 为空时取当前可执行文件。
type InstallOptions struct {
	BinPath  string
	Listen   string
	Port     int
	DBPath   string
	Password string // 明文密码，仅写哈希入库；空则服务首启自动生成（journalctl 查看）
}
