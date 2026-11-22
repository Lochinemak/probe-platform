<script setup>
// The result on a map: each province (中国) or country (全球) coloured by the
// average latency of the nodes there, on the same scale as the table. Clicking
// a region narrows the table to it.
import { ref, computed, watch, shallowRef } from 'vue'
import { dur, bucketOf } from '../fmt.js'
import { MAP_SCOPES, loadMap, regionId } from '../maps.js'
import ChoroplethMap from './ChoroplethMap.vue'

const props = defineProps({
  task: Object,
  rows: Array,
  selected: { type: Object, default: null }, // { by, id, name } as set by this map
})
const emit = defineEmits(['select'])

// Start on the scope most of the nodes are on.
const scope = ref('')
function bestScope() {
  if (!MAP_SCOPES.length) return ''
  const inChina = props.rows.filter((r) => r.geo?.cc === 'CN').length
  const abroad = props.rows.filter((r) => r.geo?.cc && r.geo.cc !== 'CN').length
  const want = inChina >= abroad ? 'china' : 'world'
  return (MAP_SCOPES.find((s) => s.id === want) || MAP_SCOPES[0]).id
}
watch(() => props.task?.id, () => { scope.value = bestScope() }, { immediate: true })
watch(() => props.rows.length, () => { if (!touched) scope.value = bestScope() })
let touched = false
function setScope(id) { touched = true; scope.value = id; if (props.selected) emit('select', null) }
const scopeDef = computed(() => MAP_SCOPES.find((s) => s.id === scope.value))

const map = shallowRef(null)
const failed = ref(false)
watch(scope, async (id) => {
  map.value = null
  failed.value = false
  if (!id) return
  try { const m = await loadMap(id); if (scope.value === id) map.value = m } catch { failed.value = true }
}, { immediate: true })

// With a couple of nodes a map is mostly empty space: it starts folded.
const open = ref(null)
const isOpen = computed(() => (open.value === null ? props.rows.length > 2 : open.value))

const values = computed(() => {
  const groups = {}
  for (const r of props.rows) {
    const id = regionId(scope.value, r.geo)
    if (id) (groups[id] ||= []).push(r)
  }
  const out = {}
  for (const [id, list] of Object.entries(groups)) {
    const measured = list.filter((r) => r.metric !== undefined && r.metric !== null && !Number.isNaN(r.metric))
    const avg = measured.length ? measured.reduce((s, r) => s + r.metric, 0) / measured.length : undefined
    const b = bucketOf(props.task.type, avg)
    out[id] = {
      fill: b < 0 ? 'var(--lat-x)' : `var(--lat-${b})`,
      title: `${list.length} 个节点` + (avg !== undefined ? `，平均 ${dur(avg)}` : list.some((r) => r.live) ? '，检测中' : '，无数据'),
      lines: list.slice(0, 8).map((r) => [r.agent_name + (r.line ? ` · ${r.line}` : ''), r.metric !== undefined ? dur(r.metric) : r.statusText, r.failed ? 'bad' : '']),
    }
  }
  return out
})
const offMap = computed(() => props.rows.filter((r) => !regionId(scope.value, r.geo)).length)
const selectedId = computed(() => (props.selected && props.selected.by === scopeDef.value?.by ? props.selected.id : ''))
function select(id) {
  const region = map.value?.regions.find((r) => r.id === id)
  emit('select', id ? { by: scopeDef.value.by, id, name: region?.name || id } : null)
}
</script>

<template>
  <div class="result-map" v-if="MAP_SCOPES.length">
    <div class="row">
      <button class="btn sm" type="button" :aria-expanded="isOpen" @click="open = !isOpen">{{ isOpen ? '收起地图' : '显示地图' }}</button>
      <template v-if="isOpen">
        <div class="tabs sm" v-if="MAP_SCOPES.length > 1" role="group" aria-label="地图范围">
          <button v-for="s in MAP_SCOPES" :key="s.id" type="button" :class="{ active: scope === s.id }" @click="setScope(s.id)">{{ s.label }}</button>
        </div>
        <span class="sub">按节点所在{{ scopeDef?.by === 'cc' ? '国家或地区' : '省份' }}着色，点击筛选下面的表格</span>
        <span class="sub" v-if="offMap">· {{ offMap }} 个节点不在这张图上{{ scope === 'china' ? '（境外或地域未识别）' : '（地域未识别）' }}</span>
      </template>
    </div>
    <template v-if="isOpen">
      <ChoroplethMap v-if="map" :map="map" :values="values" :selected="selectedId" :label="`${scopeDef.label}地图：各地区的检测结果`" @select="select" />
      <div v-else-if="failed" class="sub" style="padding:16px 0">地图加载失败</div>
      <div v-else class="sub" style="padding:16px 0">地图加载中…</div>
      <div class="sub map-credit" v-if="map?.credit">{{ map.credit }}</div>
    </template>
  </div>
</template>
