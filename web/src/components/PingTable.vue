<script setup>
import { ref, computed } from 'vue'
import { ms, pct, lossClass, rttClass, statusLabel, agentPlace } from '../fmt.js'
import Sparkline from './Sparkline.vue'
import ErrorBadge from './ErrorBadge.vue'

const props = defineProps({ task: Object, results: Array })
const sortKey = ref('')
const sortDir = ref(1)
function sortBy(k) {
  if (sortKey.value === k) sortDir.value = -sortDir.value
  else { sortKey.value = k; sortDir.value = 1 }
}
const rows = computed(() => {
  const arr = [...props.results]
  if (!sortKey.value) return arr
  const k = sortKey.value
  return arr.sort((a, b) => {
    const av = a.stats?.[k], bv = b.stats?.[k]
    if (av === undefined && bv === undefined) return 0
    if (av === undefined) return 1
    if (bv === undefined) return -1
    return (av - bv) * sortDir.value
  })
})
const count = computed(() => props.task.params?.count || 10)
</script>

<template>
  <div class="table-wrap">
    <table class="grid">
      <thead>
        <tr>
          <th>节点</th>
          <th>位置 / 运营商</th>
          <th>目标 IP</th>
          <th class="num">发送/接收</th>
          <th class="num sortable" @click="sortBy('loss_pct')">丢包 {{ sortKey === 'loss_pct' ? (sortDir > 0 ? '↑' : '↓') : '' }}</th>
          <th class="num sortable" @click="sortBy('min_ms')">最小</th>
          <th class="num sortable" @click="sortBy('avg_ms')">平均 {{ sortKey === 'avg_ms' ? (sortDir > 0 ? '↑' : '↓') : '' }}</th>
          <th class="num sortable" @click="sortBy('max_ms')">最大</th>
          <th class="num sortable" @click="sortBy('stddev_ms')">抖动</th>
          <th>趋势</th>
          <th>状态</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in rows" :key="r.agent_id">
          <td><b>{{ r.agent_name }}</b></td>
          <td class="sub">{{ agentPlace(r) || '-' }}</td>
          <td class="mono">{{ r.ip || '-' }}<span v-if="r.port" class="sub">:{{ r.port }}</span></td>
          <td class="num">{{ r.stats?.sent ?? 0 }}/{{ r.stats?.received ?? 0 }}</td>
          <td class="num" :class="lossClass(r.stats?.loss_pct)">{{ r.stats?.sent ? pct(r.stats.loss_pct) : '-' }}</td>
          <td class="num">{{ r.stats?.received ? ms(r.stats.min_ms) : '-' }}</td>
          <td class="num" :class="rttClass(r.stats?.avg_ms)"><b>{{ r.stats?.received ? ms(r.stats.avg_ms) : '-' }}</b></td>
          <td class="num">{{ r.stats?.received ? ms(r.stats.max_ms) : '-' }}</td>
          <td class="num">{{ r.stats?.received ? ms(r.stats.stddev_ms) : '-' }}</td>
          <td><Sparkline :replies="r.replies || []" :total="r.live ? count : 0" /></td>
          <td>
            <span v-if="r.status === 'running'" class="badge running"><span class="pulse"></span>{{ statusLabel(r.status) }}</span>
            <ErrorBadge v-else-if="r.status === 'error'" :message="r.error || '失败'" />
            <span v-else class="badge" :class="r.status">{{ statusLabel(r.status) }}</span>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
