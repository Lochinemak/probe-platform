<script setup>
// Narrowing a result table: by line (电信 / 联通 / …), by region (华东 / …
// / 海外), failures only, plus whatever the response-IP summary or the map
// set. `modelValue` is { line, region, place, ip, failed }; the counts beside
// each choice are of the unfiltered rows.
import { computed } from 'vue'
import { LINES, REGIONS, UNKNOWN_PLACE } from '../geo.js'

const props = defineProps({ rows: Array, modelValue: { type: Object, required: true } })
const emit = defineEmits(['update:modelValue'])
const f = computed(() => props.modelValue)
function set(patch) { emit('update:modelValue', { ...props.modelValue, ...patch }) }

function tally(key) {
  const m = new Map()
  for (const r of props.rows) { const k = key(r); m.set(k, (m.get(k) || 0) + 1) }
  return m
}
const lines = computed(() => {
  const m = tally((r) => r.line || '其他')
  return LINES.filter((l) => m.has(l)).map((l) => ({ value: l, count: m.get(l) }))
})
const regions = computed(() => {
  const m = tally((r) => r.region)
  const order = [...REGIONS.map((r) => r.name), '中国', '海外', UNKNOWN_PLACE]
  return order.filter((n) => m.has(n)).map((n) => ({ value: n, count: m.get(n) }))
})
const failedCount = computed(() => props.rows.filter((r) => r.failed).length)
const active = computed(() => !!(f.value.line || f.value.region || f.value.place || f.value.ip || f.value.failed))
</script>

<template>
  <div class="filters" v-if="rows.length > 1">
    <div class="row" v-if="lines.length > 1 || regions.length > 1 || failedCount">
      <div class="tabs sm" v-if="lines.length > 1" role="group" aria-label="按线路筛选">
        <button type="button" :class="{ active: !f.line }" @click="set({ line: '' })">全部 {{ rows.length }}</button>
        <button v-for="l in lines" :key="l.value" type="button" :class="{ active: f.line === l.value }" @click="set({ line: l.value })">{{ l.value }} {{ l.count }}</button>
      </div>
      <div class="tabs sm" v-if="regions.length > 1" role="group" aria-label="按地区筛选">
        <button type="button" :class="{ active: !f.region }" @click="set({ region: '' })">全部地区</button>
        <button v-for="g in regions" :key="g.value" type="button" :class="{ active: f.region === g.value }" @click="set({ region: g.value })">{{ g.value }} {{ g.count }}</button>
      </div>
      <button v-if="failedCount" type="button" class="chip" :class="{ on: f.failed }" :aria-pressed="!!f.failed" @click="set({ failed: !f.failed })">仅看失败 <span class="sub">{{ failedCount }}</span></button>
    </div>
    <div class="row filter-tags" v-if="active">
      <span class="sub">当前筛选</span>
      <button v-if="f.line" type="button" class="tag" @click="set({ line: '' })">线路：{{ f.line }} ×</button>
      <button v-if="f.region" type="button" class="tag" @click="set({ region: '' })">地区：{{ f.region }} ×</button>
      <button v-if="f.place" type="button" class="tag" @click="set({ place: '' })">地图联动：{{ f.place }} ×</button>
      <button v-if="f.ip" type="button" class="tag" @click="set({ ip: '' })">响应 IP：{{ f.ip }} ×</button>
      <button v-if="f.failed" type="button" class="tag" @click="set({ failed: false })">仅看失败 ×</button>
      <button type="button" class="btn link sm" @click="set({ line: '', region: '', place: '', ip: '', failed: false })">清除全部</button>
    </div>
  </div>
</template>
