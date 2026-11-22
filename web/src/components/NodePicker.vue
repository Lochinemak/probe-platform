<script setup>
// Which nodes a probe runs on. Three ways to say it:
//   全部节点  every usable node (optionally one kind of line)
//   按地区    country → province → city, taken from where the nodes actually are
//   手动选择  the chips themselves
// Clicking a chip in the first two modes switches to manual with the current
// selection as the starting point. The parent still gets a plain Set of ids.
import { reactive, computed, watch } from 'vue'
import { LINES, UNKNOWN_PLACE, placeKey, placeLabel, placeOrder } from '../geo.js'

const props = defineProps({
  agents: { type: Array, default: () => [] },
  modelValue: { type: Set, default: () => new Set() },
  type: { type: String, default: 'ping' },
  ipVersion: { type: String, default: '' },
})
const emit = defineEmits(['update:modelValue'])

// What a node must be able to do for the current probe. A node that reports
// no capabilities at all predates the list and is taken at its word.
const NEEDS = {
  ping: ['icmp', '没有 ICMP 权限，不能 Ping'],
  mtr: ['mtr', '没有 raw socket 权限，不能做路由分析'],
  tcping: ['tcp', '不支持 TCP 检测'],
  http: ['http', '不支持 HTTP 检测'],
}
function blocker(a) {
  if (!a.online) return '离线'
  const caps = a.capabilities || []
  if (!caps.length) return ''
  const need = NEEDS[props.type]
  if (need && !caps.includes(need[0])) return need[1]
  if (props.ipVersion === '6' && !caps.includes('ipv6')) return '没有 IPv6 出口'
  return ''
}
const usable = computed(() => props.agents.filter((a) => !blocker(a)))
const onlineCount = computed(() => props.agents.filter((a) => a.online).length)

// --- scope, remembered between visits ---
const KEY = 'probe.scope'
const scope = reactive({ mode: 'all', cc: '', province: '', city: '', line: '', ids: [] })
try {
  const saved = JSON.parse(localStorage.getItem(KEY) || 'null')
  if (saved && ['all', 'region', 'manual'].includes(saved.mode)) Object.assign(scope, saved, { ids: Array.isArray(saved.ids) ? saved.ids : [] })
} catch { /* start with everything */ }
watch(scope, (s) => { try { localStorage.setItem(KEY, JSON.stringify(s)) } catch { /* ignore */ } }, { deep: true })

const ccOf = (a) => a.geo?.cc || ''
function options(list, key, label) {
  const m = new Map()
  for (const a of list) {
    const k = key(a)
    if (!k) continue
    const o = m.get(k) || { value: k, label: label(a), count: 0 }
    o.count++
    m.set(k, o)
  }
  return [...m.values()]
}
const countryOptions = computed(() => options(usable.value, ccOf, (a) => a.geo.country || a.geo.cc)
  .sort((x, y) => (y.value === 'CN') - (x.value === 'CN') || x.label.localeCompare(y.label, 'zh-CN')))
const unknownCount = computed(() => usable.value.filter((a) => !ccOf(a)).length)
const inCountry = computed(() => usable.value.filter((a) => (scope.cc === '-' ? !ccOf(a) : ccOf(a) === scope.cc)))
const provinceOptions = computed(() => options(inCountry.value, (a) => a.geo?.province, (a) => a.geo.province).sort((x, y) => placeOrder(x.value, y.value)))
const inProvince = computed(() => (scope.province ? inCountry.value.filter((a) => a.geo?.province === scope.province) : inCountry.value))
const cityOptions = computed(() => options(inProvince.value, (a) => a.geo?.city, (a) => a.geo.city))

// Keep the cascade consistent: pick a country when none is chosen, and drop a
// narrower choice its parent no longer contains.
watch([() => scope.mode, countryOptions, unknownCount], () => {
  if (scope.mode !== 'region') return
  const known = countryOptions.value.some((o) => o.value === scope.cc) || (scope.cc === '-' && unknownCount.value)
  if (!known && countryOptions.value.length) { scope.cc = countryOptions.value[0].value; scope.province = ''; scope.city = '' }
}, { immediate: true })
function setCountry(cc) { scope.cc = cc; scope.province = ''; scope.city = '' }
function setProvince(p) { scope.province = p; scope.city = '' }

// Nodes the place part of the scope lets through; the line filter narrows them.
const placed = computed(() => {
  if (scope.mode !== 'region') return usable.value
  return scope.city ? inProvince.value.filter((a) => a.geo?.city === scope.city) : inProvince.value
})
const lineOptions = computed(() => {
  const counts = new Map()
  for (const a of placed.value) { const l = a.geo?.line || '其他'; counts.set(l, (counts.get(l) || 0) + 1) }
  return LINES.filter((l) => counts.has(l)).map((l) => ({ value: l, count: counts.get(l) }))
})
watch(lineOptions, (opts) => { if (scope.line && !opts.some((o) => o.value === scope.line) && props.agents.length) scope.line = '' })
const matched = computed(() => (scope.line ? placed.value.filter((a) => (a.geo?.line || '其他') === scope.line) : placed.value))

// The selection handed to the parent.
const selectedIds = computed(() => {
  if (scope.mode !== 'manual') return matched.value.map((a) => a.id)
  const ok = new Set(usable.value.map((a) => a.id))
  return scope.ids.filter((id) => ok.has(id))
})
watch(selectedIds, (ids) => {
  const cur = props.modelValue
  if (ids.length === cur.size && ids.every((id) => cur.has(id))) return
  emit('update:modelValue', new Set(ids))
}, { immediate: true })

