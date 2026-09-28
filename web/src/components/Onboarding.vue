<script setup>
import { ref, computed, watch } from 'vue'
import { api } from '../api.js'

// Onboarding snippets. Every node has its own token, so commands exist only
// for a node: either one just created here, or an existing one picked from the
// list (`node`). Variables come first (export ...) so a copied command can be
// edited at the top; the values are driven by the inputs below.
const props = defineProps({
  agentImage: { type: String, default: 'ghcr.io/lochinemak/probe-agent:latest' },
  serverVersion: { type: String, default: '' },
  downloads: { type: Array, default: () => [] },
  node: { type: Object, default: null }, // existing node whose install command to show
})
const emit = defineEmits(['changed', 'clear'])
const serverURL = `${location.protocol}//${location.host}`
const nodeId = ref('')
const token = ref('')
const showToken = ref(false)
const name = ref('home-shenzhen')
const loc = ref('广东 深圳')
const isp = ref('电信')
const copiedKey = ref('')
const busy = ref(false)
const error = ref('')

watch(() => props.node, async (n) => {
  error.value = ''
  showToken.value = false
  if (!n) return
  nodeId.value = n.id
  name.value = n.name || n.id
  loc.value = n.location || ''
  isp.value = n.isp || ''
  token.value = ''
  try { token.value = (await api.agentToken(n.id)).token || '' } catch (e) { error.value = e.message }
}, { immediate: true })

async function create() {
  error.value = ''
  busy.value = true
  try {
    const r = await api.createAgent({ name: name.value, location: loc.value, isp: isp.value })
    nodeId.value = r.agent.id
    token.value = r.token
    emit('changed')
  } catch (e) { error.value = e.message } finally { busy.value = false }
}
async function resetToken() {
  if (!confirm(`重置节点 ${name.value} 的 token？\n\n旧 token 立即失效：正在运行的 agent 会被断开，直到用新命令重新安装（覆盖安装）。适用于 token 泄露或机器易手。`)) return
  error.value = ''
  busy.value = true
  try { token.value = (await api.resetAgentToken(nodeId.value)).token; showToken.value = false; emit('changed') } catch (e) { error.value = e.message } finally { busy.value = false }
}
function startOver() {
  nodeId.value = ''
  token.value = ''
  error.value = ''
  name.value = loc.value = isp.value = ''
  emit('clear')
}

