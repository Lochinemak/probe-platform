<script setup>
// Multi-series line chart on uPlot. One y-axis per chart (never dual axis):
// the caller renders latency and loss as two charts. Hover shows a crosshair
// with every series' value in the legend row (uPlot's built-in legend).
import { ref, watch, onMounted, onBeforeUnmount } from 'vue'
import uPlot from 'uplot'
import 'uplot/dist/uPlot.min.css'
import { chrome, seriesColor, isDark } from '../palette.js'

const props = defineProps({
  // [{ id, label, points: [[tMs, value], ...] }]
  series: { type: Array, default: () => [] },
  unit: { type: String, default: 'ms' },
  height: { type: Number, default: 220 },
  title: { type: String, default: '' },
  hidden: { type: Object, default: () => ({}) }, // id -> true to hide
})
const el = ref(null)
let plot = null
let ro = null

function fmtVal(v) {
  if (v === null || v === undefined || Number.isNaN(v)) return '-'
  if (props.unit === '%') return v.toFixed(0) + '%'
  return v >= 100 ? Math.round(v) + ' ms' : v.toFixed(1) + ' ms'
}

function build() {
  if (!el.value) return
  destroy()
  const dark = isDark()
  const c = chrome[dark ? 'dark' : 'light']
  const xs = [...new Set(props.series.flatMap((s) => s.points.map((p) => p[0])))].sort((a, b) => a - b)
  const idx = new Map(xs.map((t, i) => [t, i]))
  const data = [xs.map((t) => t / 1000)]
  const seriesOpts = [{}]
  props.series.forEach((s, i) => {
    const ys = new Array(xs.length).fill(null)
    for (const [t, v] of s.points) ys[idx.get(t)] = v === null || v < 0 ? null : v
    data.push(ys)
    seriesOpts.push({
      label: s.label,
      stroke: seriesColor(i),
      width: 2,
      spanGaps: false,
      points: { show: xs.length <= 60, size: 6 },
      show: !props.hidden[s.id],
      value: (u, v) => fmtVal(v),
    })
  })
  const width = el.value.clientWidth || 600
  plot = new uPlot({
    width,
    height: props.height,
    title: props.title || undefined,
    cursor: { drag: { x: true, y: false }, points: { size: 8 } },
    legend: { live: true },
    scales: { x: { time: true }, y: { range: (u, min, max) => [0, Math.max(props.unit === '%' ? 100 : 1, max * 1.1)] } },
    axes: [
      { stroke: c.muted, grid: { stroke: c.grid, width: 1 }, ticks: { stroke: c.axis, width: 1 }, font: '11px system-ui' },
      {
        stroke: c.muted, grid: { stroke: c.grid, width: 1 }, ticks: { show: false }, font: '11px system-ui', size: 54,
        values: (u, vals) => vals.map((v) => (props.unit === '%' ? v + '%' : v >= 1000 ? (v / 1000).toFixed(1) + 's' : v + 'ms')),
      },
    ],
    series: seriesOpts,
  }, data, el.value)
}
function destroy() { if (plot) { plot.destroy(); plot = null } }

onMounted(() => {
  build()
  ro = new ResizeObserver(() => { if (plot && el.value) plot.setSize({ width: el.value.clientWidth, height: props.height }) })
  ro.observe(el.value)
  const mq = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)')
  if (mq) mq.addEventListener('change', build)
})
onBeforeUnmount(() => { destroy(); if (ro) ro.disconnect() })
watch(() => [props.series, props.hidden], build, { deep: true })
</script>

<template>
  <div class="chart" ref="el"></div>
</template>