function setMode(m) {
  if (m === 'manual' && scope.mode !== 'manual') scope.ids = [...selectedIds.value]
  scope.mode = m
}
function toggle(a) {
  if (blocker(a)) return
  const ids = new Set(selectedIds.value)
  if (ids.has(a.id)) ids.delete(a.id); else ids.add(a.id)
  scope.ids = [...ids]
  scope.mode = 'manual'
}
function selectAll(on) { scope.ids = on ? usable.value.map((a) => a.id) : []; scope.mode = 'manual' }

// Chips grouped by where the node is.
const groups = computed(() => {
  const m = new Map()
  for (const a of props.agents) {
    const k = placeKey(a.geo)
    if (!m.has(k)) m.set(k, [])
    m.get(k).push(a)
  }
  return [...m.entries()].sort((x, y) => placeOrder(x[0], y[0]))
})
function chipSub(a) {
  const g = a.geo
  const place = g?.cc ? (g.cc === 'CN' ? g.city : g.city || g.province) : (a.location || '')
  return [place, g?.line && g.line !== '其他' ? g.line : (a.isp || '')].filter(Boolean).join(' · ')
}
const selected = computed(() => new Set(selectedIds.value))
</script>

<template>
  <div class="card">
    <div class="row" style="margin-bottom:10px">
      <h3 style="margin:0">节点 <span class="sub">将使用 {{ selectedIds.length }} 个 / 在线 {{ onlineCount }}</span></h3>
      <span class="spacer"></span>
      <div class="tabs sm" role="group" aria-label="节点范围">
        <button type="button" :class="{ active: scope.mode === 'all' }" @click="setMode('all')">全部节点</button>
        <button type="button" :class="{ active: scope.mode === 'region' }" @click="setMode('region')">按地区</button>
        <button type="button" :class="{ active: scope.mode === 'manual' }" @click="setMode('manual')">手动选择</button>
      </div>
    </div>

    <div class="row scope-row" v-if="scope.mode === 'region' && (countryOptions.length || unknownCount)">
      <div class="field"><label>节点所在国家或地区</label>
        <select :value="scope.cc" @change="setCountry($event.target.value)">
          <option v-for="o in countryOptions" :key="o.value" :value="o.value">{{ o.label }}（{{ o.count }}）</option>
          <option v-if="unknownCount" value="-">{{ UNKNOWN_PLACE }}（{{ unknownCount }}）</option>
        </select>
      </div>
      <div class="field" v-if="provinceOptions.length"><label>{{ scope.cc === 'CN' ? '省份' : '省 / 州' }}</label>
        <select :value="scope.province" @change="setProvince($event.target.value)">
          <option value="">全部（{{ inCountry.length }}）</option>
          <option v-for="o in provinceOptions" :key="o.value" :value="o.value">{{ o.label }}（{{ o.count }}）</option>
        </select>
      </div>
      <div class="field" v-if="scope.province && cityOptions.length"><label>城市</label>
        <select v-model="scope.city">
          <option value="">全部（{{ inProvince.length }}）</option>
          <option v-for="o in cityOptions" :key="o.value" :value="o.value">{{ o.label }}（{{ o.count }}）</option>
        </select>
      </div>
      <p class="sub" style="margin:18px 0 0">这里选的是节点所在的位置，不是目标的归属地。</p>
    </div>

    <div class="row scope-row" v-if="scope.mode !== 'manual' && lineOptions.length > 1">
      <span class="sub">线路</span>
      <div class="agents-pick">
        <button type="button" class="chip" :class="{ on: !scope.line }" :aria-pressed="!scope.line" @click="scope.line = ''">全部线路 <span class="sub">{{ placed.length }}</span></button>
        <button v-for="o in lineOptions" :key="o.value" type="button" class="chip" :class="{ on: scope.line === o.value }" :aria-pressed="scope.line === o.value" @click="scope.line = o.value">{{ o.value }} <span class="sub">{{ o.count }}</span></button>
      </div>
    </div>
    <div class="row scope-row" v-if="scope.mode === 'manual'">
      <span class="sub">点下面的节点逐个增减</span>
      <button class="btn sm" type="button" @click="selectAll(true)">全选</button>
      <button class="btn sm" type="button" @click="selectAll(false)">清空</button>
    </div>

    <div v-if="!agents.length" class="sub">还没有节点接入。到「节点」页查看接入方法。</div>
    <div v-else-if="!selectedIds.length" class="sub" style="margin-bottom:8px">
      {{ usable.length ? '当前范围内没有可用的在线节点。' : '没有可用的在线节点。' }}
      <button v-if="usable.length && scope.mode !== 'all'" class="btn link sm" type="button" @click="scope.line = ''; setMode('all')">改用全部节点</button>
    </div>
    <div v-for="[place, list] in groups" :key="place" class="row" style="margin-bottom:8px">
      <span class="sub place">{{ place }}</span>
      <div class="agents-pick">
        <button v-for="a in list" :key="a.id" type="button" class="chip" :class="{ on: selected.has(a.id), off: !!blocker(a) }" :aria-pressed="selected.has(a.id)" :aria-disabled="!!blocker(a)"
          :title="blocker(a) || placeLabel(a.geo) || a.location || ''" @click="toggle(a)">
          <span class="status-dot" :class="{ online: a.online }"></span>
          {{ a.name }}
          <span class="sub" v-if="blocker(a) && a.online">{{ blocker(a) }}</span>
          <span class="sub" v-else-if="chipSub(a)">{{ chipSub(a) }}</span>
        </button>
      </div>
    </div>
  </div>
</template>
