//go:build linux || darwin

package process

import "syscall"

func killProcessGroup(pgid int, sig syscall.Signal) error {
	return syscall.Kill(-pgid, sig)
}
