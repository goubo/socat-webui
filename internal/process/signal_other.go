//go:build !linux && !darwin

package process

import (
	"fmt"
	"syscall"
)

func killProcessGroup(pgid int, sig syscall.Signal) error {
	return fmt.Errorf("当前平台不支持进程组信号 pgid=%d sig=%v", pgid, sig)
}
