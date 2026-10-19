# socat-webui

socat 的 WebUI 控制面板：把本机所在网段其他服务器的地址（IP:Port）映射到本地指定端口，通过浏览器完成代理的增删改查与状态管理。前后端分离开发，合并打包为单一二进制交付。

## 快速开始

```bash
make build                      # 构建前端 + 单二进制到 bin/socat-webui
./bin/socat-webui serve         # 默认监听 0.0.0.0:15200
./bin/socat-webui serve --listen 127.0.0.1 --port 18080 --db ./data.db
```

## 开发

```bash
make dev       # 前端 dev server（/api 代理到 127.0.0.1:15200）
go run ./cmd/socat-webui serve
make test
```

## 交叉编译

```bash
make build-linux-amd64   # bin/linux_amd64/socat-webui
make build-linux-arm64   # bin/linux_arm64/socat-webui
```

## 外部依赖

- socat：端口映射执行器，程序启动时自动检测；缺失时代理管理操作不可用。
