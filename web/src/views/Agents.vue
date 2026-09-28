<script setup>
import { ref, computed, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { api } from '../api.js'
import { timeAgo, fmtTime } from '../fmt.js'
import Onboarding from '../components/Onboarding.vue'

const props = defineProps({
  agentImage: { type: String, default: 'ghcr.io/lochinemak/probe-agent:latest' },
  serverVersion: { type: String, default: '' },
  admin: { type: Boolean, default: true },
})
const agents = ref([])
const error = ref('')
const downloads = ref([])
const legacyToken = ref('none') // shared token of earlier versions: none | enabled | disabled
const selected = ref(null) // node whose install command is shown
const onboarding = ref(null)
function outdated(a) {
  return a.version && props.serverVersion && a.version !== 'dev' && props.serverVersion !== 'dev' && a.version !== props.serverVersion
}
async function loadDownloads() {
  try { downloads.value = (await api.agentFiles()).files || [] } catch { downloads.value = [] }
}
let timer = null
async function load() {
  try {
    const r = await api.agents()
    agents.value = r.agents
    legacyToken.value = r.legacy_token || 'none'
  } catch (e) { if (e.status !== 401) error.value = e.message }
}
async function remove(a) {
  const msg = `删除节点 ${a.name}？\n\n它的 token 会立即失效${a.online ? '，正在运行的 agent 会被断开' : ''}，那台机器上的 agent 再也连不上；历史记录保留。` +
    '\n要重新接入只能新建节点。卸载那台机器上的 agent 请用「接入新节点」卡片里的卸载命令。'
  if (!confirm(msg)) return
  try {
    await api.deleteAgent(a.id)
    if (selected.value?.id === a.id) selected.value = null
    await load()
  } catch (e) { error.value = e.message }
}
async function showInstall(a) {
  selected.value = a
  await nextTick()
  onboarding.value?.$el?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}
const online = computed(() => agents.value.filter((a) => a.online).length)
const legacyNodes = computed(() => agents.value.filter((a) => a.auth === 'legacy'))
async function setLegacy(enabled) {
  if (!enabled) {
    const left = legacyNodes.value
    const msg = left.length
      ? `仍有 ${left.length} 个节点在用共享 token：${left.map((a) => a.name).join('、')}。\n\n停用后它们会立即掉线，直到用各自的「安装命令」重装。确定停用？`
      : '停用旧版共享 token？此后只有节点自己的 token 能连接和下载 agent。'
    if (!confirm(msg)) return
  }
  try { await api.updateSettings({ legacy_agent_token: enabled }); await load() } catch (e) { error.value = e.message }
}
onMounted(() => { load(); if (props.admin) loadDownloads(); timer = setInterval(load, 5000) })
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div>
    <div class="error-box" v-if="error">{{ error }}</div>
    <div class="card" v-if="props.admin && legacyToken === 'enabled'" style="border-color:var(--warn)">
      <h3>迁移到节点独立 token <span class="sub">{{ legacyNodes.length ? `${legacyNodes.length} 个节点仍在用旧版共享 token` : '全部节点已迁移' }}</span></h3>
      <p class="sub" style="margin-top:0">现在每个节点都有自己的 token，服务端按 token 认节点，不再相信节点自报的名字。旧版共享 token 目前仍被接受，但只对升级前就存在、尚未迁移的节点有效：</p>
      <ul class="sub" style="margin:0 0 10px;padding-left:20px">
        <li><b>systemd / OpenWrt / Windows 节点自动迁移</b>：它们自更新到新版后，服务端会把节点自己的 token 交给它，agent 存到二进制旁的 <code>probe-agent.token</code> 并立即改用它重连，无需任何操作。</li>
        <li><b>Docker / 群晖 / 威联通节点</b>不自更新，需要手动迁移：点该节点的「安装命令」，按 Docker / 群晖 / 威联通的命令重建容器（同一个 token 覆盖安装，节点和历史不变）。</li>
        <li>标着 <span class="badge warn">共享 token</span> 的节点就是还没迁移的。全部迁移后点下面的按钮停用共享 token，此后只有节点自己的 token 能连接、下载 agent，环境变量里的 <code>PROBE_AGENT_TOKEN</code> 也可以删掉。</li>
      </ul>
      <button class="btn sm danger" type="button" @click="setLegacy(false)">停用共享 token</button>
    </div>
    <p class="sub" v-if="props.admin && legacyToken === 'disabled'" style="margin:0 0 12px">旧版共享 token 已停用，只有节点自己的 token 能连接。
      <button class="btn link sm" type="button" @click="setLegacy(true)">重新启用</button></p>
    <div class="card">
      <h3>节点列表 <span class="sub">{{ online }} 在线 / {{ agents.length }} 总数</span></h3>
      <div class="table-wrap" v-if="!props.admin">
        <table class="grid">
          <thead><tr><th></th><th>名称</th><th>位置</th><th>运营商</th><th>能力</th><th class="num">运行中</th></tr></thead>
          <tbody>
            <tr v-for="a in agents" :key="a.id">
              <td><span class="status-dot" :class="{ online: a.online }"></span></td>
              <td><b>{{ a.name }}</b></td>
              <td>{{ a.location || '-' }}</td>
              <td>{{ a.isp || '-' }}</td>
              <td><span v-for="c in a.capabilities" :key="c" class="badge" style="margin-right:4px">{{ c }}</span></td>
              <td class="num">{{ a.running || 0 }}</td>
            </tr>
            <tr v-if="!agents.length"><td colspan="6" class="empty">暂无节点</td></tr>
          </tbody>
        </table>
        <p class="sub">游客视图只显示节点的位置与运营商；管理员登录后可查看公网 IP、版本等信息并接入新节点。</p>
      </div>
      <div class="table-wrap" v-else>
        <table class="grid">
          <thead>
            <tr>
              <th></th><th>名称</th><th>位置</th><th>运营商</th><th>公网 IP</th><th>系统</th><th>版本</th><th>能力</th><th class="num">运行中</th><th>最后在线</th><th></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="a in agents" :key="a.id">
              <td><span class="status-dot" :class="{ online: a.online }"></span></td>
              <td>
                <b>{{ a.name }}</b>
                <span v-if="a.auth === 'legacy'" class="badge warn" style="margin-left:6px" title="还在用旧版共享 token；自更新到新版后会自动换成节点自己的 token，Docker 节点需要用「安装命令」重建容器">共享 token</span>
                <span v-else-if="!a.auth" class="badge" style="margin-left:6px" title="已在 dashboard 创建，还没用它的 token 连上来过">待接入</span>
                <div class="sub" v-if="a.id !== a.name">ID {{ a.id }}</div>
                <div class="sub" v-if="a.tags?.length">{{ a.tags.join(', ') }}</div>
              </td>
              <td>{{ a.location || a.geo_location || '-' }}<div class="sub" v-if="a.location && a.geo_location && a.location !== a.geo_location">{{ a.geo_location }}</div></td>
              <td>{{ a.isp || a.geo_isp || '-' }}<div class="sub" v-if="a.isp && a.geo_isp && a.isp !== a.geo_isp">{{ a.geo_isp }}</div></td>
              <td class="mono">{{ a.public_ip || '-' }}</td>
              <td class="sub">{{ a.os ? `${a.os}/${a.arch}` : '-' }}</td>
              <td class="sub">{{ a.version }} <span v-if="outdated(a)" class="badge warn" title="与服务端版本不一致，节点重连后会自动更新">待更新</span></td>
              <td>
                <span v-for="c in a.capabilities" :key="c" class="badge" :class="{ ok: c === 'icmp_raw' || c === 'mtr' }" style="margin-right:4px">{{ c }}</span>
                <span v-if="a.capabilities && !a.capabilities.includes('mtr')" class="badge warn" title="没有 raw socket 权限，MTR 不可用">无 MTR</span>
              </td>
              <td class="num">{{ a.running || 0 }}</td>
              <td class="sub" :title="a.auth ? fmtTime(a.last_seen) : ''">{{ a.online ? '在线' : (a.auth ? timeAgo(a.last_seen) : '从未连接') }}</td>
              <td style="white-space:nowrap">
                <button class="btn sm" @click="showInstall(a)" title="查看或重置这个节点的 token 与安装命令；用同一个 token 重新执行即覆盖安装">安装命令</button>
                <button class="btn sm danger" @click="remove(a)" style="margin-left:6px">删除</button>
              </td>
            </tr>
            <tr v-if="!agents.length"><td colspan="11" class="empty">暂无节点</td></tr>
          </tbody>
        </table>
      </div>
    </div>

    <Onboarding v-if="props.admin" ref="onboarding" :agent-image="props.agentImage" :server-version="props.serverVersion" :downloads="downloads"
      :node="selected" @changed="load" @clear="selected = null" />
  </div>
</template>
