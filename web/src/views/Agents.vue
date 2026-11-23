<script setup>
import { ref, computed, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { api } from '../api.js'
import { timeAgo, fmtTime } from '../fmt.js'
import { LINES, PROVINCES, COUNTRIES, COMMON_COUNTRIES, OTHER_COUNTRIES, placeLabel } from '../geo.js'
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
// --- correcting a node's place and line ---
// The server works them out from the public IP and the labels given when the
// node was created; this is for when both are wrong (a NAS behind a tunnel, an
// IP the database files under the wrong city).
const geoEdit = ref(null) // { id, name, cc, province, city, line, saving }
const geoError = ref('')
function editGeo(a) {
  const o = a.geo_override || {}
  geoEdit.value = { id: a.id, name: a.name, auto: a.geo, cc: o.cc || '', province: o.province || '', city: o.city || '', line: o.line || '', saving: false }
  geoError.value = ''
}
const countryChoices = computed(() => {
  const e = geoEdit.value
  // A code the list does not have (set through the API) stays selectable.
  return e && e.cc && !COUNTRIES.some(([cc]) => cc === e.cc) ? [[e.cc, e.cc]] : []
})
function onCountryChange() { geoEdit.value.province = ''; geoEdit.value.city = '' }
async function saveGeo(clear = false) {
  const e = geoEdit.value
  e.saving = true
  geoError.value = ''
  try {
    await api.patchAgent(e.id, { geo: clear ? null : { cc: e.cc, province: e.cc ? e.province : '', city: e.cc ? e.city : '', line: e.line } })
    geoEdit.value = null
    await load()
  } catch (err) { geoError.value = err.message; e.saving = false }
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
    <form class="card" v-if="geoEdit" @submit.prevent="saveGeo()">
      <h3>修正节点地域与线路 <span class="sub">{{ geoEdit.name }}</span></h3>
      <p class="sub" style="margin-top:0">地域和线路默认由公网 IP 归属地、以及接入时填写的位置 / 运营商得出{{ geoEdit.auto ? `（当前：${placeLabel(geoEdit.auto) || '未识别'}${geoEdit.auto.line ? ' · ' + geoEdit.auto.line : ''}）` : '（当前未识别）' }}。只填需要改的项，留空的继续自动识别；改地域时必须选国家或地区。</p>
      <div class="row top" style="gap:16px">
        <div class="field"><label>国家或地区</label>
          <select v-model="geoEdit.cc" @change="onCountryChange">
            <option value="">自动识别</option>
            <option v-for="[cc, name] in countryChoices" :key="cc" :value="cc">{{ name }}</option>
            <optgroup label="常用"><option v-for="[cc, name] in COMMON_COUNTRIES" :key="cc" :value="cc">{{ name }}</option></optgroup>
            <optgroup label="其他（按拼音）"><option v-for="[cc, name] in OTHER_COUNTRIES" :key="cc" :value="cc">{{ name }}</option></optgroup>
          </select>
        </div>
        <div class="field" v-if="geoEdit.cc === 'CN'"><label>省份</label>
          <select v-model="geoEdit.province"><option value="">未指定</option><option v-for="p in PROVINCES" :key="p">{{ p }}</option></select>
        </div>
        <div class="field" v-else-if="geoEdit.cc"><label>省 / 州</label><input type="text" v-model="geoEdit.province" maxlength="40" /></div>
        <div class="field" v-if="geoEdit.cc"><label>城市</label><input type="text" v-model="geoEdit.city" maxlength="40" placeholder="可留空" /></div>
        <div class="field"><label>线路</label>
          <select v-model="geoEdit.line"><option value="">自动识别</option><option v-for="l in LINES" :key="l">{{ l }}</option></select>
        </div>
      </div>
      <div class="error-box" v-if="geoError" style="margin:12px 0 0">{{ geoError }}</div>
      <div class="row" style="margin-top:14px">
        <button class="btn" type="submit" :disabled="geoEdit.saving">保存</button>
        <button class="btn" type="button" :disabled="geoEdit.saving" @click="saveGeo(true)">恢复自动识别</button>
        <button class="btn link" type="button" @click="geoEdit = null">取消</button>
      </div>
    </form>
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
              <th></th><th>名称</th><th>位置</th><th>运营商</th><th title="节点筛选、结果分组和地图用的地域与线路：由公网 IP 归属地和上面两列的填写内容得出，可手动修正">地域 / 线路</th><th>公网 IP</th><th>系统</th><th>版本</th><th>能力</th><th class="num">运行中</th><th>最后在线</th><th></th>
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
              <td style="white-space:nowrap">
                <template v-if="a.geo">{{ placeLabel(a.geo) || '未识别' }} <span class="badge" v-if="a.geo.line">{{ a.geo.line }}</span>
                  <span class="sub" v-if="a.geo.src === 'manual'" title="已由管理员手动修正"> 手动</span></template>
                <span v-else class="warn">未识别</span>
                <button class="btn link sm" type="button" @click="editGeo(a)">修正</button>
              </td>
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
            <tr v-if="!agents.length"><td colspan="12" class="empty">暂无节点</td></tr>
          </tbody>
        </table>
      </div>
    </div>

    <Onboarding v-if="props.admin" ref="onboarding" :agent-image="props.agentImage" :server-version="props.serverVersion" :downloads="downloads"
      :node="selected" @changed="load" @clear="selected = null" />
  </div>
</template>
