# probe-platform · 自部署拨测系统

类似 [itdog.cn](https://www.itdog.cn) 的多节点网络拨测平台：在自己的服务器上部署 Dashboard，在腾讯云、亲戚朋友家里的 NAS（x86 / ARM）上跑 agent，从多个地域、多个运营商同时对目标做：

| 类型 | 内容 |
| --- | --- |
| **ICMP Ping** | 延迟 / 丢包 / 抖动，逐包实时回传 |
| **TCPing** | TCP 握手延迟、端口连通性 |
| **HTTP Ping** | 状态码、协议版本、TLS 与证书信息，DNS / TCP / TLS / 首字节 / 下载分阶段耗时，重定向链 |
| **MTR** | 原生 Go 实现（不依赖系统 `mtr`），多轮逐跳丢包与延迟，支持 IPv4 / IPv6，可用 ip2region 离线库标注每跳的地区与运营商 |

## 架构

```
 浏览器 ──HTTP/SSE──▶ probe-server（Go 单二进制，内嵌 Vue 前端，SQLite 存历史）
                          ▲ WebSocket（agent 主动出站连接，家里 NAT 后无需公网 IP）
        ┌─────────────────┼──────────────────┐
   腾讯云 agent      群晖 NAS agent      树莓派 / ARM NAS agent
```

- **agent 永远是出站连接**：通过一条 WebSocket 长连接接收任务、流式回传结果，断线自动重连。家用宽带没有公网 IP、路由器不用做端口映射。
- **纯 Go、无 cgo**：一条命令交叉编译出 `linux/amd64`、`arm64`、`armv7`、`mips` 等所有 NAS 常见架构；SQLite 用 modernc 纯 Go 驱动。
- **实时**：任务下发后每个 ping 包、每一轮 MTR 都会通过 SSE 推到浏览器，体验与 itdog 一致。

## 快速开始

### 1. 构建

需要 Go ≥ 1.26 与 Node ≥ 22（Go 会按 `go.mod` 自动下载合适的工具链）。

```bash
make build            # 构建前端 + bin/probe-server + bin/probe-agent（当前平台）
make agents-all       # 交叉编译所有架构的 agent 到 bin/
```

或直接用 Docker：

```bash
cp deploy/.env.example deploy/.env   # 填 PROBE_AGENT_TOKEN / PROBE_ADMIN_PASSWORD
docker compose -f deploy/docker-compose.yml up -d --build
```

### 2. 启动 server

```bash
PROBE_ADMIN_PASSWORD=你的密码 ./bin/probe-server --listen :8080 --data ./data
```

首次启动会自动生成 agent token 写入 `data/agent_token` 并打印在日志里。打开 <http://localhost:8080>。

生产环境建议放在 Caddy / nginx 后面加 HTTPS（示例见 `deploy/Caddyfile.example`、`deploy/nginx.conf.example`），并设置 `PROBE_TRUST_PROXY=true` 让 server 正确识别 agent 的公网 IP。

### 3. 接入 agent

**Docker（群晖 / QNAP / Unraid / 任意有 Docker 的机器）**

```bash
docker run -d --name probe-agent --restart unless-stopped \
  --network host --cap-add NET_RAW \
  -e PROBE_SERVER=https://probe.example.com \
  -e PROBE_TOKEN=<data/agent_token 的内容> \
  -e PROBE_NAME=home-shenzhen -e PROBE_LOCATION="广东 深圳" -e PROBE_ISP=电信 \
  ghcr.91856478.xyz/lochinemak/probe-agent:latest   # 镜像站；源站为 ghcr.io/lochinemak/probe-agent
```

`--network host` 让探测走宿主机真实网络栈；`--cap-add NET_RAW` 是 ICMP ping / MTR 需要的 raw socket 权限。

**二进制 + systemd（Linux）**

一条命令（脚本会安装二进制、写 `/etc/probe-agent.env`、装 unit 并启动；重复执行即升级）：

```bash
scp bin/probe-agent-linux-amd64 deploy/install-agent.sh user@nas:/tmp/
ssh user@nas "sudo sh -c 'PROBE_SERVER=https://probe.example.com PROBE_TOKEN=<token> PROBE_NAME=home-sz PROBE_LOCATION=\"广东 深圳\" PROBE_ISP=电信 sh /tmp/install-agent.sh /tmp/probe-agent-linux-amd64'"
```

或者手动：

```bash
sudo install -m755 bin/probe-agent-linux-arm64 /usr/local/bin/probe-agent
sudo setcap cap_net_raw+ep /usr/local/bin/probe-agent
sudo tee /etc/probe-agent.env >/dev/null <<'ENV'
PROBE_SERVER=https://probe.example.com
PROBE_TOKEN=<token>
PROBE_NAME=tencent-gz
PROBE_LOCATION=广东 广州
PROBE_ISP=腾讯云
ENV
sudo cp deploy/probe-agent.service /etc/systemd/system/
sudo systemctl enable --now probe-agent
```

**先本地验证探测能力**（不需要 server）：

```bash
probe-agent test www.qq.com         # 依次跑 ping / tcping / http / mtr 并打印 JSON
probe-agent test mtr 1.1.1.1
```

节点页会显示每个 agent 检测到的能力（`icmp_raw` / `mtr` / `ipv6`）。没有 raw socket 权限时 MTR 不可用，ping 会退回到非特权 ICMP（Linux 需要 `net.ipv4.ping_group_range` 覆盖运行用户的 gid）。

### 4. 可选：MTR 每跳地区标注

```bash
make geoip-db     # 下载 ip2region 离线库到 data/ip2region.xdb，重启 server 生效
```

之后 MTR 每一跳会显示「中国 广东省 深圳市 电信」这类标签。agent 自身的公网 IP 默认通过 ip-api.com 在线识别（可用 `PROBE_GEOIP_ONLINE=false` 关闭）。

## 生产部署与 CI/CD（当前实例）

- **Dashboard**：`https://probe.geneyuriy.com`，腾讯云主机上以 Docker 运行（`/opt/probe-platform`，文件见 `deploy/prod/`），OpenResty 在宿主机做 TLS 终止与反代，证书由 acme.sh 签发并通过 `--install-cert ... --reloadcmd "systemctl reload openresty"` 自动续期。
- **持续部署**：推送到 `main` → GitHub Actions 构建并推送 `ghcr.io/lochinemak/probe-server` 与 `probe-agent` 镜像 → 通过一把只允许执行 `deploy.sh` 的受限 SSH 密钥通知主机 → 主机经镜像站 `ghcr.91856478.xyz` 拉取该 commit 的 `sha-xxxxxxx` 标签并重启。回滚：在主机上执行 `/opt/probe-platform/deploy.sh sha-<旧commit>`。
- **发布**：打 `v*` 标签会额外产出各架构的二进制到 GitHub Releases。
- 需要的仓库 secrets：`DEPLOY_HOST`、`DEPLOY_PORT`、`DEPLOY_USER`、`DEPLOY_SSH_KEY`。

## 配置

server（flag 或环境变量）：

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `PROBE_LISTEN` | `:8080` | 监听地址 |
| `PROBE_DATA_DIR` | `./data` | SQLite、token、ip2region 库所在目录 |
| `PROBE_AGENT_TOKEN` | 自动生成 | agent 共享密钥 |
| `PROBE_ADMIN_PASSWORD` | 空（无登录） | Dashboard 密码，公网部署务必设置 |
| `PROBE_TRUST_PROXY` | `false` | 反向代理后设为 `true` |
| `PROBE_GEOIP_ONLINE` | `true` | 用 ip-api.com 识别 agent 位置 |
| `PROBE_IP2REGION_DB` | `<data>/ip2region.xdb` | 离线 IP 库路径 |
| `PROBE_TASK_TIMEOUT` | `180` | 任务整体超时（秒） |
| `PROBE_RETAIN_DAYS` | `90` | 历史保留天数，0 为永久 |

agent：`PROBE_SERVER`、`PROBE_TOKEN`、`PROBE_NAME`（默认主机名，作为节点唯一 ID）、`PROBE_LOCATION`、`PROBE_ISP`、`PROBE_TAGS`、`PROBE_CONCURRENCY`（默认 4）、`PROBE_INSECURE`（跳过 TLS 校验）。

## API

所有接口返回 JSON；设置了密码时需先 `POST /api/login {"password": "..."}` 获取 Cookie。

```bash
# 对所有在线节点做 tcping
curl -s -X POST localhost:8080/api/tasks -H 'content-type: application/json' \
  -d '{"type":"tcping","target":"www.qq.com:443","params":{"count":5}}'
# 指定节点
curl -s -X POST localhost:8080/api/tasks -d '{"type":"mtr","target":"1.1.1.1","agent_ids":["home-shenzhen"]}'
# 实时结果（SSE：snapshot → progress* → result* → done）
curl -N localhost:8080/api/tasks/<id>/events
# 历史
curl localhost:8080/api/tasks?limit=20
curl localhost:8080/api/tasks/<id>
curl localhost:8080/api/agents
```

任务参数（`params`）：

| 字段 | 适用 | 说明 |
| --- | --- | --- |
| `count` | 全部 | ping/tcping 次数、HTTP 请求次数、MTR 轮数 |
| `interval_ms` / `timeout_ms` | 全部 | 间隔与单次超时 |
| `ip_version` | 全部 | `""` 自动（优先 v4）、`"4"`、`"6"` |
| `packet_size` | ping | 负载大小，默认 56 |
| `port` | tcping | 目标里没写端口时使用，默认 80 |
| `method` / `headers` / `body` / `follow_redirects` / `insecure_tls` | http | |
| `max_hops` / `resolve` | mtr | 最大跳数（默认 30）、是否反向解析 |

## 项目结构

```
cmd/server, cmd/agent      入口
internal/protocol          server / agent / 前端共享的消息与结果类型
internal/probe             ping（pro-bing）、tcping、http（httptrace）、mtr（原生 ICMP）
internal/agent             WebSocket 客户端、任务执行、重连
internal/server            hub（连接与任务调度）、SQLite 存储、HTTP/SSE API、鉴权、GeoIP
web/                       Vue 3 + Vite 前端，构建后由 server embed
deploy/                    Dockerfile、compose、systemd、反向代理示例
```

开发时：`make run-server` 起后端，`cd web && npm run dev` 起前端（已配置代理），`make run-agent` 在本机接一个 agent。

## 安全说明

- Dashboard 能让任意在线节点向任意目标发包，**公网部署必须设置 `PROBE_ADMIN_PASSWORD` 并使用 HTTPS**。
- agent 只接受 server 下发的四种探测类型，参数有上限（次数 ≤ 100、跳数 ≤ 64、HTTP 响应最多读 8 MiB）。
- token 泄露后重新生成：删除 `data/agent_token` 或改 `PROBE_AGENT_TOKEN`，然后更新各 agent。
