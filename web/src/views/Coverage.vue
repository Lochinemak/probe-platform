<script setup>
// 节点覆盖: where the nodes are and on which lines. Everything is worked out
// here from GET /api/agents; nodes created but never connected are left out.
import { ref, computed, watch, shallowRef, onMounted, onBeforeUnmount } from 'vue'
import { api } from '../api.js'
import { LINES, UNKNOWN_PLACE, isMainland, placeKey, placeOrder } from '../geo.js'
import { MAP_SCOPES, loadMap, regionId } from '../maps.js'
import ChoroplethMap from '../components/ChoroplethMap.vue'

const agents = ref([])
const error = ref('')
const loading = ref(true)
let timer = null
async function load() {
  try {
    agents.value = (await api.agents()).agents.filter((a) => a.online || a.auth !== '')
    error.value = ''
  } catch (e) { if (e.status !== 401) error.value = e.message } finally { loading.value = false }
}
onMounted(() => { load(); timer = setInterval(load, 15000) })
onBeforeUnmount(() => clearInterval(timer))

const lineOf = (a) => a.geo?.line || '其他'
const online = computed(() => agents.value.filter((a) => a.online))
const known = computed(() => agents.value.filter((a) => a.geo?.cc))
const stats = computed(() => {
  const provinces = new Set(online.value.filter((a) => a.geo?.cc === 'CN' && a.geo.province).map((a) => a.geo.province))
  const countries = new Set(online.value.filter((a) => a.geo?.cc && a.geo.cc !== 'CN').map((a) => a.geo.cc))
  const offlineOnly = new Set(known.value.map((a) => placeKey(a.geo)))
  for (const a of online.value) offlineOnly.delete(placeKey(a.geo))
  return {
    provinces: provinces.size, countries: countries.size, online: online.value.length, total: agents.value.length,
    mainland: online.value.filter((a) => isMainland(a.geo)).length, offlineOnly: offlineOnly.size,
    unknown: agents.value.filter((a) => !a.geo?.cc).length,
  }
})

// --- map ---
const scope = ref(MAP_SCOPES[0]?.id || '')
const scopeDef = computed(() => MAP_SCOPES.find((s) => s.id === scope.value))
const map = shallowRef(null)
watch(scope, async (id) => { map.value = null; if (id) { try { const m = await loadMap(id); if (scope.value === id) map.value = m } catch { /* the tables still work */ } } }, { immediate: true })
const picked = ref('') // region id on the current map
watch(scope, () => { picked.value = '' })

function tally(list) {
  const lines = Object.fromEntries(LINES.map((l) => [l, { online: 0, total: 0 }]))
  let on = 0
  for (const a of list) { const l = lines[lineOf(a)]; l.total++; if (a.online) { l.online++; on++ } }
  return { online: on, total: list.length, lines }
}
const mapValues = computed(() => {
  const groups = {}
  for (const a of agents.value) { const id = regionId(scope.value, a.geo); if (id) (groups[id] ||= []).push(a) }
  const out = {}
  for (const [id, list] of Object.entries(groups)) {
    const t = tally(list)
    out[id] = {
      fill: t.online ? 'var(--ok)' : 'var(--warn)',
      title: `在线 ${t.online} / 共 ${t.total} 个节点`,
      lines: LINES.filter((l) => t.lines[l].total).map((l) => [l, `${t.lines[l].online} / ${t.lines[l].total}`, t.lines[l].online ? '' : 'bad']),
    }
  }
  return out
})

// --- side panel: the picked region, or everything ---
const pickedName = computed(() => map.value?.regions.find((r) => r.id === picked.value)?.name || '')
const inPanel = computed(() => (picked.value ? agents.value.filter((a) => regionId(scope.value, a.geo) === picked.value) : agents.value))
const panel = computed(() => tally(inPanel.value))
const panelLines = computed(() => LINES.map((l) => ({ line: l, ...panel.value.lines[l] })).filter((x) => x.total))
const share = (n) => (panel.value.total ? Math.round((n / panel.value.total) * 100) : 0)

// --- ranking ---
const ranking = computed(() => {
  const groups = new Map()
  for (const a of agents.value) { const k = placeKey(a.geo); if (!groups.has(k)) groups.set(k, []); groups.get(k).push(a) }
  return [...groups.entries()].map(([place, list]) => ({ place, ...tally(list) }))
    .sort((a, b) => (a.place === UNKNOWN_PLACE) - (b.place === UNKNOWN_PLACE) || b.online - a.online || b.total - a.total || placeOrder(a.place, b.place))
})
const usedLines = computed(() => LINES.filter((l) => agents.value.some((a) => lineOf(a) === l)))
const totals = computed(() => tally(agents.value))
</script>

