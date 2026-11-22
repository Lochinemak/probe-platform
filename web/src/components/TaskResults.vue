<script setup>
// One task's result: title and actions, summary cards, the latency legend,
// the response-IP summary, filters, then the table for the probe type. The
// filters live here so every part works on the same narrowed rows.
import { computed, ref, watch } from 'vue'
import { derive } from '../derive.js'
import { latencyScale } from '../fmt.js'
import ResultHeader from './ResultHeader.vue'
import MetricCards from './MetricCards.vue'
import IpSummary from './IpSummary.vue'
import ResultFilters from './ResultFilters.vue'
import PingTable from './PingTable.vue'
import HttpTable from './HttpTable.vue'
import MtrView from './MtrView.vue'
import DnsTable from './DnsTable.vue'

const props = defineProps({ task: Object, results: Array, running: Boolean, zen: Boolean })
const emit = defineEmits(['update:zen'])

const rows = computed(() => (props.results || []).map((r) => derive(props.task, r)))

const NO_FILTER = { line: '', region: '', place: '', ip: '', failed: false }
const filter = ref({ ...NO_FILTER })
watch(() => props.task?.id, () => { filter.value = { ...NO_FILTER } })
const filtered = computed(() => {
  const f = filter.value
  return rows.value.filter((r) => (!f.line || (r.line || '其他') === f.line) && (!f.region || r.region === f.region) && (!f.place || r.placeKey === f.place)
    && (!f.ip || (r.ips || []).some((x) => x.ip === f.ip)) && (!f.failed || r.failed))
})
const scale = computed(() => latencyScale(props.task.type))
const isHTTP = computed(() => props.task.type === 'http')
// What the table is showing, in its own order, for the exports.
const shown = ref([])
</script>

<template>
  <div>
    <ResultHeader :task="task" :rows="shown.length ? shown : filtered" :results="results" :running="running" :zen="zen" @update:zen="emit('update:zen', $event)" />
    <MetricCards :task="task" :rows="filtered" />
    <div class="legend lat-legend" aria-label="延迟色阶">
      <span class="sub">{{ isHTTP ? '请求耗时' : '延迟' }}</span>
      <span v-for="(label, i) in scale.labels" :key="i"><i :class="'lat-bg-' + i"></i>{{ label }}</span>
      <span><i class="lat-bg-x"></i>超时 / 无数据</span>
      <span class="sub" v-if="isHTTP">HTTP 耗时是整个请求（解析、连接、TLS、等待首字节、下载）的时间，不是链路往返延迟。</span>
    </div>
    <IpSummary v-if="task.type !== 'mtr'" :rows="rows" :selected="filter.ip" @select="filter = { ...filter, ip: $event }" />
    <ResultFilters v-model="filter" :rows="rows" />
    <div class="empty" v-if="rows.length && !filtered.length">没有符合当前筛选的节点。</div>
    <template v-else>
      <PingTable v-if="task.type === 'ping' || task.type === 'tcping'" :task="task" :results="filtered" @order="shown = $event" />
      <HttpTable v-else-if="task.type === 'http'" :task="task" :results="filtered" @order="shown = $event" />
      <MtrView v-else-if="task.type === 'mtr'" :task="task" :results="filtered" />
      <DnsTable v-else-if="task.type === 'dns'" :task="task" :results="filtered" @order="shown = $event" />
    </template>
  </div>
</template>
