#!/bin/sh
# socat-webui Linux systemd 验证（T-009）：
# install 注册 → 登录创建代理 → docker restart 模拟系统重启 → 断言代理自动恢复。
# 前置：docker 可用；脚本自行完成 linux/amd64 交叉编译。
set -eu
cd "$(dirname "$0")/.."

IMAGE=socat-webui-verify:latest
CTR=socat-webui-verify-run
PORT=15200
MAPPED=25200
PASSWORD=verify-pw-15200

step() { echo "\n===== $* ====="; }

# 轮询直到 /api/health 返回 401（HTTP 已就绪且认证生效）
wait_http() {
    for i in $(seq 1 30); do
        code=$(docker exec "$CTR" curl -s -o /dev/null -w '%{http_code}' --max-time 2 \
            "http://127.0.0.1:$PORT/api/health" 2>/dev/null || echo 000)
        [ "$code" = "401" ] && return 0
        sleep 1
    done
    echo "等待服务 HTTP 就绪超时"; return 1
}

step "1/5 构建 linux/amd64 二进制"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o bin/linux_amd64/socat-webui ./cmd/socat-webui

step "2/5 构建验证镜像（debian + systemd + socat）"
docker build -t "$IMAGE" -f scripts/Dockerfile.verify .

step "3/5 安装服务并创建代理"
docker rm -f "$CTR" >/dev/null 2>&1 || true
docker run -d --name "$CTR" --privileged "$IMAGE" >/dev/null
for i in $(seq 1 30); do
    docker exec "$CTR" systemctl is-system-running >/dev/null 2>&1 && break
    sleep 1
done

docker exec "$CTR" /usr/local/bin/socat-webui install --listen 0.0.0.0 --port "$PORT" --password "$PASSWORD"
docker exec "$CTR" systemctl is-active socat-webui
wait_http

TOKEN=$(docker exec "$CTR" curl -sf -X POST "http://127.0.0.1:$PORT/api/auth/login" \
    -H 'Content-Type: application/json' -d "{\"password\":\"$PASSWORD\"}" \
    | sed -E 's/.*"token":"([^"]+)".*/\1/')
[ -n "$TOKEN" ] || { echo "登录失败"; exit 1; }

# 远端指向 WebUI 自身端口（容器内常驻可达），经代理访问应返回 401 JSON
docker exec "$CTR" curl -sf -X POST "http://127.0.0.1:$PORT/api/proxies" \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -d "{\"name\":\"verify\",\"localPort\":$MAPPED,\"remoteHost\":\"127.0.0.1\",\"remotePort\":$PORT}" >/dev/null
sleep 1
FIRST=$(docker exec "$CTR" curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$MAPPED/api/health")
[ "$FIRST" = "401" ] || { echo "经代理访问预期 401，实际 $FIRST"; exit 1; }
echo "代理转发正常（经 ${MAPPED} 返回 ${FIRST}）"

step "4/5 docker restart 模拟系统重启"
docker restart "$CTR" >/dev/null
wait_http

step "5/5 验证重启后自动恢复"
docker exec "$CTR" systemctl is-active socat-webui

TOKEN=$(docker exec "$CTR" curl -sf -X POST "http://127.0.0.1:$PORT/api/auth/login" \
    -H 'Content-Type: application/json' -d "{\"password\":\"$PASSWORD\"}" \
    | sed -E 's/.*"token":"([^"]+)".*/\1/')
LIST=$(docker exec "$CTR" curl -sf "http://127.0.0.1:$PORT/api/proxies" -H "Authorization: Bearer $TOKEN")
echo "$LIST" | grep -q "\"status\":\"running\"" || { echo "重启后代理未恢复: $LIST"; exit 1; }

RESTARTED=$(docker exec "$CTR" curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$MAPPED/api/health")
[ "$RESTARTED" = "401" ] || { echo "重启后经代理访问预期 401，实际 $RESTARTED"; exit 1; }

echo "$LIST"
docker rm -f "$CTR" >/dev/null
echo "T-009 验证通过：install 注册 / 系统重启后代理自动恢复"
