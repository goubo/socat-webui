//go:build !linux

package systemd

import "fmt"

// Install 在非 Linux 平台返回明确错误；macOS 开发机直接前台运行 serve。
func Install(opts InstallOptions) error {
	return fmt.Errorf("systemd 注册仅支持 Linux；当前平台请直接运行 serve（开发模式）")
}

// Uninstall 同上。
func Uninstall() error {
	return fmt.Errorf("systemd 卸载仅支持 Linux")
}
