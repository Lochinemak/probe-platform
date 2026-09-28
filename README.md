# probe-platform · 自部署拨测系统

类似 [itdog.cn](https://www.itdog.cn) 的多节点网络拨测平台：在自己的服务器上部署 Dashboard，在腾讯云、亲戚朋友家里的 NAS（x86 / ARM）上跑 agent，从多个地域、多个运营商同时对目标做：

| 类型 | 内容 |
| --- | --- |
| **ICMP Ping** | 延迟 / 丢包 / 抖动，逐包实时回传 |
| **TCPing** | TCP 握手延迟、端口连通性 |
| **HTTP Ping** | 状态码、协议版本、TLS 与证书信息，DNS / TCP / TLS / 首字节 / 下载分阶段耗时，带状态码的跳转链；自定义方法 / Header / Body；状态码、关键字、耗时断言；按时间窗口的下载测速 |
| **MTR** | 原生 Go 实现（不依赖系统 `mtr`），ICMP / TCP SYN / UDP 三种探针，多轮逐跳丢包与延迟，支持 IPv4 / IPv6，可用 ip2region 离线库标注每跳的地区与运营商 |
| **DNS** | 各节点解析对比：A / AAAA / CNAME / MX / TXT / NS / PTR / SOA / SRV，记录与 TTL、RCode、耗时，可指定 DNS 服务器（UDP / TCP / DoH），fake-IP 应答会被标出 |
| **定时监控 + 告警** | 按 30 秒到 24 小时的间隔持续拨测，结果入库并绘制各节点延迟 / 丢包趋势图；阈值 + 连续失败次数触发告警，恢复时再通知；Telegram / 企业微信 / 钉钉 / Bark / PushDeer / Gotify / Webhook / 邮件 |

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

**Linux + systemd 一条命令**（推荐，之后随 dashboard 自动更新）：

```bash
export PROBE_SERVER=https://probe.example.com
export PROBE_TOKEN=<data/agent_token 的内容>
export PROBE_NAME=home-shenzhen
export PROBE_LOCATION="广东 深圳"
export PROBE_ISP=电信
curl -fsSL $PROBE_SERVER/install-agent.sh | sudo -E sh
```

Dashboard 的「节点」页会按你的服务器地址、token 和填写的节点名生成可直接复制的版本。

脚本会按 CPU 架构从 dashboard 下载对应二进制（amd64 / arm64 / armv7 / armv6 / 386 / riscv64 / mipsle / mips / mips64le），装到 `/var/lib/probe-agent/`（归专用的 `probe-agent` 系统用户所有），写 `/etc/probe-agent.env`，安装 systemd unit（非 root 运行，`AmbientCapabilities=CAP_NET_RAW` 提供 ICMP / MTR 所需的 raw socket）。重复执行即升级或改配置。

**OpenWrt / iStoreOS 路由器**（procd，无 systemd，以 root 运行）：

```bash
export PROBE_SERVER=https://probe.example.com
export PROBE_TOKEN=<token>
export PROBE_NAME=home-router
export PROBE_LOCATION="广东 深圳"
export PROBE_ISP=电信
curl -fsSL $PROBE_SERVER/install-agent-openwrt.sh | sh
```

- 占用约 8 MB overlay 空间、10 MB 内存；128 MB 闪存 / 512 MB 内存的机器绰绰有余，16 MB 闪存的老路由不建议。
- 架构按 `uname -m` 自动选择：`aarch64`（MT7981 / MT7986 Filogic、RK3568 等）用 arm64；`mips`（MT7621 等）用 32 位 MIPS 软浮点构建，脚本会从 `/bin/sh` 的 ELF 头判断大小端。x86 软路由用 amd64。
- 装好后 `logread -e probe-agent` 看日志，`/etc/init.d/probe-agent restart` 重启，配置在 `/etc/probe-agent.env`。自更新同样有效。
- 如果这台路由器自己就在跑 OpenClash，它本机的 DNS 也是 fake-IP，域名类探测会失真；这种情况下把节点装在路由器后面的机器上更合适，或者用旁路由 / 二级 AP 上的 iStoreOS 做节点。

**群晖 / 威联通 NAS 用 Docker**：`deploy/nas/synology-dsm.compose.yml`、`deploy/nas/qnap-container-station.compose.yml` 是可直接粘贴到 Container Manager「项目」/ Container Station「应用程序」的样例，注释里说明了每个特殊参数；Dashboard 节点页也会生成填好地址、token 和节点名的版本。要点只有三个：`network_mode: host`（走 NAS 真实网络栈）、`cap_add: [NET_RAW]`（ICMP / MTR 需要，比「特权模式」安全）、镜像走大陆可达的镜像站。容器不自更新，升级靠重新拉镜像，样例里附了可选的 Watchtower 配置。

**Docker**（Unraid / 任意有 Docker 的机器）：

```bash
export PROBE_SERVER=https://probe.example.com
export PROBE_TOKEN=<data/agent_token 的内容>
export PROBE_NAME=home-shenzhen
export PROBE_LOCATION="广东 深圳"
export PROBE_ISP=电信
docker run -d --name probe-agent --restart unless-stopped \
  --network host --cap-add NET_RAW \
  -e PROBE_SERVER -e PROBE_TOKEN -e PROBE_NAME -e PROBE_LOCATION -e PROBE_ISP \
  ghcr.91856478.xyz/lochinemak/probe-agent:latest   # 镜像站；源站为 ghcr.io/lochinemak/probe-agent
```

`--network host` 让探测走宿主机真实网络栈；`--cap-add NET_RAW` 是 ICMP ping / MTR 需要的 raw socket 权限。容器里默认关闭自更新，升级靠拉新镜像（可以用 Watchtower 自动做）。

**先本地验证探测能力**（不需要 server）：

```bash
probe-agent test www.qq.com         # 依次跑 ping / tcping / http / mtr 并打印 JSON
probe-agent test mtr 1.1.1.1 protocol=tcp port=443   # 参数用 key=value 追加
probe-agent test dns www.qq.com dns_server=223.5.5.5
sudo /var/lib/probe-agent/probe-agent test www.qq.com   # 已用 systemd 安装的机器
```

节点页会显示每个 agent 检测到的能力（`icmp_raw` / `mtr` / `ipv6`）。没有 raw socket 权限时 MTR 不可用，ping 会退回到非特权 ICMP（Linux 需要 `net.ipv4.ping_group_range` 覆盖运行用户的 gid）。

### 3.1 agent 自动更新

server 镜像里自带所有平台的 agent 二进制（`PROBE_AGENTS_DIR`，Docker 镜像默认 `/usr/share/probe-platform/agents`）。流程：

1. agent 连上时上报自己的版本；server 发现与自身版本不一致，且有该平台的二进制，就下发 `update` 消息。
2. agent 随机等待 0～20 秒（避免全网节点同时下载），从 `/api/agent/download/<os>-<arch>` 下载到二进制所在目录，校验 sha256 和大小，运行一次 `version` 确认新文件能执行并且版本正确。
3. 等当前任务跑完（最多 60 秒），把旧文件改名为 `probe-agent.prev`，新文件原子替换，然后原地 `exec` 重启（PID 不变，systemd 无感知，ambient capabilities 保留）。
4. 失败会记录日志并在 10 分钟内不再重试；旧版本继续工作。

所以 dashboard 每次通过 CI/CD 更新后，server 重启导致所有 agent 重连，几十秒内全网节点自动跟上。语义是「与 server 版本保持一致」，回滚 server 时 agent 也会跟着回滚。

- 只有真实构建（版本号不是 `dev`）之间才会触发。
- `PROBE_SELF_UPDATE=false` 关闭；Docker 镜像里默认关闭。
- 早期用 `/usr/local/bin` + setcap 方式安装的节点重新跑一次安装脚本即可迁移到新布局。

### 4. 可选：MTR 每跳地区标注

```bash
make geoip-db     # 下载 ip2region 离线库到 data/ip2region.xdb，重启 server 生效
```

之后 MTR 每一跳会显示「中国 广东省 深圳市 电信」这类标签。agent 自身的公网 IP 默认通过 ip-api.com 在线识别（可用 `PROBE_GEOIP_ONLINE=false` 关闭）。

## 定时监控与告警

「监控」页新建一个监控：类型、目标、间隔（30 秒 ～ 24 小时）、节点（不选则每次用全部在线节点）、探测参数、告警规则和通知渠道。之后：

- 调度器每 5 秒检查一次到期的监控，像手动拨测一样下发任务；每个节点的结果被压缩成一个样本（延迟、丢包 / 失败率、是否成功、状态码等）写入 `samples` 表。
- 详情页按 1 小时 ～ 30 天查看各节点的延迟和丢包曲线（超过 6 小时按 5 分钟 / 30 分钟 / 2 小时聚合），点图例可隐藏节点；下面是每个节点的当前状态、告警记录和最近运行（可点开看完整结果）。
- **失败的定义**：探测出错、ping/tcping 全部丢包、HTTP 断言未通过（状态码 / 关键字 / 耗时）、MTR 未到达目标、DNS 非 NOERROR。在此之上可以再设「丢包 ≥ x%」「延迟 ≥ y ms」。
- **告警**：某节点连续 N 次失败（默认 2）触发一次「异常」通知，之后不再重复；恢复正常时发一次「恢复」通知并带上持续时长。每个 (监控, 节点) 独立判断。
- **通知渠道**（「通知」页）：Telegram 机器人、企业微信群机器人、钉钉群机器人（支持加签）、Bark、PushDeer（官方或自建服务器，填 PushKey）、Gotify（自建服务器 + 应用 Token，可设优先级）、通用 Webhook（POST JSON `{title, text, at}`，可加认证头）、SMTP 邮件（465 SSL 或 587 STARTTLS）。保存后可发送测试消息。
- 数据保留：样本随 `PROBE_RETAIN_DAYS`（默认 90 天）；每次运行的完整结果只保留 `PROBE_MONITOR_TASK_RETAIN_HOURS`（默认 48 小时），避免数据库膨胀。设置 `PROBE_BASE_URL` 后通知里会带详情链接。
- 监控产生的运行默认不出现在「历史」页，勾选「含定时监控的运行」可查看。

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
| `PROBE_ADMIN_USER` / `PROBE_ADMIN_PASSWORD` | `admin` / 空 | 引导用的管理员用户名和密码；之后在「设置」页修改 |
| `PROBE_GUEST` | `true` | 引导值：允许游客发起拨测、查看结果（「设置」页可改） |
| `PROBE_LOGTO_ENDPOINT` / `PROBE_LOGTO_APP_ID` / `PROBE_LOGTO_APP_SECRET` / `PROBE_LOGTO_ADMINS` | 空 | 引导值，建议直接在「设置」页配置 |
| `PROBE_TRUST_PROXY` | `false` | 反向代理后设为 `true` |
| `PROBE_GEOIP_ONLINE` | `true` | 用 ip-api.com 识别 agent 位置 |
| `PROBE_IP2REGION_DB` | `<data>/ip2region.xdb` | 离线 IP 库路径 |
| `PROBE_TASK_TIMEOUT` | `180` | 任务整体超时（秒） |
| `PROBE_RETAIN_DAYS` | `90` | 历史保留天数，0 为永久 |
| `PROBE_BASE_URL` | 空 | 引导值：站点公网地址，用于告警链接和 Logto 回调（「设置 → 常规」可改） |
| `PROBE_MONITOR_TASK_RETAIN_HOURS` | `48` | 定时监控每次运行的完整结果保留时长（小时） |
| `PROBE_AGENTS_DIR` | 空（Docker 镜像内已设） | 存放 `probe-agent-<os>-<arch>` 二进制的目录，用于 agent 自更新与安装脚本下载 |
| `PROBE_AGENT_IMAGE` | `ghcr.io/lochinemak/probe-agent:latest` | 节点页展示的 Docker 镜像名（大陆可填镜像站地址） |

agent：`PROBE_SERVER`、`PROBE_TOKEN`、`PROBE_NAME`（默认主机名，作为节点唯一 ID）、`PROBE_LOCATION`、`PROBE_ISP`、`PROBE_TAGS`、`PROBE_CONCURRENCY`（默认 4）、`PROBE_INSECURE`（跳过 TLS 校验）、`PROBE_SELF_UPDATE`（默认 `true`）。

## API

所有接口返回 JSON；管理员接口需先 `POST /api/login {"username": "admin", "password": "..."}` 获取 Cookie。游客可直接调用拨测相关接口，但 `GET /api/tasks`、`GET /api/tasks/{id}`（及 `/events`、`/cancel`）只对自己发起的任务有效：第一次 `POST /api/tasks` 会下发一枚 `probe_guest` Cookie，之后带着它请求才能看到这些记录。

```bash
# 对所有在线节点做 tcping
curl -s -X POST localhost:8080/api/tasks -H 'content-type: application/json' \
  -d '{"type":"tcping","target":"www.qq.com:443","params":{"count":5}}'
# 指定节点，TCP 模式的 MTR
curl -s -X POST localhost:8080/api/tasks -d '{"type":"mtr","target":"1.1.1.1","params":{"protocol":"tcp","port":443},"agent_ids":["home-shenzhen"]}'
# DNS 对比
curl -s -X POST localhost:8080/api/tasks -d '{"type":"dns","target":"www.qq.com","params":{"dns_server":"223.5.5.5"}}'
# 定时监控与通知渠道
curl -s localhost:8080/api/monitors; curl -s localhost:8080/api/notify; curl -s localhost:8080/api/alerts
curl -s "localhost:8080/api/monitors/<id>/series?range=24h'
# 实时结果（SSE：snapshot → progress* → result* → done）
curl -N localhost:8080/api/tasks/<id>/events
# 历史
curl localhost:8080/api/tasks?limit=20
curl localhost:8080/api/tasks/<id>
curl localhost:8080/api/agents
# server 自带的 agent 二进制（agent token 或登录态均可访问）
curl -H "Authorization: Bearer <agent token>" localhost:8080/api/agent/version
curl -H "Authorization: Bearer <agent token>" localhost:8080/api/agent/download/linux-arm64 -o probe-agent
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
| `protocol` / `port` | mtr | `icmp`（默认）、`tcp`（SYN，默认 80 端口）、`udp`（默认 33434） |
| `expect_status` / `expect_keyword` / `expect_max_ms` | http | 断言：状态码（0 = 任意 < 400）、响应体关键字、总耗时上限 |
| `speed_test` / `speed_seconds` | http | 下载测速：持续读取指定秒数（默认 5）并计算 Mbps |
| `record_type` / `dns_server` | dns | 记录类型（默认 A）；服务器留空用节点系统 DNS，可填 `223.5.5.5`、`223.5.5.5:53`、`tcp://1.1.1.1`、`https://doh.pub/dns-query` |

## 项目结构

```
cmd/server, cmd/agent      入口
internal/protocol          server / agent / 前端共享的消息与结果类型
internal/probe             ping（pro-bing）、tcping、http（httptrace + 断言 + 测速）、mtr（ICMP / TCP / UDP 引擎）、dns
internal/server            另含 scheduler（定时监控与告警状态机）、notify（八种通知渠道）
internal/agent             WebSocket 客户端、任务执行、重连
internal/server            hub（连接与任务调度）、SQLite 存储、HTTP/SSE API、鉴权、GeoIP
web/                       Vue 3 + Vite 前端，构建后由 server embed
deploy/                    Dockerfile、compose、systemd、安装脚本、反向代理示例；deploy/prod 为当前生产实例的文件
```

开发时：`make run-server` 起后端，`cd web && npm run dev` 起前端（已配置代理），`make run-agent` 在本机接一个 agent。

## 在 OpenClash（fake-IP）后面的节点

家里路由器跑 OpenClash 且 DNS 为 fake-IP 模式时，节点上的域名会解析成 `198.18.x.x`，表现为：ping 域名 100% 丢包、HTTP 与 TCPing 走的是 Clash 代理链路而非真实家宽、MTR 到域名没有意义。直接填 IP 的探测基本不受影响（国内 IP 走 DIRECT）。

拨测节点的意义就是测真实网络，所以建议把节点整机绕开代理，两步缺一不可：

1. **让流量不进 Clash**：OpenClash → 插件设置 → 流量控制 → 「局域网访问控制」改为黑名单模式，把节点的内网 IP（或 MAC）加入黑名单。保存并重启 OpenClash 后，这台机器的 TCP/UDP 不再被 iptables 转给 Clash，境外目标也走真实家宽。
2. **让 DNS 不问路由器**：路由器的 dnsmasq 已经把查询交给了 Clash 的 fake-IP DNS，节点只要还用路由器做 DNS 就仍会拿到假 IP。在节点上把 DNS 改为上游公共 DNS，例如 `223.5.5.5`、`119.29.29.29`。Debian 上改 `/etc/systemd/resolved.conf` 的 `DNS=`（或 `/etc/network/interfaces` / `dhclient.conf` 里的 `supersede domain-name-servers`），群晖在「控制面板 → 网络 → 常规」里手动指定 DNS。

验证：在节点上 `getent hosts www.qq.com` 应返回真实公网 IP 而不是 `198.18.` 开头；dashboard 里对该节点 ping 一个域名不再全丢。

不推荐的做法：把 OpenClash 整体切到 redir-host 模式（影响全家上网体验）、维护 fake-ip-filter 白名单（对任意拨测目标不可维护）、只改 DNS 不加黑名单（OpenClash 通常劫持所有 53 端口流量，换了上游也会被截回来；而且境外目标仍被透明代理）。

## 访问控制：游客与管理员

登录相关的配置都在 Dashboard 的「设置」页（管理员可见）修改，保存后立即生效、无需重启；环境变量只作为首次启动的引导值，页面里保存过的项以数据库为准。

- **未配置任何登录方式**（既没有密码也没有 Logto）：开放模式，所有人都是管理员，只适合内网。首次部署请用 `PROBE_ADMIN_PASSWORD` 提供一个引导密码，登录后在「设置 → 管理员密码」里改成自己的（保存为 bcrypt 哈希，之后环境变量里的密码就不再起作用，可以删掉）。
- **配置了登录方式**：匿名访客是**游客**，可以发起拨测、看实时结果，节点列表只显示名称、位置、运营商、能力；看不到公网 IP、版本、接入命令，也不能进「监控」「通知」「设置」页。「历史」页只显示**本浏览器**发起过的拨测：游客第一次发起拨测时会得到一枚一年有效的匿名 Cookie（`probe_guest`）作为身份，换浏览器、清 Cookie 或者知道任务 ID 也看不到别人的记录。游客发起拨测有限制：每 IP 每分钟 10 次，ping/tcping 最多 20 次、HTTP 3 次、MTR 10 轮、DNS 5 次，不能用下载测速。「设置 → 访问控制」可以关闭游客访问，届时所有功能都需要登录。
- **管理员**：右上角「管理员登录」。登录后「历史」页显示所有人的记录，并多一列「来源」（管理员用户名 / 游客 + 短 ID / 定时监控）。两种方式：
  - 用户名 + 密码：用户名在「设置」里改（默认 `admin`），密码在「设置 → 管理员密码」里改。改密码会让所有会话失效。
  - Logto（OIDC 授权码 + PKCE，服务端换取令牌并校验 ID Token）：在「设置 → Logto 登录」填 Logto 地址（如 `https://auth.example.com`，自动加 `/oidc`）、App ID、App Secret（Traditional Web 应用必填，Single Page App 留空）和可选的管理员名单（逗号分隔的邮箱 / 用户名 / sub，留空表示该 Logto 的任何用户都是管理员），并在「常规」里填站点地址。页面会显示需要登记到 Logto 应用「Redirect URIs」的回调地址 `<站点地址>/api/auth/logto/callback`，「测试连接」按钮会做一次 OIDC 发现。密钥只写不读。
- 对应的引导环境变量：`PROBE_ADMIN_USER`、`PROBE_ADMIN_PASSWORD`、`PROBE_GUEST`、`PROBE_LOGTO_ENDPOINT`、`PROBE_LOGTO_APP_ID`、`PROBE_LOGTO_APP_SECRET`、`PROBE_LOGTO_ADMINS`、`PROBE_BASE_URL`、`PROBE_AGENT_IMAGE`。
- 会话是 HMAC 签名的 Cookie（密钥随机生成并保存在数据库），30 天有效。

## 安全说明

- Dashboard 能让任意在线节点向任意目标发包，**公网部署必须配置管理员登录并使用 HTTPS**；开放游客访问时请评估被滥用的风险（已有频率与参数限制，仍可 `PROBE_GUEST=false` 关闭）。
- agent 只接受 server 下发的四种探测类型，参数有上限（次数 ≤ 100、跳数 ≤ 64、HTTP 响应最多读 8 MiB）。
- **所有 agent 目前共用一个 token**（`PROBE_AGENT_TOKEN`）。它只能让持有者以任意名字注册成节点、接收任务、下载 agent 二进制，不能登录 Dashboard 或查看历史。泄露后轮换：改 `PROBE_AGENT_TOKEN`（生产实例在 `/opt/probe-platform/.env`）并重启 server，然后逐台改 `/etc/probe-agent.env`（或容器环境变量）再重启 agent；自更新机制不负责分发 token。若节点分散在多个不完全信任的地方，可以考虑改为每节点独立 token 并在 Dashboard 上签发 / 吊销，目前尚未实现。
