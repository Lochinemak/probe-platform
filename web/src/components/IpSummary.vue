<script setup>
// 响应 IP 汇总: which addresses the nodes got for the target, how many nodes
// got each and from where. Shows at a glance whether a CDN or a split-horizon
// DNS hands different regions different addresses. Clicking an address filters
// the table to the nodes that got it.
import { ref, computed } from 'vue'

const props = defineProps({ rows: Array, selected: { type: String, default: '' } })
const emit = defineEmits(['select'])
const query = ref('')
const expanded = ref(false)
const copied = ref('')
const LIMIT = 8

const groups = computed(() => {
  const m = new Map()
  let total = 0
  for (const r of props.rows) {
    for (const { ip, geo } of r.ips || []) {
      total++
      const g = m.get(ip) || { ip, geo, count: 0, places: new Set(), lines: new Set(), v6: ip.includes(':') }
      g.count++
      if (!g.geo && geo) g.geo = geo
      if (r.placeKey) g.places.add(r.placeKey)
      if (r.line) g.lines.add(r.line)
      m.set(ip, g)
    }
  }
  return [...m.values()].map((g) => ({ ...g, pct: total ? (g.count / total) * 100 : 0, places: [...g.places], lines: [...g.lines] }))
    .sort((a, b) => b.count - a.count || a.ip.localeCompare(b.ip, 'en', { numeric: true }))
})
const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return groups.value
  return groups.value.filter((g) => [g.ip, g.geo, ...g.places, ...g.lines].join(' ').toLowerCase().includes(q))
})
const shown = computed(() => (expanded.value ? filtered.value : filtered.value.slice(0, LIMIT)))
const families = computed(() => {
  const v6 = groups.value.filter((g) => g.v6).length
  return v6 === 0 ? 'IPv4' : v6 === groups.value.length ? 'IPv6' : 'IPv4 + IPv6'
})

async function copy(text, what) {
  try { await navigator.clipboard.writeText(text); copied.value = what; setTimeout(() => { if (copied.value === what) copied.value = '' }, 1500) } catch { copied.value = 'fail' }
}
</script>

<template>
  <div class="ip-summary" v-if="groups.length">
    <div class="row" style="margin-bottom:8px">
      <h4>响应 IP 汇总 <span class="sub">{{ groups.length }} 个地址 · {{ families }}</span></h4>
      <span class="spacer"></span>
      <input v-if="groups.length > LIMIT" type="text" v-model="query" placeholder="搜索 IP、地区或线路" aria-label="筛选响应 IP" style="width:200px" />
      <button class="btn sm" type="button" @click="copy(filtered.map((g) => g.ip).join('\n'), 'all')">{{ copied === 'all' ? '已复制' : `复制当前 ${filtered.length} 个 IP` }}</button>
      <span class="sub bad" v-if="copied === 'fail'">复制失败，请手动选择 IP</span>
    </div>
    <div class="ip-list">
      <div v-for="g in shown" :key="g.ip" class="ip-row" :class="{ on: selected === g.ip }">
        <button type="button" class="ip-main" :aria-pressed="selected === g.ip" :title="selected === g.ip ? '取消筛选' : '只看拿到这个地址的节点'" @click="emit('select', selected === g.ip ? '' : g.ip)">
          <span class="mono">{{ g.ip }}</span>
          <span class="badge" v-if="g.v6">IPv6</span>
          <span class="sub">{{ g.geo || '归属地未知' }}</span>
        </button>
        <span class="ip-share" :title="`${g.count} 个结果，占 ${g.pct.toFixed(1)}%`">
          <span class="bar wide"><i :style="{ width: g.pct + '%' }"></i></span>
          <b>{{ g.count }}</b><span class="sub">（{{ Math.round(g.pct) }}%）</span>
        </span>
        <span class="sub ip-where" :title="[...g.places, ...g.lines].join('、')">{{ g.places.join('、') || '区域未知' }}<template v-if="g.lines.length"> · {{ g.lines.join('、') }}</template></span>
        <button type="button" class="btn link sm" @click="copy(g.ip, g.ip)">{{ copied === g.ip ? '已复制' : '复制' }}</button>
      </div>
      <div v-if="!filtered.length" class="sub">没有匹配的响应 IP</div>
    </div>
    <button v-if="filtered.length > LIMIT" class="btn link sm" type="button" @click="expanded = !expanded">{{ expanded ? '收起' : `查看全部 ${filtered.length} 个` }}</button>
  </div>
</template>