const maskedToken = computed(() => showToken.value ? token.value : '••••••••••••' + token.value.slice(-4))
function q(s) { s = String(s ?? ''); return /[\s"'$`\\]/.test(s) ? '"' + s.replace(/(["\\$`])/g, '\\$1') + '"' : (s || '""') }
function yq(s) { return JSON.stringify(String(s ?? '')) }
function pq(s) { return "'" + String(s ?? '').replace(/'/g, "''") + "'" } // PowerShell single-quoted literal
const tok = (real) => real ? token.value : maskedToken.value

function exportsBlock(real) {
  return [
    `export PROBE_SERVER=${serverURL}`,
    `export PROBE_TOKEN=${tok(real)}`,
    `export PROBE_NAME=${q(name.value)}`,
    `export PROBE_LOCATION=${q(loc.value)}`,
    `export PROBE_ISP=${q(isp.value)}`,
  ].join('\n')
}
function powershellBlock(real) {
  return [
    `$env:PROBE_SERVER=${pq(serverURL)}`,
    `$env:PROBE_TOKEN=${pq(tok(real))}`,
    `$env:PROBE_NAME=${pq(name.value)}`,
    `$env:PROBE_LOCATION=${pq(loc.value)}`,
    `$env:PROBE_ISP=${pq(isp.value)}`,
    'irm "$env:PROBE_SERVER/install-agent.ps1" | iex',
  ].join('\n')
}
function composeYaml(real, platform) {
  const head = platform === 'synology'
    ? '# 群晖 DSM 7.2+：Container Manager → 项目 → 新增 → 来源选「创建 docker-compose.yml」→ 粘贴\n'
    : '# 威联通：Container Station → 应用程序 → 创建 → 粘贴 YAML（旧版 Container Station 2 需要 version 字段）\nversion: "3.8"\n'
  return head +
`services:
  probe-agent:
    image: ${props.agentImage}
    container_name: probe-agent
    restart: unless-stopped
    network_mode: host        # 走 NAS 真实网络栈，不要用桥接
    cap_add:
      - NET_RAW               # ICMP ping / MTR 需要，不必开「特权模式」
    environment:
      PROBE_SERVER: ${serverURL}
      PROBE_TOKEN: ${yq(tok(real))}   # 本节点专属 token
      PROBE_NAME: ${yq(name.value)}
      PROBE_LOCATION: ${yq(loc.value)}
      PROBE_ISP: ${yq(isp.value)}
      TZ: Asia/Shanghai
    logging:
      driver: json-file
      options: { max-size: "10m", max-file: "3" }`
}
const snippets = computed(() => [
  { key: 'systemd', title: 'Linux + systemd（推荐，装完随 dashboard 自动更新）', open: true,
    text: (real) => exportsBlock(real) + '\ncurl -fsSL $PROBE_SERVER/install-agent.sh | sudo -E sh' },
  { key: 'openwrt', title: 'OpenWrt / iStoreOS 路由器（procd，root 直接执行）',
    text: (real) => exportsBlock(real) + '\ncurl -fsSL $PROBE_SERVER/install-agent-openwrt.sh | sh' },
  { key: 'windows', title: 'Windows 10 / 11 电脑（以管理员身份打开 PowerShell 粘贴；装成系统服务，开机自启、自动更新）',
    text: (real) => powershellBlock(real) },
  { key: 'docker', title: 'Docker（Unraid / 任意 Linux；升级靠拉新镜像；覆盖安装会先删掉旧容器）',
    text: (real) => exportsBlock(real) + `\ndocker rm -f probe-agent 2>/dev/null
docker run -d --name probe-agent --restart unless-stopped \\
  --network host --cap-add NET_RAW \\
  -e PROBE_SERVER -e PROBE_TOKEN -e PROBE_NAME -e PROBE_LOCATION -e PROBE_ISP \\
  ${props.agentImage}` },
  { key: 'synology', title: '群晖 DSM · Container Manager 项目（docker-compose；覆盖安装：改好后「构建」重建项目）', text: (real) => composeYaml(real, 'synology') },
  { key: 'qnap', title: '威联通 · Container Station 应用程序（docker-compose）', text: (real) => composeYaml(real, 'qnap') },
  { key: 'manual', title: '手动：先下载二进制再安装',
    text: (real) => exportsBlock(real) + `\ncurl -fsSL -H "Authorization: Bearer $PROBE_TOKEN" $PROBE_SERVER/api/agent/download/linux-arm64 -o probe-agent
curl -fsSL $PROBE_SERVER/install-agent.sh -o install-agent.sh
sudo -E sh install-agent.sh ./probe-agent` },
])
const uninstall = {
  key: 'uninstall', title: '卸载节点（脚本自动识别 systemd / OpenWrt；只清理那台机器，要让 token 失效请在节点列表删除节点）',
  text: () => `# Linux（systemd）
curl -fsSL ${serverURL}/uninstall-agent.sh | sudo sh
# OpenWrt / iStoreOS（已是 root）
curl -fsSL ${serverURL}/uninstall-agent.sh | sh
# Windows（管理员 PowerShell）
$env:PROBE_UNINSTALL='1'; irm ${pq(serverURL + '/install-agent.ps1')} | iex
# Docker
docker rm -f probe-agent`,
}

async function copy(s) {
  const text = s.text(true)
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    const ta = document.createElement('textarea'); ta.value = text; document.body.appendChild(ta); ta.select()
    try { document.execCommand('copy') } catch { /* ignore */ } finally { ta.remove() }
  }
  copiedKey.value = s.key
  setTimeout(() => { if (copiedKey.value === s.key) copiedKey.value = '' }, 1500)
}
</script>

<template>
  <div class="card">
    <h3 v-if="nodeId">节点 <span style="text-transform:none">{{ nodeId }}</span> 的安装命令</h3>
    <h3 v-else>接入新节点</h3>
    <p class="sub">agent 主动连接本服务器，家里 NAS 无需公网 IP 或端口映射。<b>每个节点有自己的 token，token 就是节点的身份</b>：先填节点名、位置、运营商并「创建节点」，再选对应平台点「复制」，到目标机器上粘贴执行。</p>
    <p class="sub"><b>覆盖安装</b>：同一个节点的命令（同一个 token）重复执行即可——升级、修复、换机器、改位置或运营商都行，节点 ID、历史记录和监控保持不变。已有节点的命令随时在节点列表点「安装命令」再取；token 泄露或机器易手时点「重置 token」，旧 token 立即失效。</p>
    <div class="row" style="gap:16px;margin-bottom:12px">
      <div class="field"><label>节点名 PROBE_NAME{{ nodeId ? '' : '（唯一）' }}</label><input type="text" v-model="name" spellcheck="false" /></div>
      <div class="field"><label>位置 PROBE_LOCATION</label><input type="text" v-model="loc" /></div>
      <div class="field"><label>运营商 PROBE_ISP</label><input type="text" v-model="isp" /></div>
      <button v-if="!nodeId" class="btn primary" style="margin-top:18px" type="button" :disabled="busy || !name.trim()" @click="create">创建节点</button>
      <template v-else>
        <label class="field inline" style="margin-top:18px"><input type="checkbox" v-model="showToken" /> 显示 token</label>
        <button class="btn sm danger" style="margin-top:18px" type="button" :disabled="busy" @click="resetToken">重置 token</button>
        <button class="btn sm" style="margin-top:18px" type="button" @click="startOver">接入其它新节点</button>
      </template>
    </div>
    <div class="error-box" v-if="error">{{ error }}</div>
    <p class="sub" v-if="nodeId">节点 ID 固定为 <code>{{ nodeId }}</code>，由 token 决定，与命令里的节点名无关；这里改节点名、位置、运营商后重新执行命令，节点列表里的显示会跟着更新。</p>
    <template v-if="token">
      <details v-for="s in snippets" :key="s.key" :open="s.open">
        <summary>{{ s.title }}</summary>
        <div class="snippet">
          <button class="btn sm copy" type="button" @click="copy(s)">{{ copiedKey === s.key ? '已复制' : '复制' }}</button>
          <pre class="cmd">{{ s.text(false) }}</pre>
        </div>
      </details>
    </template>
    <p class="sub" v-else-if="!nodeId">创建节点后，这里会出现填好该节点 token 的各平台安装命令。</p>
    <details>
      <summary>{{ uninstall.title }}</summary>
      <div class="snippet">
        <button class="btn sm copy" type="button" @click="copy(uninstall)">{{ copiedKey === uninstall.key ? '已复制' : '复制' }}</button>
        <pre class="cmd">{{ uninstall.text() }}</pre>
      </div>
    </details>
    <p class="sub" v-if="downloads.length" style="margin-top:10px">本服务端自带的 agent 二进制（{{ serverVersion }}）：
      <a v-for="f in downloads" :key="f.key" :href="'/api/agent/download/' + f.key" style="margin-right:10px">{{ f.key }}</a>
    </p>
    <p class="sub">验证探测能力：装好后在目标机器上执行 <code>sudo /var/lib/probe-agent/probe-agent test www.qq.com</code>（路由器：<code>probe-agent test www.qq.com</code>；Windows：<code>&amp; 'C:\ProgramData\probe-agent\probe-agent.exe' test www.qq.com</code>）。</p>
    <p class="sub">Windows 说明：需要 Windows 10 / 11 或 Server 2016 以上，x64 / ARM64 / 32 位都有对应构建；不用装任何运行库（单个静态 exe，不依赖 .NET、VC++ 或 Npcap），脚本会自行检查管理员权限、系统版本和 CPU 架构。装到 <code>C:\ProgramData\probe-agent\</code>，服务名 <code>probe-agent</code>，日志在同目录 <code>probe-agent.log</code>；电脑睡眠时节点会离线，建议把「电源和睡眠」里的睡眠设为「从不」。</p>
  </div>
</template>
