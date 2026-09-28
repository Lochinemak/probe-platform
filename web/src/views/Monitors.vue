<script setup>
import { ref, reactive, computed, onMounted, onBeforeUnmount } from 'vue'
import { api } from '../api.js'
import { typeLabel, timeAgo, ms, pct } from '../fmt.js'
import MonitorDetail from '../components/MonitorDetail.vue'

const emit = defineEmits(['open-history'])
const monitors = ref([])
const agents = ref([])
const channels = ref([])
const error = ref('')
const editing = ref(null) // monitor object being edited/created
const detailId = ref('')
let timer = null

const intervals = [[30, '30 秒'], [60, '1 分钟'], [120, '2 分钟'], [300, '5 分钟'], [600, '10 分钟'], [900, '15 分钟'], [1800, '30 分钟'], [3600, '1 小时'], [21600, '6 小时'], [86400, '24 小时']]
function intervalLabel(sec) { return (intervals.find((i) => i[0] === sec) || [sec, sec + ' 秒'])[1] }

async function load() {
  try {
    const [m, a] = await Promise.all([api.monitors(), api.agents()])
    monitors.value = m.monitors
    agents.value = a.agents
  } catch (e) { if (e.status !== 401) error.value = e.message }
}
async function loadChannels() { try { channels.value = (await api.channels()).channels } catch { /* ignore */ } }

function blank() {
  return {
    name: '', type: 'ping', target: '', interval_sec: 60, enabled: true, agent_ids: [],
    params: { count: 5, timeout_ms: 2000, port: 80, method: 'GET', follow_redirects: true, record_type: 'A', dns_server: '', protocol: 'icmp', ip_version: '', expect_status: 0, expect_keyword: '', expect_max_ms: 0, max_hops: 30 },
    alert: { enabled: true, loss_pct: 50, latency_ms: 0, consecutive: 2 },
    notify_ids: [],
  }
}
function startCreate() { editing.value = blank(); error.value = '' }
function startEdit(m) {
  const e = blank()
  editing.value = { ...e, ...JSON.parse(JSON.stringify(m)), params: { ...e.params, ...(m.params || {}) }, alert: { ...e.alert, ...(m.alert || {}) } }
  error.value = ''
}
function toggleAgent(id) {
  const s = new Set(editing.value.agent_ids)
  s.has(id) ? s.delete(id) : s.add(id)
  editing.value.agent_ids = [...s]
}
function toggleChannel(id) {
  const s = new Set(editing.value.notify_ids)
  s.has(id) ? s.delete(id) : s.add(id)
  editing.value.notify_ids = [...s]
}
function cleanParams(e) {
  const p = { ...e.params }
  const keep = {
    ping: ['count', 'timeout_ms', 'interval_ms', 'packet_size', 'ip_version'],
    tcping: ['count', 'timeout_ms', 'interval_ms', 'port', 'ip_version'],
    http: ['count', 'timeout_ms', 'method', 'follow_redirects', 'insecure_tls', 'expect_status', 'expect_keyword', 'expect_max_ms', 'ip_version'],
    mtr: ['count', 'timeout_ms', 'max_hops', 'protocol', 'port', 'ip_version'],
    dns: ['count', 'timeout_ms', 'record_type', 'dns_server', 'ip_version'],
  }[e.type]
  const out = {}
  for (const k of keep) if (p[k] !== '' && p[k] !== null && p[k] !== undefined && p[k] !== false && p[k] !== 0) out[k] = p[k]
  if (e.type === 'mtr' && out.protocol === 'icmp') delete out.port
  if (e.type === 'http' && e.params.follow_redirects === false) delete out.follow_redirects
  return out
}
async function save() {
  const e = editing.value
  const body = { ...e, params: cleanParams(e) }
  try {
    if (e.id) await api.updateMonitor(e.id, body)
    else await api.createMonitor(body)
    editing.value = null
    await load()
  } catch (err) { error.value = err.message }
}
async function remove(m) {
  if (!confirm(`删除监控「${m.name}」及其全部历史数据？`)) return
  try { await api.deleteMonitor(m.id); await load() } catch (e) { error.value = e.message }
}
async function toggleEnabled(m) {
  try { await api.updateMonitor(m.id, { ...m, enabled: !m.enabled }); await load() } catch (e) { error.value = e.message }
}
async function runNow(m) {
  try { await api.runMonitor(m.id); setTimeout(load, 3000) } catch (e) { error.value = e.message }
}
function agentClass(st) {
  if (st.alerting) return 'bad'
  if (!st.last) return ''
  return st.last.ok ? 'ok' : 'warn'
}
const totalAlerting = computed(() => monitors.value.reduce((s, m) => s + (m.alerting || 0), 0))

