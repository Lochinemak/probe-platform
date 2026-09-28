<script setup>
import { ref, reactive, computed, onMounted, onBeforeUnmount } from 'vue'
import { api, subscribe } from '../api.js'
import TaskResults from '../components/TaskResults.vue'

const props = defineProps({ role: { type: String, default: 'admin' } })
const type = ref('ping')
const target = ref('')
const error = ref('')
const running = ref(false)
const showAdvanced = ref(false)

const params = reactive({
  ping: { count: 10, interval_ms: 500, timeout_ms: 2000, packet_size: 56, ip_version: '' },
  tcping: { port: 80, count: 10, interval_ms: 500, timeout_ms: 3000, ip_version: '' },
  http: { method: 'GET', count: 1, timeout_ms: 10000, follow_redirects: true, insecure_tls: false, ip_version: '' },
  mtr: { count: 10, max_hops: 30, timeout_ms: 1000, resolve: false, ip_version: '', protocol: 'icmp', port: 80 },
  dns: { record_type: 'A', dns_server: '', count: 1, timeout_ms: 3000, ip_version: '' },
})
const httpExtra = reactive({ headers: '', body: '', expect_status: 0, expect_keyword: '', expect_max_ms: 0, speed_test: false, speed_seconds: 5 })
const placeholder = {
  ping: '例如 www.qq.com 或 223.5.5.5',
  tcping: '例如 www.qq.com:443（也可只填域名并在右侧指定端口）',
  http: '例如 https://www.baidu.com/',
  mtr: '例如 www.qq.com 或 1.1.1.1',
  dns: '例如 www.qq.com（在各节点上解析，可指定 DNS 服务器）',
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
    agents.value = list
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

function applyEvent(kind, ev) {
  if (kind === 'snapshot') {
    task.value = ev.task
    results.value = ev.results || []
    if (ev.done) running.value = false
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
  }
}

async function run() {
  error.value = ''
  const t = target.value.trim()
  if (!t) { error.value = '请输入目标'; return }
  if (selected.value.size === 0) { error.value = '请至少选择一个在线节点'; return }
  if (unsubscribe) { unsubscribe(); unsubscribe = null }
  const p = { ...params[type.value] }
  if (type.value === 'http') {
    const headers = {}
    for (const line of httpExtra.headers.split('\n')) {
      const i = line.indexOf(':')
      if (i > 0) headers[line.slice(0, i).trim()] = line.slice(i + 1).trim()
    }
    if (Object.keys(headers).length) p.headers = headers
    if (httpExtra.body && !['GET', 'HEAD'].includes(p.method)) p.body = httpExtra.body
    if (httpExtra.expect_status > 0) p.expect_status = httpExtra.expect_status
    if (httpExtra.expect_keyword) p.expect_keyword = httpExtra.expect_keyword
    if (httpExtra.expect_max_ms > 0) p.expect_max_ms = httpExtra.expect_max_ms
    if (httpExtra.speed_test) { p.speed_test = true; p.speed_seconds = httpExtra.speed_seconds }
  }
  if (type.value === 'mtr' && p.protocol === 'icmp') delete p.port
  for (const k of Object.keys(p)) if (p[k] === '' || p[k] === null || p[k] === false) delete p[k]
  running.value = true
  results.value = []
  try {
    const snap = await api.createTask({ type: type.value, target: t, params: p, agent_ids: [...selected.value] })
    applyEvent('snapshot', snap)
    if (!snap.done) {
      unsubscribe = subscribe(snap.task.id, applyEvent, (e) => { error.value = e.message; running.value = false })
    }
    remember(t)
  } catch (e) {
    running.value = false
    error.value = e.message
  }
}
async function cancel() {
  if (!task.value) return
  try { await api.cancelTask(task.value.id) } catch { /* already finished */ }
}

// --- recent targets ---
const recent = ref([])
function remember(t) {
  const list = [t, ...recent.value.filter((x) => x !== t)].slice(0, 8)
  recent.value = list
  try { localStorage.setItem('probe.recent', JSON.stringify(list)) } catch { /* ignore */ }
}
function pick(t) { target.value = t }

onMounted(() => {
  try { recent.value = JSON.parse(localStorage.getItem('probe.recent') || '[]') } catch { /* ignore */ }
  loadAgents()
  agentTimer = setInterval(loadAgents, 8000)
})
onBeforeUnmount(() => { clearInterval(agentTimer); if (unsubscribe) unsubscribe() })
</script>

<template>
  <div>
    <div class="card">
      <div class="row" style="margin-bottom:14px">
        <div class="tabs">
          <button v-for="t in ['ping', 'tcping', 'http', 'mtr', 'dns']" :key="t" type="button" :class="{ active: type === t }" @click="type = t">{{ t.toUpperCase() }}</button>
        </div>
        <span class="sub" v-if="type === 'ping'">ICMP 延迟与丢包</span>
        <span class="sub" v-else-if="type === 'tcping'">TCP 端口连通性与握手延迟</span>
        <span class="sub" v-else-if="type === 'http'">HTTP(S) 状态码与分阶段耗时</span>
        <span class="sub" v-else-if="type === 'mtr'">逐跳路由追踪，ICMP / TCP / UDP 三种探针（节点需具备 raw socket 权限）</span>
        <span class="sub" v-else>各节点 DNS 解析对比：记录、TTL、RCode、耗时，可指定 DNS 服务器或 DoH</span>
        <span class="spacer"></span>
        <button class="btn sm" type="button" @click="showAdvanced = !showAdvanced">{{ showAdvanced ? '收起参数' : '高级参数' }}</button>
      </div>

      <form class="row" @submit.prevent="run">
        <input class="target-input grow" type="text" v-model="target" :placeholder="placeholder[type]" spellcheck="false" autocomplete="off" />
        <input v-if="type === 'tcping'" type="number" min="1" max="65535" v-model.number="params.tcping.port" title="端口" style="width:100px" placeholder="端口" />
        <button class="btn primary" type="submit" :disabled="running">{{ running ? '进行中…' : '开始拨测' }}</button>
        <button class="btn" type="button" v-if="running" @click="cancel">停止</button>
      </form>

      <div class="row" v-if="recent.length" style="margin-top:8px">
        <span class="sub">最近：</span>
        <button v-for="t in recent" :key="t" class="btn link sm mono" type="button" @click="pick(t)">{{ t }}</button>
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
          <div class="field"><label>方法</label>
            <select v-model="params.http.method"><option v-for="m in ['GET', 'HEAD', 'POST', 'PUT', 'DELETE', 'OPTIONS']" :key="m">{{ m }}</option></select>
          </div>
          <div class="field"><label>次数</label><input type="number" min="1" max="20" v-model.number="params.http.count" /></div>
          <div class="field"><label>超时 (ms)</label><input type="number" min="1000" max="60000" v-model.number="params.http.timeout_ms" /></div>
          <label class="field inline" style="margin-top:18px"><input type="checkbox" v-model="params.http.follow_redirects" /> 跟随重定向</label>
          <label class="field inline" style="margin-top:18px"><input type="checkbox" v-model="params.http.insecure_tls" /> 忽略证书错误</label>
          <div class="field" style="flex-basis:100%"></div>
          <div class="field"><label>期望状态码（0 = 任意 &lt; 400）</label><input type="number" min="0" max="599" v-model.number="httpExtra.expect_status" /></div>
          <div class="field"><label>响应体包含关键字</label><input type="text" v-model="httpExtra.expect_keyword" placeholder="留空不检查" /></div>
          <div class="field"><label>总耗时上限 (ms，0 不检查)</label><input type="number" min="0" v-model.number="httpExtra.expect_max_ms" /></div>
          <label class="field inline" style="margin-top:18px" v-if="props.role === 'admin'"><input type="checkbox" v-model="httpExtra.speed_test" /> 下载测速</label>
          <div class="field" v-if="httpExtra.speed_test"><label>测速时长 (s)</label><input type="number" min="1" max="60" v-model.number="httpExtra.speed_seconds" /></div>
          <div class="field" style="flex-basis:100%"></div>
          <div class="field grow"><label>自定义 Header（每行一个，Key: Value）</label><textarea rows="2" v-model="httpExtra.headers" placeholder="User-Agent: MyProbe/1.0&#10;Host: example.com" spellcheck="false"></textarea></div>
          <div class="field grow" v-if="!['GET', 'HEAD'].includes(params.http.method)"><label>请求体</label><textarea rows="2" v-model="httpExtra.body" spellcheck="false"></textarea></div>
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
        <div class="field"><label>IP 版本</label>
          <select v-model="params[type].ip_version"><option value="">自动</option><option value="4">IPv4</option><option value="6">IPv6</option></select>
        </div>
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

    <div class="error-box" v-if="error">{{ error }}</div>

    <div class="card" v-if="task">
      <TaskResults :task="task" :results="results" :running="running" />
    </div>
    <div class="card empty" v-else>输入目标并点击「开始拨测」，各节点结果会实时显示在这里。</div>
  </div>
</template>