<template>
  <div>
    <div class="error-box" v-if="error">{{ error }}</div>
    <div class="row" style="margin-bottom:12px">
      <div>
        <h2 style="margin:0">节点覆盖</h2>
        <div class="sub">节点分布在哪些地区、哪些线路，哪里只剩离线节点</div>
      </div>
      <span class="spacer"></span>
      <div class="tabs sm solid" v-if="MAP_SCOPES.length > 1" role="group" aria-label="地图范围">
        <button v-for="s in MAP_SCOPES" :key="s.id" type="button" :class="{ active: scope === s.id }" @click="scope = s.id">{{ s.label }}</button>
      </div>
      <button class="btn sm" type="button" @click="load">刷新</button>
    </div>

    <div class="stats five">
      <div class="stat"><div class="stat-label">在线节点</div><div class="stat-value ok">{{ stats.online }}<span class="sub"> / {{ stats.total }}</span></div><div class="sub">其中中国大陆 {{ stats.mainland }}</div></div>
      <div class="stat"><div class="stat-label">已覆盖省级行政区</div><div class="stat-value">{{ stats.provinces }}</div><div class="sub">有在线节点，含港澳台</div></div>
      <div class="stat"><div class="stat-label">海外国家或地区</div><div class="stat-value">{{ stats.countries }}</div><div class="sub">有在线节点</div></div>
      <div class="stat" :class="{ 'warn-card': stats.offlineOnly > 0 }"><div class="stat-label">无在线节点的地区</div><div class="stat-value" :class="{ warn: stats.offlineOnly > 0 }">{{ stats.offlineOnly }}</div><div class="sub">有节点但全部离线</div></div>
      <div class="stat" :class="{ 'warn-card': stats.unknown > 0 }"><div class="stat-label">地域未识别</div><div class="stat-value" :class="{ warn: stats.unknown > 0 }">{{ stats.unknown }}</div><div class="sub">可在「节点」页修正</div></div>
    </div>

    <div class="coverage-main">
      <div class="card" v-if="MAP_SCOPES.length">
        <div class="row">
          <h3 style="margin:0">{{ scopeDef?.label }}节点分布</h3>
          <span class="spacer"></span>
          <div class="legend" style="margin:0">
            <span><i style="background:var(--ok)"></i>有在线节点</span>
            <span><i style="background:var(--warn)"></i>节点全部离线</span>
            <span><i style="background:var(--map-empty)"></i>暂无节点</span>
          </div>
        </div>
        <ChoroplethMap v-if="map" :map="map" :values="mapValues" :selected="picked" :label="`${scopeDef.label}地图：各地区的节点数`" @select="picked = $event" />
        <div v-else class="empty">地图加载中…</div>
        <div class="sub map-credit" v-if="map?.credit">{{ map.credit }}</div>
      </div>

      <div class="card">
        <div class="row">
          <h3 style="margin:0">{{ pickedName || '全国 / 全部概览' }}</h3>
          <span class="badge" v-if="!picked">未选择地区</span>
          <span class="spacer"></span>
          <button v-if="picked" class="btn link sm" type="button" @click="picked = ''">查看全部</button>
        </div>
        <p class="sub" v-if="!picked && MAP_SCOPES.length" style="margin:6px 0 0">点击左侧地图上有节点的地区，查看各线路分布情况。</p>
        <div class="panel-kpis">
          <div class="panel-kpi"><span>在线</span><b class="ok">{{ panel.online }}</b></div>
          <div class="panel-kpi"><span>总节点</span><b>{{ panel.total }}</b></div>
          <div class="panel-kpi"><span>涉及线路</span><b>{{ panelLines.length }}</b></div>
        </div>
        <div class="table-wrap">
          <table class="grid">
            <thead><tr><th>线路</th><th class="num">在线 / 总数</th><th>占比</th><th>状态</th></tr></thead>
            <tbody>
              <tr v-for="l in panelLines" :key="l.line">
                <td><b>{{ l.line }}</b></td>
                <td class="num">{{ l.online }} / {{ l.total }}</td>
                <td><span class="bar wide" style="width:90px"><i :style="{ width: share(l.total) + '%' }"></i></span><span class="sub">{{ share(l.total) }}%</span></td>
                <td><span class="badge" :class="l.online ? 'ok' : 'warn'">{{ l.online ? '在线' : '全部离线' }}</span></td>
              </tr>
              <tr v-if="!panelLines.length"><td colspan="4" class="empty">{{ loading ? '加载中…' : '暂无节点' }}</td></tr>
            </tbody>
          </table>
        </div>
        <div class="agents-pick" style="margin-top:12px" v-if="picked">
          <span v-for="a in inPanel" :key="a.id" class="chip" style="cursor:default" :class="{ off: !a.online }"><span class="status-dot" :class="{ online: a.online }"></span>{{ a.name }}<span class="sub">{{ [a.geo?.city, lineOf(a)].filter(Boolean).join(' · ') }}</span></span>
        </div>
      </div>
    </div>

    <div class="card">
      <h3>地区排行 <span class="sub">省份（中国）或国家，按在线节点数</span></h3>
      <div class="table-wrap">
        <table class="grid">
          <thead><tr><th>地区</th><th>状态</th><th class="num">在线 / 总数</th><th v-for="l in usedLines" :key="l" class="num">{{ l }}</th></tr></thead>
          <tbody>
            <tr v-for="r in ranking" :key="r.place">
              <td><b>{{ r.place }}</b></td>
              <td><span class="badge" :class="r.online ? 'ok' : 'warn'">{{ r.online ? '有在线节点' : '全部离线' }}</span></td>
              <td class="num">{{ r.online }} / {{ r.total }}</td>
              <td v-for="l in usedLines" :key="l" class="num" :class="{ sub: !r.lines[l].total }">{{ r.lines[l].total ? `${r.lines[l].online} / ${r.lines[l].total}` : '-' }}</td>
            </tr>
            <tr v-if="!ranking.length"><td :colspan="3 + usedLines.length" class="empty">{{ loading ? '加载中…' : '还没有节点接入' }}</td></tr>
          </tbody>
        </table>
      </div>
    </div>

    <div class="card" v-if="usedLines.length">
      <h3>线路汇总</h3>
      <div class="stats lines">
        <div class="stat" v-for="l in usedLines" :key="l"><div class="stat-label">{{ l }}</div><div class="stat-value">{{ totals.lines[l].online }}<span class="sub"> / {{ totals.lines[l].total }}</span></div><div class="sub">在线 / 总数</div></div>
      </div>
    </div>
  </div>
</template>
