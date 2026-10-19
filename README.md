# socat-webui

socat 的 WebUI 控制面板：把本机所在网段其他服务器的地址（IP:Port）映射到本地指定端口，通过浏览器完成代理的增删改查与状态管理。前后端分离开发、合并打包为单一二进制交付。

## 功能

- 单二进制交付：前端静态资源内嵌（`go:embed`），`CGO_ENABLED=0` 静态编译，支持 linux/amd64 与 linux/arm64
- 单密码认证：登录换取 token（24h 有效），登录限速；密码经 `install --password` 或环境变量设置
- 代理管理：新增（前置检查：远端连通性 + 本地端口占用）、列表、详情、修改、停用/启用、删除；状态以 socat 实际进程为准
- 异常自愈：进程异常退出按 1s/5s/30s 退避自动重启，3 次失败置 failed；运行稳定后计数清零
- 重启恢复：服务重启或系统重启后，自动恢复重启前处于启用状态的代理（SQLite 持久化）
- systemd 自注册：`install` / `uninstall` 子命令（root，以 root 运行）

## 快速开始

```bash
make build                      # 构建前端 + 单二进制到 bin/socat-webui
./bin/socat-webui serve         # 默认监听 0.0.0.0:15200
./bin/socat-webui serve --listen 127.0.0.1 --port 18080 --db ./data.db
```

首次启动若未设置密码，将在标准输出打印一次性初始密码。

### Linux 服务器部署（root）

```bash
make build-linux-amd64          # 或 build-linux-arm64
scp bin/linux_amd64/socat-webui root@server:/usr/local/bin/

# 服务器上（需已安装 socat 与 systemd）：
sudo /usr/local/bin/socat-webui install --port 15200 --password '你的面板密码'
sudo /usr/local/bin/socat-webui uninstall   # 卸载（数据目录保留）
```

安装后访问 `http://<server>:15200`。服务名 `socat-webui`，数据目录 `/var/lib/socat-webui/`。

## 开发

```bash
make dev       # 前端 dev server（/api 代理到 127.0.0.1:15200）
go run ./cmd/socat-webui serve
make test      # go test ./...（本机安装 socat 后包含真实转发集成用例）
```

配置来源优先级：flag > 环境变量（`SOCAT_WEBUI_LISTEN` / `SOCAT_WEBUI_PORT` / `SOCAT_WEBUI_DB` / `SOCAT_WEBUI_SOCAT` / `SOCAT_WEBUI_PASSWORD`）> 默认值。

## 验证

```bash
make verify-linux   # Docker systemd 容器内验证 install / 系统重启恢复（需 docker）
```

## 外部依赖

- socat：端口映射执行器，程序启动时自动检测；缺失时 WebUI 仍可访问并给出提示，代理启停操作被拒绝。
