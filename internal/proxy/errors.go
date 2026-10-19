package proxy

import "errors"

var (
	ErrNotFound          = errors.New("代理不存在")
	ErrValidation        = errors.New("参数校验失败")
	ErrPortConflict      = errors.New("本地端口已被其他代理使用")
	ErrPortInUse         = errors.New("本地端口被占用")
	ErrRemoteUnreachable = errors.New("远端不可达")
	ErrSocatMissing      = errors.New("socat 不可用")
	ErrAlreadyRunning    = errors.New("进程已在运行")
)