onMounted(() => { load(); loadChannels(); timer = setInterval(load, 15000) })
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div>
    <MonitorDetail v-if="detailId" :id="detailId" @close="detailId = ''; load()" @edit="(m) => { detailId = ''; startEdit(m) }" @open-history="(id) => emit('open-history', id)" />
    <template v-else>
      <div class="error-box" v-if="error">{{ error }}</div>

      <div class="card" v-if="editing">
        <h3>{{ editing.id ? '编辑监控' : '新建监控' }}</h3>
        <form @submit.prevent="save">
          <div class="row" style="gap:16px">
            <div class="field"><label>名称</label><input type="text" v-model="editing.name" placeholder="默认用目标" /></div>
            <div class="field"><label>类型</label>
              <select v-model="editing.type"><option v-for="t in ['ping', 'tcping', 'http', 'mtr', 'dns']" :key="t" :value="t">{{ typeLabel[t] }}</option></select>
            </div>
            <div class="field grow"><label>目标</label><input type="text" v-model="editing.target" class="mono" required spellcheck="false" /></div>
            <div class="field"><label>间隔</label>
              <select v-model.number="editing.interval_sec"><option v-for="[v, l] in intervals" :key="v" :value="v">{{ l }}</option></select>
            </div>
            <label class="field inline" style="margin-top:18px"><input type="checkbox" v-model="editing.enabled" /> 启用</label>
          </div>

          <div class="row" style="gap:16px;margin-top:10px">
            <div class="field"><label>次数</label><input type="number" min="1" max="100" v-model.number="editing.params.count" /></div>
            <div class="field"><label>超时 (ms)</label><input type="number" min="200" max="60000" v-model.number="editing.params.timeout_ms" /></div>
            <div class="field" v-if="editing.type === 'tcping'"><label>端口</label><input type="number" min="1" max="65535" v-model.number="editing.params.port" /></div>
            <template v-if="editing.type === 'http'">
              <div class="field"><label>方法</label><select v-model="editing.params.method"><option v-for="m in ['GET', 'HEAD', 'POST']" :key="m">{{ m }}</option></select></div>
              <div class="field"><label>期望状态码 (0 = &lt; 400)</label><input type="number" min="0" max="599" v-model.number="editing.params.expect_status" /></div>
              <div class="field"><label>关键字</label><input type="text" v-model="editing.params.expect_keyword" /></div>
              <div class="field"><label>耗时上限 (ms)</label><input type="number" min="0" v-model.number="editing.params.expect_max_ms" /></div>
              <label class="field inline" style="margin-top:18px"><input type="checkbox" v-model="editing.params.follow_redirects" /> 跟随重定向</label>
            </template>
            <template v-if="editing.type === 'mtr'">
              <div class="field"><label>探针</label><select v-model="editing.params.protocol"><option value="icmp">ICMP</option><option value="tcp">TCP SYN</option><option value="udp">UDP</option></select></div>
              <div class="field" v-if="editing.params.protocol !== 'icmp'"><label>端口</label><input type="number" min="1" max="65535" v-model.number="editing.params.port" /></div>
              <div class="field"><label>最大跳数</label><input type="number" min="1" max="64" v-model.number="editing.params.max_hops" /></div>
            </template>
            <template v-if="editing.type === 'dns'">
              <div class="field"><label>记录类型</label><select v-model="editing.params.record_type"><option v-for="t in ['A', 'AAAA', 'CNAME', 'MX', 'TXT', 'NS']" :key="t">{{ t }}</option></select></div>
              <div class="field grow"><label>DNS 服务器（留空用节点默认）</label><input type="text" v-model="editing.params.dns_server" placeholder="223.5.5.5 / https://doh.pub/dns-query" /></div>
            </template>
            <div class="field"><label>IP 版本</label><select v-model="editing.params.ip_version"><option value="">自动</option><option value="4">IPv4</option><option value="6">IPv6</option></select></div>
          </div>

          <div style="margin-top:12px">
            <label class="sub">节点（不选 = 每次运行时所有在线节点）</label>
            <div class="agents-pick" style="margin-top:6px">
              <span v-for="a in agents" :key="a.id" class="chip" :class="{ on: editing.agent_ids.includes(a.id), off: !a.online }" @click="toggleAgent(a.id)">
                <span class="status-dot" :class="{ online: a.online }"></span>{{ a.name }}<span class="sub" v-if="a.location || a.geo_location">{{ a.location || a.geo_location }}</span>
              </span>
            </div>
          </div>

          <div class="row" style="gap:16px;margin-top:12px">
            <label class="field inline" style="margin-top:18px"><input type="checkbox" v-model="editing.alert.enabled" /> 启用告警</label>
            <template v-if="editing.alert.enabled">
              <div class="field"><label>丢包 ≥ (%，0 不检查)</label><input type="number" min="0" max="100" v-model.number="editing.alert.loss_pct" /></div>
              <div class="field"><label>延迟 ≥ (ms，0 不检查)</label><input type="number" min="0" v-model.number="editing.alert.latency_ms" /></div>
              <div class="field"><label>连续失败次数</label><input type="number" min="1" max="20" v-model.number="editing.alert.consecutive" /></div>
              <div class="field grow"><label>通知渠道</label>
                <div class="agents-pick" style="margin-top:4px">
                  <span v-for="c in channels" :key="c.id" class="chip" :class="{ on: editing.notify_ids.includes(c.id) }" @click="toggleChannel(c.id)">{{ c.name }} <span class="sub">{{ c.type }}</span></span>
                  <span class="sub" v-if="!channels.length">还没有通知渠道，到「通知」页添加</span>
                </div>
              </div>
            </template>
          </div>
          <p class="sub" style="margin:8px 0 0">探测出错、全部丢包、HTTP 断言失败、MTR 未到达、DNS 非 NOERROR 一律视为失败；上面的阈值是额外条件。连续失败达到次数后告警一次，恢复时再通知一次。</p>
          <div class="row" style="margin-top:12px">
            <button class="btn primary" type="submit">保存</button>
            <button class="btn" type="button" @click="editing = null">取消</button>
          </div>
        </form>
      </div>

      <div class="card">
        <div class="row" style="margin-bottom:10px">
          <h3 style="margin:0">定时监控 <span class="sub">{{ monitors.length }} 个<template v-if="totalAlerting"> · <span class="bad">{{ totalAlerting }} 个节点告警中</span></template></span></h3>
          <span class="spacer"></span>
          <button class="btn sm" @click="load">刷新</button>
          <button class="btn primary sm" style="padding:6px 14px" @click="startCreate" v-if="!editing">新建监控</button>
        </div>
        <div class="table-wrap">
          <table class="grid">
            <thead><tr><th></th><th>名称</th><th>目标</th><th>间隔</th><th>节点</th><th>最近运行</th><th></th></tr></thead>
            <tbody>
              <tr v-for="m in monitors" :key="m.id">
                <td><span class="status-dot" :class="{ online: m.enabled }" :title="m.enabled ? '运行中' : '已停用'"></span></td>
                <td><a href="#" @click.prevent="detailId = m.id"><b>{{ m.name }}</b></a><span v-if="m.alerting" class="badge error" style="margin-left:6px">告警 {{ m.alerting }}</span></td>
                <td><span class="badge" :class="m.type">{{ typeLabel[m.type] }}</span> <span class="mono">{{ m.target }}</span></td>
                <td class="sub">{{ intervalLabel(m.interval_sec) }}</td>
                <td style="white-space:normal">
                  <span v-for="st in m.agents" :key="st.agent_id" class="badge" :class="agentClass(st)" style="margin:2px 4px 2px 0" :title="st.last ? (st.last.error || ('丢包 ' + pct(st.last.loss_pct))) : '暂无数据'">
                    {{ st.agent_name }} <template v-if="st.last && st.last.latency_ms >= 0">{{ ms(st.last.latency_ms) }}</template><template v-else-if="st.last">×</template>
                  </span>
                  <span v-if="!m.agents.length" class="sub">全部在线节点</span>
                </td>
                <td class="sub">{{ m.last_run_at ? timeAgo(m.last_run_at) : '-' }}</td>
                <td>
                  <button class="btn sm" @click="detailId = m.id">详情</button>
                  <button class="btn sm" @click="runNow(m)">立即运行</button>
                  <button class="btn sm" @click="toggleEnabled(m)">{{ m.enabled ? '停用' : '启用' }}</button>
                  <button class="btn sm" @click="startEdit(m)">编辑</button>
                  <button class="btn sm danger" @click="remove(m)">删除</button>
                </td>
              </tr>
              <tr v-if="!monitors.length"><td colspan="7" class="empty">还没有监控。新建一个，选好目标、间隔和节点，结果会持续入库并绘制趋势图。</td></tr>
            </tbody>
          </table>
        </div>
      </div>
    </template>
  </div>
</template>
