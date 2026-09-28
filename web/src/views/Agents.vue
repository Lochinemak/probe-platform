<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { api } from '../api.js'
import { timeAgo, fmtTime } from '../fmt.js'

const props = defineProps({
  agentImage: { type: String, default: 'ghcr.io/lochinemak/probe-agent:latest' },
  serverVersion: { type: String, default: '' },
})
const agents = ref([])
const error = ref('')
const downloads = ref([])
function outdated(a) {
  return a.version && props.serverVersion && a.version !== 'dev' && props.serverVersion !== 'dev' && a.version !== props.serverVersion
}
async function loadDownloads() {
  try { downloads.value = (await api.agentFiles()).files || [] } catch { downloads.value = [] }
}
let timer = null
async function load() {
  try { agents.value = (await api.agents()).agents } catch (e) { if (e.status !== 401) error.value = e.message }
}
async function remove(a) {
  if (!confirm(`删除离线节点 ${a.name} 的记录？（节点重新连接会自动恢复）`)) return
  try { await api.deleteAgent(a.id); await load() } catch (e) { error.value = e.message }
}
const online = computed(() => agents.value.filter((a) => a.online).length)
const serverURL = computed(() => `${location.protocol}//${location.host}`)
onMounted(() => { load(); loadDownloads(); timer = setInterval(load, 5000) })
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div>
    <div class="error-box" v-if="error">{{ error }}</div>
    <div class="card">
      <h3>节点列表 <span class="sub">{{ online }} 在线 / {{ agents.length }} 总数</span></h3>
      <div class="table-wrap">
        <table class="grid">
          <thead>
            <tr>
              <th></th><th>名称</th><th>位置</th><th>运营商</th><th>公网 IP</th><th>系统</th><th>版本</th><th>能力</th><th class="num">运行中</th><th>最后在线</th><th></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="a in agents" :key="a.id">
              <td><span class="status-dot" :class="{ online: a.online }"></span></td>
              <td><b>{{ a.name }}</b><div class="sub" v-if="a.tags?.length">{{ a.tags.join(', ') }}</div></td>
              <td>{{ a.location || a.geo_location || '-' }}<div class="sub" v-if="a.location && a.geo_location && a.location !== a.geo_location">{{ a.geo_location }}</div></td>
              <td>{{ a.isp || a.geo_isp || '-' }}<div class="sub" v-if="a.isp && a.geo_isp && a.isp !== a.geo_isp">{{ a.geo_isp }}</div></td>
              <td class="mono">{{ a.public_ip || '-' }}</td>
              <td class="sub">{{ a.os }}/{{ a.arch }}</td>
              <td class="sub">{{ a.version }} <span v-if="outdated(a)" class="badge warn" title="与服务端版本不一致，节点重连后会自动更新">待更新</span></td>
              <td>
                <span v-for="c in a.capabilities" :key="c" class="badge" :class="{ ok: c === 'icmp_raw' || c === 'mtr' }" style="margin-right:4px">{{ c }}</span>
                <span v-if="a.capabilities && !a.capabilities.includes('mtr')" class="badge warn" title="没有 raw socket 权限，MTR 不可用">无 MTR</span>
              </td>
              <td class="num">{{ a.running || 0 }}</td>
              <td class="sub" :title="fmtTime(a.last_seen)">{{ a.online ? '在线' : timeAgo(a.last_seen) }}</td>
              <td><button v-if="!a.online" class="btn sm danger" @click="remove(a)">删除</button></td>
            </tr>
            <tr v-if="!agents.length"><td colspan="11" class="empty">暂无节点</td></tr>
          </tbody>
        </table>
      </div>
    </div>

    <div class="card">
      <h3>接入新节点</h3>
      <p class="sub">在腾讯云服务器、家里的 NAS（x86 / ARM）上运行 agent，它会主动连接本服务器，无需公网 IP 或端口映射。Token 在服务器 <code>data/agent_token</code> 文件（或 <code>PROBE_AGENT_TOKEN</code>）中。</p>
      <details open>
        <summary>Linux + systemd 一条命令（推荐；之后随 dashboard 自动更新）</summary>
        <pre class="cmd">curl -fsSL {{ serverURL }}/install-agent.sh | sudo \
  PROBE_SERVER={{ serverURL }} PROBE_TOKEN=&lt;agent token&gt; \
  PROBE_NAME=home-shenzhen PROBE_LOCATION="广东 深圳" PROBE_ISP=电信 sh</pre>
        <p class="sub" v-if="downloads.length">本服务端自带的 agent 二进制（v{{ serverVersion }}）：
          <a v-for="f in downloads" :key="f.key" :href="'/api/agent/download/' + f.key" style="margin-right:10px">{{ f.key }}</a>
        </p>
      </details>
      <details>
        <summary>Docker（群晖 / QNAP / Unraid 均可，升级靠拉新镜像）</summary>
        <pre class="cmd">docker run -d --name probe-agent --restart unless-stopped \
  --network host --cap-add NET_RAW \
  -e PROBE_SERVER={{ serverURL }} \
  -e PROBE_TOKEN=&lt;agent token&gt; \
  -e PROBE_NAME=home-shenzhen -e PROBE_LOCATION="广东 深圳" -e PROBE_ISP=电信 \
  {{ props.agentImage }}</pre>
      </details>
      <details>
        <summary>手动：先下载再安装</summary>
        <pre class="cmd">curl -fsSL -H "Authorization: Bearer &lt;agent token&gt;" {{ serverURL }}/api/agent/download/linux-arm64 -o probe-agent
curl -fsSL {{ serverURL }}/install-agent.sh -o install-agent.sh
sudo PROBE_SERVER={{ serverURL }} PROBE_TOKEN=&lt;agent token&gt; PROBE_NAME=nas-1 sh install-agent.sh ./probe-agent</pre>
      </details>
      <details>
        <summary>先在本机验证探测能力</summary>
        <pre class="cmd">probe-agent test www.qq.com          # 依次跑 ping / tcping / http / mtr
probe-agent test mtr 1.1.1.1
# 已用 systemd 安装的机器上：sudo /var/lib/probe-agent/probe-agent test www.qq.com</pre>
      </details>
    </div>
  </div>
</template>
