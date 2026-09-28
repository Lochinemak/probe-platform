<script setup>
import { ref, computed, onMounted } from 'vue'
import { api } from '../api.js'

// Onboarding snippets. Variables come first (export ...) so a copied command
// can be edited at the top; the values are driven by the inputs below and the
// agent token is fetched for the logged-in admin.
const props = defineProps({
  agentImage: { type: String, default: 'ghcr.io/lochinemak/probe-agent:latest' },
  serverVersion: { type: String, default: '' },
  downloads: { type: Array, default: () => [] },
})
const serverURL = `${location.protocol}//${location.host}`
const token = ref('')
const showToken = ref(false)
const name = ref('home-shenzhen')
const loc = ref('广东 深圳')
const isp = ref('电信')
const copiedKey = ref('')

onMounted(async () => {
  try { token.value = (await api.agentToken()).token || '' } catch { token.value = '' }
})

const realToken = computed(() => token.value || '<agent token>')
const maskedToken = computed(() => (showToken.value || !token.value) ? realToken.value : '••••••••••••' + token.value.slice(-4))
function q(s) { s = String(s ?? ''); return /[\s"'$`\\]/.test(s) ? '"' + s.replace(/(["\\$`])/g, '\\$1') + '"' : (s || '""') }
function yq(s) { return JSON.stringify(String(s ?? '')) }

function exportsBlock(real) {
  return [
    `export PROBE_SERVER=${serverURL}`,
    `export PROBE_TOKEN=${real ? realToken.value : maskedToken.value}`,
    `export PROBE_NAME=${q(name.value)}`,
    `export PROBE_LOCATION=${q(loc.value)}`,
    `export PROBE_ISP=${q(isp.value)}`,
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
      PROBE_TOKEN: ${yq(real ? realToken.value : maskedToken.value)}
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
  { key: 'docker', title: 'Docker（Unraid / 任意 Linux；升级靠拉新镜像）',
    text: (real) => exportsBlock(real) + `\ndocker run -d --name probe-agent --restart unless-stopped \\
  --network host --cap-add NET_RAW \\
  -e PROBE_SERVER -e PROBE_TOKEN -e PROBE_NAME -e PROBE_LOCATION -e PROBE_ISP \\
  ${props.agentImage}` },
  { key: 'synology', title: '群晖 DSM · Container Manager 项目（docker-compose）', text: (real) => composeYaml(real, 'synology') },
  { key: 'qnap', title: '威联通 · Container Station 应用程序（docker-compose）', text: (real) => composeYaml(real, 'qnap') },
  { key: 'manual', title: '手动：先下载二进制再安装',
    text: (real) => exportsBlock(real) + `\ncurl -fsSL -H "Authorization: Bearer $PROBE_TOKEN" $PROBE_SERVER/api/agent/download/linux-arm64 -o probe-agent
curl -fsSL $PROBE_SERVER/install-agent.sh -o install-agent.sh
sudo -E sh install-agent.sh ./probe-agent` },
])

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
    <h3>接入新节点</h3>
    <p class="sub">agent 主动连接本服务器，家里 NAS 无需公网 IP 或端口映射。下面的命令已填入本服务器地址和 agent token；改好节点名、位置、运营商后点「复制」即可到目标机器上粘贴执行。</p>
    <div class="row" style="gap:16px;margin-bottom:12px">
      <div class="field"><label>节点名 PROBE_NAME（唯一）</label><input type="text" v-model="name" spellcheck="false" /></div>
      <div class="field"><label>位置 PROBE_LOCATION</label><input type="text" v-model="loc" /></div>
      <div class="field"><label>运营商 PROBE_ISP</label><input type="text" v-model="isp" /></div>
      <label class="field inline" style="margin-top:18px"><input type="checkbox" v-model="showToken" /> 显示 token</label>
      <span class="sub" v-if="!token" style="margin-top:18px">未能获取 token，请用服务器 data/agent_token 的内容替换占位符</span>
    </div>
    <details v-for="s in snippets" :key="s.key" :open="s.open">
      <summary>{{ s.title }}</summary>
      <div class="snippet">
        <button class="btn sm copy" type="button" @click="copy(s)">{{ copiedKey === s.key ? '已复制' : '复制' }}</button>
        <pre class="cmd">{{ s.text(false) }}</pre>
      </div>
    </details>
    <p class="sub" v-if="downloads.length" style="margin-top:10px">本服务端自带的 agent 二进制（{{ serverVersion }}）：
      <a v-for="f in downloads" :key="f.key" :href="'/api/agent/download/' + f.key" style="margin-right:10px">{{ f.key }}</a>
    </p>
    <p class="sub">验证探测能力：装好后在目标机器上执行 <code>sudo /var/lib/probe-agent/probe-agent test www.qq.com</code>（路由器：<code>probe-agent test www.qq.com</code>）。</p>
  </div>
</template>
