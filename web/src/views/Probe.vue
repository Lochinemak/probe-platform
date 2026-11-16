<script setup>
import { ref, reactive, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { api, subscribe } from '../api.js'
import { parseHeaders, formatHeaders, fmtTime, timeAgo } from '../fmt.js'
import { route, navigate, taskPath, PROBE_TYPES } from '../route.js'
import { TYPE_INFO, validateTarget } from '../validate.js'
import * as recentStore from '../recent.js'
import TaskResults from '../components/TaskResults.vue'
import TargetInput from '../components/TargetInput.vue'

const props = defineProps({ role: { type: String, default: 'admin' } })
const isAdmin = computed(() => props.role === 'admin')

// --- probe type: lives in the URL (/ping, /tcping, …) ---
const TYPE_KEY = 'probe.type'
function storedType() {
  try { const t = localStorage.getItem(TYPE_KEY); return PROBE_TYPES.includes(t) ? t : 'ping' } catch { return 'ping' }
}
const type = ref(route.type || storedType())
const info = computed(() => TYPE_INFO[type.value])
function setType(t) {
  if (t !== type.value) targetError.value = ''
  type.value = t
  try { localStorage.setItem(TYPE_KEY, t) } catch { /* ignore */ }
  if (route.page === 'probe' && route.type !== t) navigate('/' + t)
}

const target = ref('')
const targetError = ref('')
const error = ref('')
const running = ref(false)
const stopping = ref(false)
const showAdvanced = ref(false)
const scheme = ref('https') // http probe: used when the target has no scheme of its own
const httpMode = ref('simple') // simple = one plain GET; full = every request option
const targetBox = ref(null)

const DEFAULTS = {
  ping: { count: 10, interval_ms: 500, timeout_ms: 2000, packet_size: 56, ip_version: '' },
  tcping: { port: 80, count: 10, interval_ms: 500, timeout_ms: 3000, ip_version: '' },
  http: { method: 'GET', count: 1, timeout_ms: 10000, follow_redirects: true, insecure_tls: false, ip_version: '' },
  mtr: { count: 10, max_hops: 30, timeout_ms: 1000, resolve: false, ip_version: '', protocol: 'icmp', port: 80 },
  dns: { record_type: 'A', dns_server: '', count: 1, timeout_ms: 3000, ip_version: '' },
}
const HTTP_EXTRA = { headers: '', body: '', expect_status: 0, expect_keyword: '', expect_max_ms: 0, speed_test: false, speed_seconds: 5 }
const params = reactive(JSON.parse(JSON.stringify(DEFAULTS)))
const httpExtra = reactive({ ...HTTP_EXTRA })
// 持续检测 (ping / tcping): a long sampling run next to the one-off check.
const CONTINUOUS = { count: 100, interval_ms: 1000 }
const cont = reactive({ ...CONTINUOUS })
const hasContinuous = computed(() => type.value === 'ping' || type.value === 'tcping')
const guestContinuous = { count: 60, seconds: 60 } // mirrors the server's guest limits

// The scheme switch follows a scheme typed into the box, and a host:port
// typed for tcping fills the port box.
watch(target, (t) => {
  targetError.value = ''
  if (type.value === 'http') {
    const m = /^(https?):\/\//i.exec(t.trim())
    if (m) scheme.value = m[1].toLowerCase()
  } else if (type.value === 'tcping') {
    const v = validateTarget('tcping', t)
    if (v.ok && v.port) params.tcping.port = v.port
  }
})
function setScheme(s) {
  scheme.value = s
  if (/^https?:\/\//i.test(target.value.trim())) target.value = target.value.trim().replace(/^https?/i, s)
}

// --- agents ---
const agents = ref([])
const selected = ref(new Set())
const onlineAgents = computed(() => agents.value.filter((a) => a.online))
let agentTimer = null
let userTouched = false
async function loadAgents() {
  try {
    const { agents: list } = await api.agents()
    agents.value = list.filter((a) => a.online || a.auth !== '') // skip nodes created but never connected
    const online = new Set(list.filter((a) => a.online).map((a) => a.id))
    if (!userTouched) {
      selected.value = online
    } else {
      const s = new Set([...selected.value].filter((id) => online.has(id)))
      selected.value = s
    }
  } catch (e) { if (e.status !== 401) error.value = '获取节点失败：' + e.message }
}
function toggleAgent(a) {
  if (!a.online) return
  userTouched = true
  const s = new Set(selected.value)
  if (s.has(a.id)) s.delete(a.id); else s.add(a.id)
  selected.value = s
}
function selectAll(on) {
  userTouched = !on
  selected.value = on ? new Set(onlineAgents.value.map((a) => a.id)) : new Set()
}
const groups = computed(() => {
  const m = new Map()
  for (const a of agents.value) {
    const k = a.isp || a.geo_isp || '未分类'
    if (!m.has(k)) m.set(k, [])
    m.get(k).push(a)
  }
  return [...m.entries()]
})

// --- task lifecycle ---
const task = ref(null)
const results = ref([])
let unsubscribe = null
function closeStream() { if (unsubscribe) { unsubscribe(); unsubscribe = null } }

function applyEvent(kind, ev) {
  if (kind === 'snapshot') {
    task.value = ev.task
    results.value = ev.results || []
    if (ev.done) { running.value = false; stopping.value = false }
  } else if (kind === 'progress') {
    const r = results.value.find((x) => x.agent_id === ev.agent_id)
    if (r) {
      r.progress = r.progress || []
      r.progress.push(ev.progress)
      if (r.progress.length > 1000) r.progress.splice(0, r.progress.length - 1000)
    }
  } else if (kind === 'result') {
    const i = results.value.findIndex((x) => x.agent_id === ev.agent_id)
    if (i >= 0) results.value[i] = ev.result
    else results.value.push(ev.result)
  } else if (kind === 'done') {
    running.value = false
    stopping.value = false
  }
}
function onStreamError(e) { error.value = e.message; running.value = false; stopping.value = false }

// buildParams turns the form into the params object for a new task.
function buildParams(continuous) {
  const t = type.value
  let p = { ...params[t] }
  if (t === 'http') {
    if (httpMode.value === 'simple') {
      p = { method: 'GET', count: 1, timeout_ms: p.timeout_ms, follow_redirects: true, ip_version: p.ip_version }
    } else {
      const headers = parseHeaders(httpExtra.headers)
      if (Object.keys(headers).length) p.headers = headers
      if (httpExtra.body && !['GET', 'HEAD'].includes(p.method)) p.body = httpExtra.body
      if (httpExtra.expect_status > 0) p.expect_status = httpExtra.expect_status
      if (httpExtra.expect_keyword) p.expect_keyword = httpExtra.expect_keyword
      if (httpExtra.expect_max_ms > 0) p.expect_max_ms = httpExtra.expect_max_ms
      if (httpExtra.speed_test) { p.speed_test = true; p.speed_seconds = httpExtra.speed_seconds }
    }
  }
  if (t === 'mtr' && p.protocol === 'icmp') delete p.port
  if (continuous) { p.count = cont.count; p.interval_ms = cont.interval_ms; p.continuous = true }
  for (const k of Object.keys(p)) if (p[k] === '' || p[k] === null || p[k] === false) delete p[k]
  return p
}

async function run(mode = 'once') {
  if (running.value) return
  error.value = ''
  const v = validateTarget(type.value, target.value, { scheme: scheme.value })
  if (!v.ok) { targetError.value = v.error; targetBox.value?.focus(); return }
  if (selected.value.size === 0) { error.value = '请至少选择一个在线节点'; return }
  closeStream()
  const p = buildParams(mode === 'continuous' && hasContinuous.value)
  running.value = true
  stopping.value = false
  results.value = []
  try {
    const snap = await api.createTask({ type: type.value, target: v.target, params: p, agent_ids: [...selected.value] })
    applyEvent('snapshot', snap)
    if (!snap.done) unsubscribe = subscribe(snap.task.id, applyEvent, onStreamError)
    remember(v.target, snap.task)
    navigate(taskPath(snap.task.id))
  } catch (e) {
    running.value = false
    error.value = e.message
  }
}
// Stop keeps what the nodes measured so far; an older server only knows the
// discarding cancel.
async function stop() {
  if (!task.value || stopping.value) return
  stopping.value = true
  try { await api.stopTask(task.value.id) } catch {
    try { await api.cancelTask(task.value.id) } catch { /* already finished */ }
  }
}

// fillForm sets the form to what produced task, so a task opened by its link
// can be tweaked and run again.
function fillForm(t) {
  if (!PROBE_TYPES.includes(t.type)) return
  type.value = t.type
  const p = t.params || {}
  const own = params[t.type]
  Object.assign(own, DEFAULTS[t.type])
  for (const k of Object.keys(own)) if (p[k] !== undefined) own[k] = p[k]
  // Checkboxes default to on in the form but are omitted from params when off.
  if (t.type === 'http') own.follow_redirects = !!p.follow_redirects
  if (p.continuous) {
    cont.count = p.count || CONTINUOUS.count
    cont.interval_ms = p.interval_ms || CONTINUOUS.interval_ms
    own.count = DEFAULTS[t.type].count
    own.interval_ms = DEFAULTS[t.type].interval_ms
  }
  if (t.type === 'http') {
    Object.assign(httpExtra, HTTP_EXTRA, {
      headers: formatHeaders(p.headers), body: p.body || '', expect_status: p.expect_status || 0, expect_keyword: p.expect_keyword || '',
      expect_max_ms: p.expect_max_ms || 0, speed_test: !!p.speed_test, speed_seconds: p.speed_seconds || 5,
    })
    const simple = (p.method || 'GET') === 'GET' && (p.count || 1) === 1 && p.follow_redirects && !p.insecure_tls && !p.headers && !p.body
      && !p.expect_status && !p.expect_keyword && !p.expect_max_ms && !p.speed_test
    httpMode.value = simple ? 'simple' : 'full'
  }
  target.value = t.target
  targetError.value = ''
}

// openTask shows the task behind a /t/<id> link: live when it is still
// running, from the store otherwise.
const opening = ref(false)
async function openTask(id) {
  error.value = ''
  closeStream()
  opening.value = true
  try {
    const snap = await api.task(id)
    fillForm(snap.task)
    applyEvent('snapshot', snap)
    running.value = !snap.done
    stopping.value = false
    if (!snap.done) unsubscribe = subscribe(id, applyEvent, onStreamError)
    lastTask.value = null
  } catch (e) {
    if (e.status === 401) return
    error.value = e.status !== 404 ? e.message : isAdmin.value ? '这个检测结果不存在或已过期。' : '这个检测结果不存在、已过期，或不是本浏览器发起的（游客只能查看自己发起的检测）。'
    if (route.taskId === id) navigate('/' + type.value, { replace: true })
  } finally { opening.value = false }
}

// --- recent targets, last task ---
const recent = ref([])
watch(type, (t) => { recent.value = recentStore.list(t) }, { immediate: true })
function removeRecent(t) { recent.value = recentStore.remove(type.value, t) }
function clearRecent() { recent.value = recentStore.clear(type.value) }

const LAST_KEY = 'probe.lastTask'
const lastTask = ref(null) // { id, type, target, at }: offered back on the next visit
function remember(shown, t) {
  recent.value = recentStore.add(t.type, shown)
  try { localStorage.setItem(LAST_KEY, JSON.stringify({ id: t.id, type: t.type, target: shown, at: t.created_at })) } catch { /* ignore */ }
}
function loadLastTask() {
  try {
    const l = JSON.parse(localStorage.getItem(LAST_KEY) || 'null')
    if (l && l.id && Date.now() - new Date(l.at).getTime() < 7 * 86400e3) lastTask.value = l
  } catch { /* ignore */ }
}
function dismissLastTask() {
  lastTask.value = null
  try { localStorage.removeItem(LAST_KEY) } catch { /* ignore */ }
}

// --- URL → state ---
watch(() => [route.page, route.type, route.taskId], ([page, t, id]) => {
  if (page !== 'probe') return
  if (id) { if (id !== task.value?.id) openTask(id); return }
  if (!t) { navigate('/' + type.value, { replace: true }); return }
  if (t !== type.value) setType(t)
  // /http?target=… prefills the box, e.g. from "发起同类探测".
  if (route.query.target) { target.value = route.query.target; navigate('/' + t, { replace: true }) }
}, { immediate: true })
watch(() => info.value.title, (title) => { if (route.page === 'probe') document.title = `${title} · 拨测平台` })
watch(() => route.page, (page) => { if (page === 'probe') document.title = `${info.value.title} · 拨测平台` }, { immediate: true })

onMounted(() => {
  recentStore.migrateLegacy()
  recent.value = recentStore.list(type.value)
  if (!route.taskId) loadLastTask()
  loadAgents()
  agentTimer = setInterval(loadAgents, 8000)
})
onBeforeUnmount(() => { clearInterval(agentTimer); closeStream() })
</script>

<template>
  <div>
    <div class="notice" v-if="lastTask && !task">
      <span>上次检测：<span class="badge" :class="lastTask.type">{{ TYPE_INFO[lastTask.type]?.label || lastTask.type }}</span> <span class="mono">{{ lastTask.target }}</span> <span class="sub" :title="fmtTime(lastTask.at)">{{ timeAgo(lastTask.at) }}</span></span>
      <span class="spacer"></span>
      <button class="btn sm" type="button" @click="navigate(taskPath(lastTask.id))">继续查看</button>
      <button class="btn sm link" type="button" @click="dismissLastTask">忽略</button>
    </div>

    <div class="card probe-form">
      <div class="row" style="margin-bottom:14px">
        <div class="tabs types" role="tablist" aria-label="检测类型">
          <button v-for="t in PROBE_TYPES" :key="t" type="button" role="tab" :aria-selected="type === t" :class="{ active: type === t }" @click="setType(t)">{{ TYPE_INFO[t].label }}</button>
        </div>
        <span class="sub">{{ info.desc }}</span>
      </div>

      <form class="probe-main" @submit.prevent="run('once')">
        <TargetInput ref="targetBox" v-model="target" :items="recent" :placeholder="info.placeholder" :invalid="!!targetError" @remove="removeRecent" @clear="clearRecent">
          <template #prefix v-if="type === 'http'">
            <div class="tabs sm" role="group" aria-label="协议">
              <button type="button" :class="{ active: scheme === 'https' }" @click="setScheme('https')">HTTPS</button>
              <button type="button" :class="{ active: scheme === 'http' }" @click="setScheme('http')">HTTP</button>
            </div>
          </template>
        </TargetInput>
        <input v-if="type === 'tcping'" class="port-input" type="number" min="1" max="65535" v-model.number="params.tcping.port" title="端口（目标里带了端口时以目标为准）" aria-label="端口" placeholder="端口" />
        <div class="probe-actions">
          <button v-if="running" class="btn stop" type="button" :disabled="stopping" @click="stop">{{ stopping ? '正在停止…' : '停止检测' }}</button>
          <template v-else-if="hasContinuous">
            <button class="btn primary" type="submit">检测</button>
            <button class="btn outline" type="button" @click="run('continuous')" :title="`持续采样 ${cont.count} 次，间隔 ${cont.interval_ms} ms，统计丢包和抖动`">持续检测</button>
          </template>
          <button v-else class="btn primary" type="submit">发起检测</button>
        </div>
      </form>
      <div class="field-error" v-if="targetError" role="alert">{{ targetError }}</div>

      <div class="row probe-opts">
        <span class="sub">{{ info.hint }}<template v-if="hasContinuous"> · 持续检测：{{ cont.count }} 次 · {{ cont.interval_ms }} ms</template></span>
        <span class="spacer"></span>
        <div class="tabs sm" v-if="type === 'http'" role="group" aria-label="请求模式">
          <button type="button" :class="{ active: httpMode === 'simple' }" title="一次普通 GET，只看状态码和耗时" @click="httpMode = 'simple'">简易</button>
          <button type="button" :class="{ active: httpMode === 'full' }" title="自定义方法、次数、断言等完整请求选项" @click="httpMode = 'full'">完整</button>
        </div>
        <div class="tabs sm" role="group" aria-label="协议栈">
          <button v-for="[v, label] in [['', '自动'], ['4', 'IPv4'], ['6', 'IPv6']]" :key="v" type="button" :class="{ active: (params[type].ip_version || '') === v }" @click="params[type].ip_version = v">{{ label }}</button>
        </div>
        <button class="btn sm" type="button" :aria-expanded="showAdvanced" @click="showAdvanced = !showAdvanced">{{ showAdvanced ? '收起选项' : '高级选项' }}</button>
      </div>

      <div class="row" v-if="type === 'http' && httpMode === 'full'" style="margin-top:14px;gap:16px">
        <div class="field"><label>方法</label>
          <select v-model="params.http.method"><option v-for="m in ['GET', 'HEAD', 'POST', 'PUT', 'DELETE', 'OPTIONS']" :key="m">{{ m }}</option></select>
        </div>
        <div class="field"><label>次数</label><input type="number" min="1" max="20" v-model.number="params.http.count" /></div>
        <label class="field inline" style="margin-top:18px"><input type="checkbox" v-model="params.http.follow_redirects" /> 跟随重定向</label>
        <label class="field inline" style="margin-top:18px"><input type="checkbox" v-model="params.http.insecure_tls" /> 忽略证书错误</label>
        <div class="field" style="flex-basis:100%"></div>
        <div class="field"><label>期望状态码（0 = 任意 &lt; 400）</label><input type="number" min="0" max="599" v-model.number="httpExtra.expect_status" /></div>
        <div class="field"><label>响应体包含关键字</label><input type="text" v-model="httpExtra.expect_keyword" placeholder="留空不检查" /></div>
        <div class="field"><label>总耗时上限 (ms，0 不检查)</label><input type="number" min="0" v-model.number="httpExtra.expect_max_ms" /></div>
        <label class="field inline" style="margin-top:18px" v-if="isAdmin"><input type="checkbox" v-model="httpExtra.speed_test" /> 下载测速</label>
        <div class="field" v-if="httpExtra.speed_test"><label>测速时长 (s)</label><input type="number" min="1" max="60" v-model.number="httpExtra.speed_seconds" /></div>
        <div class="field" style="flex-basis:100%"></div>
        <div class="field grow" v-if="isAdmin"><label>自定义 Header（每行一个，Key: Value）</label><textarea rows="2" v-model="httpExtra.headers" placeholder="User-Agent: MyProbe/1.0&#10;Host: example.com" spellcheck="false"></textarea></div>
        <p class="sub" v-else style="margin:0">游客只能发起普通 GET / HEAD 请求，自定义 Header、请求体和忽略证书错误不生效；管理员登录后可用。</p>
        <div class="field grow" v-if="isAdmin && !['GET', 'HEAD'].includes(params.http.method)"><label>请求体</label><textarea rows="2" v-model="httpExtra.body" spellcheck="false"></textarea></div>
      </div>

      <div class="row" v-if="showAdvanced" style="margin-top:14px;gap:16px">
        <template v-if="type === 'ping'">
          <div class="field"><label>次数</label><input type="number" min="1" max="100" v-model.number="params.ping.count" /></div>
          <div class="field"><label>间隔 (ms)</label><input type="number" min="100" max="5000" v-model.number="params.ping.interval_ms" /></div>
          <div class="field"><label>超时 (ms)</label><input type="number" min="200" max="10000" v-model.number="params.ping.timeout_ms" /></div>
          <div class="field"><label>包大小</label><input type="number" min="24" max="1400" v-model.number="params.ping.packet_size" /></div>
        </template>
        <template v-else-if="type === 'tcping'">
          <div class="field"><label>次数</label><input type="number" min="1" max="100" v-model.number="params.tcping.count" /></div>
          <div class="field"><label>间隔 (ms)</label><input type="number" min="100" max="5000" v-model.number="params.tcping.interval_ms" /></div>
          <div class="field"><label>超时 (ms)</label><input type="number" min="200" max="10000" v-model.number="params.tcping.timeout_ms" /></div>
        </template>
        <template v-else-if="type === 'http'">
          <div class="field"><label>超时 (ms)</label><input type="number" min="1000" max="60000" v-model.number="params.http.timeout_ms" /></div>
        </template>
        <template v-else-if="type === 'dns'">
          <div class="field"><label>记录类型</label>
            <select v-model="params.dns.record_type"><option v-for="t in ['A', 'AAAA', 'CNAME', 'MX', 'TXT', 'NS', 'PTR', 'SOA', 'SRV']" :key="t">{{ t }}</option></select>
          </div>
          <div class="field grow"><label>DNS 服务器（留空用节点系统 DNS）</label><input type="text" v-model="params.dns.dns_server" placeholder="223.5.5.5 · 119.29.29.29:53 · tcp://1.1.1.1 · https://doh.pub/dns-query" spellcheck="false" /></div>
          <div class="field"><label>次数</label><input type="number" min="1" max="20" v-model.number="params.dns.count" /></div>
          <div class="field"><label>超时 (ms)</label><input type="number" min="500" max="10000" v-model.number="params.dns.timeout_ms" /></div>
        </template>
        <template v-else>
          <div class="field"><label>探针</label>
            <select v-model="params.mtr.protocol"><option value="icmp">ICMP</option><option value="tcp">TCP SYN</option><option value="udp">UDP</option></select>
          </div>
          <div class="field" v-if="params.mtr.protocol !== 'icmp'"><label>端口</label><input type="number" min="1" max="65535" v-model.number="params.mtr.port" /></div>
          <div class="field"><label>轮数</label><input type="number" min="1" max="100" v-model.number="params.mtr.count" /></div>
          <div class="field"><label>最大跳数</label><input type="number" min="1" max="64" v-model.number="params.mtr.max_hops" /></div>
          <div class="field"><label>每跳超时 (ms)</label><input type="number" min="200" max="5000" v-model.number="params.mtr.timeout_ms" /></div>
          <label class="field inline" style="margin-top:18px"><input type="checkbox" v-model="params.mtr.resolve" /> 反向解析主机名</label>
        </template>
        <template v-if="hasContinuous">
          <div class="field" style="flex-basis:100%"></div>
          <div class="field"><label>持续检测次数</label><input type="number" min="2" max="100" v-model.number="cont.count" /></div>
          <div class="field"><label>持续检测间隔 (ms)</label><input type="number" min="200" max="10000" step="100" v-model.number="cont.interval_ms" /></div>
          <p class="sub" v-if="!isAdmin" style="margin:18px 0 0">游客的持续检测最多 {{ guestContinuous.count }} 次、总时长 {{ guestContinuous.seconds }} 秒，同时只能运行一个。</p>
        </template>
      </div>
    </div>

    <div class="card">
      <div class="row" style="margin-bottom:10px">
        <h3 style="margin:0">节点 <span class="sub">已选 {{ selected.size }} / 在线 {{ onlineAgents.length }}</span></h3>
        <span class="spacer"></span>
        <button class="btn sm" type="button" @click="selectAll(true)">全选</button>
        <button class="btn sm" type="button" @click="selectAll(false)">清空</button>
      </div>
      <div v-if="!agents.length" class="sub">还没有节点接入。到「节点」页查看接入方法。</div>
      <div v-for="[isp, list] in groups" :key="isp" class="row" style="margin-bottom:8px">
        <span class="sub" style="min-width:56px">{{ isp }}</span>
        <div class="agents-pick">
          <span v-for="a in list" :key="a.id" class="chip" :class="{ on: selected.has(a.id), off: !a.online }" @click="toggleAgent(a)" :title="a.online ? '' : '离线'">
            <span class="status-dot" :class="{ online: a.online }"></span>
            {{ a.name }}
            <span class="sub" v-if="a.location || a.geo_location">{{ a.location || a.geo_location }}</span>
          </span>
        </div>
      </div>
    </div>

    <div class="error-box" v-if="error" role="alert">{{ error }}</div>

    <div class="card" v-if="task">
      <TaskResults :task="task" :results="results" :running="running" />
    </div>
    <div class="card empty" v-else-if="opening">正在加载检测结果…</div>
    <div class="card empty" v-else>输入目标并点击「{{ hasContinuous ? '检测' : '发起检测' }}」，各节点结果会实时显示在这里。</div>
  </div>
</template>
