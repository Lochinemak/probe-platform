<script setup>
// 全球 DNS 扩散检测: after a record is changed, where has the new value
// arrived? The server walks the delegation from the root, asks every name
// server of the zone, then the public resolvers of the directory, and streams
// what it finds. The page only lays the events out.
import { reactive, ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { api, propagation } from '../api.js'
import { route, navigate } from '../route.js'
import { ms } from '../fmt.js'

const props = defineProps({ admin: Boolean })

const TYPES = ['A', 'AAAA', 'CNAME', 'MX', 'TXT', 'NS', 'SOA', 'CAA', 'SRV', 'PTR', 'DS', 'DNSKEY', 'TLSA', 'SVCB', 'HTTPS']
const STAGES = [['expect', '用户预期'], ['delegation', '父区委派'], ['authority', '子区权威'], ['recursive', '全球递归']]
const EXPECT_HINT = {
  A: '例如 203.0.113.10；多个值用逗号分开', AAAA: '例如 2001:db8::10', CNAME: '例如 target.example.net', MX: '例如 10 mail.example.com',
  TXT: '记录内容或其中一段，例如 v=spf1', NS: '例如 ns1.example.net, ns2.example.net',
}

const form = reactive({ name: '', type: 'A', tier: 'quick', expect: '', agent: '' })
const run = reactive({ state: 'idle', request: null, stages: {}, rows: [], summary: null, error: '' })
const filter = ref('')
let stop = null

// Nodes that can run the last step, for the administrator.
const nodes = ref([])
onMounted(async () => {
  if (props.admin) {
    try { nodes.value = ((await api.agents()).agents || []).filter((a) => a.online && (a.capabilities || []).includes('dns_multi')) } catch { /* the server is the vantage point */ }
  }
  const q = route.query
  if (q.name) {
    Object.assign(form, { name: q.name, type: TYPES.includes(q.type) ? q.type : 'A', tier: q.tier === 'full' ? 'full' : 'quick', expect: q.expect || '' })
    start()
  }
})
onBeforeUnmount(() => stop?.())

// The name as the server wants it: trimmed of a pasted URL's scheme and
// path, internationalised names in their xn-- form.
function cleanName(raw) {
  let s = String(raw).trim().replace(/^[a-z][a-z0-9+.-]*:\/\//i, '').replace(/[/?#].*$/, '').replace(/\.$/, '')
  if (/[^\x00-\x7f]/.test(s)) { try { s = new URL('http://' + s).hostname } catch { /* the server will say what is wrong */ } }
  return s
}

function start() {
  const name = cleanName(form.name)
  run.error = ''
  if (!name) { run.error = '请输入域名'; return }
  form.name = name
  stop?.()
  Object.assign(run, { state: 'running', request: null, stages: {}, rows: [], summary: null })
  filter.value = ''
  const params = { name, type: form.type, tier: form.tier, expect: form.expect.trim(), agent: props.admin ? form.agent : '' }
  navigate('/dns-global?' + new URLSearchParams(Object.entries({ name, type: form.type, tier: form.tier, expect: params.expect }).filter(([, v]) => v)).toString(), { replace: true })
  stop = propagation(params, (type, data) => {
    if (type === 'start') run.request = data.request
    else if (type === 'stage') run.stages[data.stage.id] = data.stage
    else if (type === 'row') run.rows.push(data.row)
    else if (type === 'done') { run.summary = data.summary; run.state = 'done'; stop = null }
  }, (e) => { run.error = e.message; run.state = run.rows.length ? 'done' : 'idle'; stop = null })
}
function cancel() { stop?.(); stop = null; run.state = run.rows.length ? 'done' : 'idle'; run.error = '已停止，下面是停止前拿到的结果。' }

const verdictOf = (row) => run.summary?.verdicts?.[row.id] ?? row.verdict ?? ''
const VERDICT = { match: ['一致', 'ok'], differ: ['不同', 'warn'], empty: ['无记录', 'bad'], unknown: ['不可判定', ''] }
const RCODE = { Success: 'NOERROR', NameError: 'NXDOMAIN', ServerFailure: 'SERVFAIL', Refused: 'REFUSED', FormatError: 'FORMERR', NotImplemented: 'NOTIMP' }
const rcode = (r) => RCODE[r.rcode] || r.rcode || ''

const authRows = computed(() => run.rows.filter((r) => r.stage === 'authority').sort((a, b) => a.name.localeCompare(b.name)))
const recAll = computed(() => run.rows.filter((r) => r.stage === 'recursive')
  .sort((a, b) => (b.cc === 'CN') - (a.cc === 'CN') || a.name.localeCompare(b.name, 'zh-CN') || a.addr.localeCompare(b.addr, undefined, { numeric: true })))
const counts = computed(() => {
  const c = { match: 0, differ: 0, empty: 0, unknown: 0 }
  for (const r of recAll.value) { const v = verdictOf(r); if (v in c) c[v]++ }
  return c
})
const recRows = computed(() => (filter.value ? recAll.value.filter((r) => verdictOf(r) === filter.value) : recAll.value))

// What a server said, as short text: the values of the asked type, the alias
// it pointed to, or why there is nothing.
function shown(row) {
  const type = run.request?.type
  const list = (row.answers || []).filter((a) => a.type === type)
  if (list.length) return list.map((a) => ({ value: a.value, geo: a.geo, ttl: a.ttl }))
  return []
}
function reason(row) {
  if (!row.ok) return row.error || '没有应答'
  if (row.cname) return 'CNAME → ' + row.cname
  if (row.rcode === 'NameError') return '域名不存在（NXDOMAIN）'
  if (row.rcode === 'Success') return '没有这个类型的记录'
  return rcode(row)
}
const HOP = { referral: '转介', answer: '权威应答', nxdomain: '权威应答：不存在', odd: '应答异常', error: '失败' }
const REF_SOURCE = { expect: '你填的期望值', authority: '权威服务器的应答', majority: '多数解析器的结果' }
const pct = (v) => (v === undefined ? '—' : v + '%')
const stageState = (id) => run.stages[id]?.state || (run.state === 'running' ? 'waiting' : '')
const STATE_TEXT = { running: '进行中', done: '完成', failed: '失败', skipped: '跳过', waiting: '等待' }
const delegation = computed(() => run.stages.delegation)
const authority = computed(() => run.stages.authority)
const recursive = computed(() => run.stages.recursive)
</script>

<template>
  <div>
    <div class="row" style="margin-bottom:12px">
      <div>
        <h2 style="margin:0">全球 DNS 扩散检测</h2>
        <div class="sub">改了解析记录之后，看新值到了哪里：从根一路查到负责这个域名的区，逐台问它的权威服务器，再问一批公共解析器，和参照值比对。</div>
      </div>
    </div>

    <form class="card" @submit.prevent="start">
      <div class="row" style="align-items:flex-end">
        <div class="field" style="flex:2;min-width:220px"><label for="prop-name">域名</label>
          <input id="prop-name" type="text" v-model="form.name" placeholder="www.example.com" autocomplete="off" autocapitalize="off" spellcheck="false" />
        </div>
        <div class="field"><label for="prop-type">记录类型</label>
          <select id="prop-type" v-model="form.type"><option v-for="t in TYPES" :key="t">{{ t }}</option></select>
        </div>
        <div class="field"><label>解析器范围</label>
          <div class="tabs sm" role="group" aria-label="解析器范围">
            <button type="button" :class="{ active: form.tier === 'quick' }" @click="form.tier = 'quick'">快速</button>
            <button type="button" :class="{ active: form.tier === 'full' }" @click="form.tier = 'full'">完整</button>
          </div>
        </div>
        <div class="field" v-if="admin && nodes.length"><label for="prop-agent">观测点</label>
          <select id="prop-agent" v-model="form.agent">
            <option value="">服务器</option>
            <option v-for="a in nodes" :key="a.id" :value="a.id">节点 {{ a.name }}</option>
          </select>
        </div>
        <button v-if="run.state !== 'running'" class="btn primary" type="submit">开始检测</button>
        <button v-else class="btn" type="button" @click="cancel">停止</button>
      </div>
      <div class="row" style="margin-top:10px;align-items:flex-end">
        <div class="field" style="flex:1;min-width:260px"><label for="prop-expect">期望值 <span class="sub">可选</span></label>
          <input id="prop-expect" type="text" v-model="form.expect" :placeholder="EXPECT_HINT[form.type] || '改动之后应该得到的记录值'" autocomplete="off" spellcheck="false" />
        </div>
      </div>
      <p class="sub" style="margin:10px 0 0">
        查询从{{ form.agent ? '所选节点' : '本平台服务器' }}所在的网络发出，看到的是那里的结果。到境外解析器的查询可能受线路影响，超时会计入「不可判定」，不算作「不同」。
        游客每分钟最多检测 3 次；同样的检测 30 秒内直接返回上一次的结果。<span v-if="admin && form.agent">交给节点的那一步会作为几条「DNS 对比」任务出现在历史里。</span>
      </p>
    </form>

    <div class="error-box" v-if="run.error">{{ run.error }}</div>

    <template v-if="run.state !== 'idle' || run.rows.length">
      <div class="prop-stages">
        <div v-for="([id, label], i) in STAGES" :key="id" class="prop-stage" :class="stageState(id)">
          <div class="row"><b>{{ i + 1 }}. {{ label }}</b><span class="spacer"></span><span class="badge" :class="{ ok: stageState(id) === 'done', bad: stageState(id) === 'failed', running: stageState(id) === 'running' }">{{ STATE_TEXT[stageState(id)] || '—' }}</span></div>
          <div class="sub">{{ run.stages[id]?.note || (id === 'delegation' && stageState(id) === 'running' ? '正在从根服务器往下查…' : '') }}</div>
        </div>
      </div>

      <div class="metrics" v-if="run.summary">
        <div class="metric">
          <div class="stat-label">确认新值率</div>
          <div class="metric-value" :class="{ ok: run.summary.confirm_pct === 100, warn: run.summary.confirm_pct < 100 && run.summary.match > 0, bad: !run.summary.match }">{{ pct(run.summary.confirm_pct) }}</div>
          <div class="sub">{{ run.summary.match }} / {{ run.summary.total }} 个地址与参照一致</div>
        </div>
        <div class="metric">
          <div class="stat-label">可比匹配率</div>
          <div class="metric-value">{{ pct(run.summary.comparable_pct) }}</div>
          <div class="sub">只算给出了可比结果的</div>
        </div>
        <div class="metric">
          <div class="stat-label">响应覆盖率</div>
          <div class="metric-value">{{ pct(run.summary.coverage_pct) }}</div>
          <div class="sub">{{ run.summary.responded }} / {{ run.summary.total }} 个有应答</div>
        </div>
        <div class="metric">
          <div class="stat-label">不可判定</div>
          <div class="metric-value" :class="{ warn: run.summary.unknown > 0 }">{{ run.summary.unknown }}</div>
          <div class="sub">超时或拒绝应答</div>
        </div>
        <div class="metric" style="grid-column: span 2">
          <div class="stat-label">参照值 <span class="sub" v-if="run.summary.ref_source">来自{{ REF_SOURCE[run.summary.ref_source] }}</span></div>
          <div class="mono prop-ref" :title="run.summary.reference.join('\n')">{{ run.summary.reference.length ? run.summary.reference.join('，') : '没有可用的参照值' }}</div>
          <div class="sub">共 {{ run.summary.queries }} 次查询 · {{ (run.summary.elapsed_ms / 1000).toFixed(1) }} 秒</div>
        </div>
      </div>
      <div class="notice" v-for="n in run.summary?.notes || []" :key="n">{{ n }}</div>

      <div class="card" v-if="delegation?.hops?.length">
        <h3 style="margin-top:0">父区委派 <span class="sub" v-if="delegation.zone && delegation.state === 'done'" style="text-transform:none">负责的区：<span class="mono">{{ delegation.zone }}</span></span></h3>
        <div class="table-wrap">
          <table class="grid">
            <thead><tr><th>区</th><th>问的服务器</th><th>地址</th><th>结果</th><th class="num">耗时</th></tr></thead>
            <tbody>
              <tr v-for="(h, i) in delegation.hops" :key="i">
                <td class="mono">{{ h.zone }}</td>
                <td class="mono">{{ h.server }}</td>
                <td class="mono">{{ h.addr }}</td>
                <td style="white-space:normal">
                  <span class="badge" :class="{ ok: h.result === 'answer', bad: h.result === 'error' || h.result === 'odd', warn: h.result === 'nxdomain' }">{{ HOP[h.result] || h.result }}</span>
                  <span v-if="h.result === 'referral'"> 到 <span class="mono">{{ h.next }}</span>，{{ h.ns.length }} 台：<span class="sub mono">{{ h.ns.join('、') }}</span></span>
                  <span v-else-if="h.error" class="sub"> {{ h.error }}</span>
                </td>
                <td class="num">{{ h.rtt_ms ? ms(h.rtt_ms) : '—' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <div class="card" v-if="authRows.length || authority?.state === 'running'">
        <h3 style="margin-top:0">子区权威 <span class="sub" v-if="authority">{{ authRows.length }} / {{ authority.total }} 台已返回</span></h3>
        <p class="sub" v-if="authority?.ns_child?.length && authority.ns_child.join() !== [...authority.ns_parent].sort().join()" style="margin-top:0">
          上级区委派的是 <span class="mono">{{ authority.ns_parent.join('、') }}</span>；本区自己列出的是 <span class="mono">{{ authority.ns_child.join('、') }}</span>。
        </p>
        <div class="table-wrap">
          <table class="grid">
            <thead><tr><th>权威服务器</th><th>地址</th><th>结论</th><th>应答</th><th class="num">SOA 序列号</th><th class="num">耗时</th></tr></thead>
            <tbody>
              <tr v-for="r in authRows" :key="r.id">
                <td class="mono">{{ r.name }}</td>
                <td class="mono">{{ r.addr || '—' }}</td>
                <td><span v-if="VERDICT[verdictOf(r)]" class="badge" :class="VERDICT[verdictOf(r)][1]">{{ VERDICT[verdictOf(r)][0] }}</span><span v-else class="sub">—</span></td>
                <td style="white-space:normal">
                  <div v-for="a in shown(r)" :key="a.value" class="mono">{{ a.value }} <span class="sub" v-if="a.geo">{{ a.geo }}</span></div>
                  <span v-if="!shown(r).length" class="sub">{{ reason(r) }}</span>
                  <span v-if="r.ok && !r.aa" class="badge warn" title="这台服务器的应答没有带权威标志">非权威</span>
                </td>
                <td class="num">{{ r.serial || '—' }}</td>
                <td class="num">{{ r.ok ? ms(r.rtt_ms) : '—' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <div class="card" v-if="recAll.length || recursive?.state === 'running'">
        <div class="row" style="margin-bottom:10px">
          <h3 style="margin:0">全球递归 <span class="sub" v-if="recursive">{{ recAll.length }} / {{ recursive.total }} 个地址已返回</span></h3>
          <span class="spacer"></span>
          <div class="tabs sm" role="group" aria-label="按结论筛选">
            <button type="button" :class="{ active: !filter }" @click="filter = ''">全部 {{ recAll.length }}</button>
            <button v-for="(v, k) in VERDICT" :key="k" type="button" :class="{ active: filter === k }" :disabled="!counts[k]" @click="filter = k">{{ v[0] }} {{ counts[k] }}</button>
          </div>
        </div>
        <div class="row prop-groups" v-if="run.summary?.groups?.length > 1">
          <span class="sub">答案分布</span>
          <span v-for="g in run.summary.groups" :key="g.values.join()" class="badge" :title="g.values.join('\n')">{{ g.count }} 个 · <span class="mono">{{ g.values.slice(0, 2).join('，') }}{{ g.values.length > 2 ? ` 等 ${g.values.length} 条` : '' }}</span></span>
        </div>
        <div class="table-wrap">
          <table class="grid">
            <thead><tr><th>解析器</th><th>地区</th><th>地址</th><th>结论</th><th>应答</th><th class="num">耗时</th></tr></thead>
            <tbody>
              <tr v-for="r in recRows" :key="r.id">
                <td>{{ r.name }} <span class="sub">{{ r.org }}</span></td>
                <td>{{ r.cc === 'CN' ? '中国' : r.cc || '—' }}</td>
                <td class="mono">{{ r.addr }}</td>
                <td><span v-if="VERDICT[verdictOf(r)]" class="badge" :class="VERDICT[verdictOf(r)][1]">{{ VERDICT[verdictOf(r)][0] }}</span><span v-else class="sub">—</span></td>
                <td style="white-space:normal">
                  <div v-for="a in shown(r)" :key="a.value" class="mono">{{ a.value }} <span class="sub" v-if="a.geo">{{ a.geo }}</span></div>
                  <span v-if="!shown(r).length" class="sub">{{ reason(r) }}</span>
                </td>
                <td class="num">{{ r.ok ? ms(r.rtt_ms) : '—' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="empty" v-if="!recRows.length && recAll.length">没有这个结论的解析器。</div>
      </div>
    </template>
  </div>
</template>
