<script setup>
import { computed } from 'vue'
import { derive } from '../derive.js'
import { typeLabel, fmtTime } from '../fmt.js'
import PingTable from './PingTable.vue'
import HttpTable from './HttpTable.vue'
import MtrView from './MtrView.vue'
import DnsTable from './DnsTable.vue'

const props = defineProps({ task: Object, results: Array, running: Boolean })
const rows = computed(() => (props.results || []).map((r) => derive(props.task, r)))
const doneCount = computed(() => rows.value.filter((r) => r.status === 'done' || r.status === 'error').length)
const okCount = computed(() => rows.value.filter((r) => r.status === 'done').length)
const avgAll = computed(() => {
  const vals = rows.value.map((r) => (props.task.type === 'http' ? r.avg?.total_ms : props.task.type === 'dns' ? r.last?.rtt_ms : r.stats?.avg_ms)).filter((v) => v !== undefined && v !== null && !Number.isNaN(v))
  return vals.length ? vals.reduce((a, b) => a + b, 0) / vals.length : undefined
})
const best = computed(() => {
  const withAvg = rows.value.filter((r) => (props.task.type === 'http' ? r.avg?.total_ms : r.stats?.avg_ms) !== undefined)
  if (!withAvg.length) return null
  return withAvg.reduce((a, b) => ((props.task.type === 'http' ? a.avg.total_ms : a.stats.avg_ms) <= (props.task.type === 'http' ? b.avg.total_ms : b.stats.avg_ms) ? a : b))
})
</script>

<template>
  <div>
    <div class="summary">
      <span><span class="badge" :class="task.type">{{ typeLabel[task.type] }}</span></span>
      <span class="mono">{{ task.target }}</span>
      <span>节点 <b>{{ rows.length }}</b></span>
      <span>完成 <b>{{ doneCount }}</b><template v-if="okCount !== doneCount"> · 成功 <b>{{ okCount }}</b></template></span>
      <span v-if="avgAll !== undefined && task.type !== 'mtr'">全网平均 <b>{{ avgAll.toFixed(1) }} ms</b></span>
      <span v-if="best && task.type !== 'mtr'">最快 <b>{{ best.agent_name }}</b></span>
      <span class="sub">{{ fmtTime(task.created_at) }}</span>
      <span v-if="running"><span class="pulse"></span>实时更新中</span>
    </div>
    <PingTable v-if="task.type === 'ping' || task.type === 'tcping'" :task="task" :results="rows" />
    <HttpTable v-else-if="task.type === 'http'" :task="task" :results="rows" />
    <MtrView v-else-if="task.type === 'mtr'" :task="task" :results="rows" />
    <DnsTable v-else-if="task.type === 'dns'" :task="task" :results="rows" />
  </div>
</template>
