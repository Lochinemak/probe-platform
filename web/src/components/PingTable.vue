<script setup>
import { computed, watch } from 'vue'
import { ms, pct, lossClass, bucketClass } from '../fmt.js'
import { useSort } from '../useSort.js'
import Sparkline from './Sparkline.vue'
import SortTh from './SortTh.vue'
import StatusCell from './StatusCell.vue'

const props = defineProps({ task: Object, results: Array })
const emit = defineEmits(['order'])
const got = (r) => r.stats?.received > 0
const sort = useSort(() => props.results, {
  name: (r) => r.agent_name, place: (r) => r.place, line: (r) => r.line, ip: (r) => r.ip,
  loss: (r) => (r.stats?.sent ? r.stats.loss_pct : undefined),
  latest: (r) => r.latest, min: (r) => (got(r) ? r.stats.min_ms : undefined), avg: (r) => (got(r) ? r.stats.avg_ms : undefined),
  max: (r) => (got(r) ? r.stats.max_ms : undefined), p95: (r) => r.p95, stddev: (r) => (got(r) ? r.stats.stddev_ms : undefined),
})
const rows = sort.sorted
watch(rows, (v) => emit('order', v), { immediate: true })
const count = computed(() => props.task.params?.count || 10)
// A continuous run is about stability over time: it gets the latest sample
// and the 95th percentile next to the usual figures.
const continuous = computed(() => !!props.task.params?.continuous)
</script>

<template>
  <div class="table-wrap">
    <table class="grid">
      <thead>
        <tr>
          <SortTh :sort="sort" k="name" class="col-name">检测点</SortTh>
          <SortTh :sort="sort" k="place">地域</SortTh>
          <SortTh :sort="sort" k="line">线路</SortTh>
          <SortTh :sort="sort" k="ip" class="col-ip">响应 IP</SortTh>
          <th class="num">发送/接收</th>
          <SortTh :sort="sort" k="loss" num>丢包率</SortTh>
          <SortTh v-if="continuous" :sort="sort" k="latest" num>最新</SortTh>
          <SortTh :sort="sort" k="min" num>最快</SortTh>
          <SortTh :sort="sort" k="avg" num>平均</SortTh>
          <SortTh :sort="sort" k="max" num>最慢</SortTh>
          <SortTh v-if="continuous" :sort="sort" k="p95" num title="95% 的样本不超过这个值，比平均值更能反映稳定体验">95% 延迟</SortTh>
          <SortTh :sort="sort" k="stddev" num>抖动</SortTh>
          <th>质量走势</th>
          <th>状态</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in rows" :key="r.agent_id">
          <td class="col-name"><b>{{ r.agent_name }}</b></td>
          <td class="sub">{{ r.place || '-' }}</td>
          <td><span class="badge" v-if="r.line">{{ r.line }}</span><span v-else class="sub">-</span></td>
          <td class="mono col-ip">{{ r.ip || '-' }}<span v-if="r.port" class="sub">:{{ r.port }}</span><div class="sub ipgeo" v-if="r.ipGeo">{{ r.ipGeo }}</div></td>
          <td class="num">{{ r.stats?.sent ?? 0 }}/{{ r.stats?.received ?? 0 }}</td>
          <td class="num" :class="lossClass(r.stats?.loss_pct)">{{ r.stats?.sent ? pct(r.stats.loss_pct) : '-' }}</td>
          <td class="num" v-if="continuous" :class="bucketClass(task.type, r.latest)">{{ ms(r.latest) }}</td>
          <td class="num">{{ got(r) ? ms(r.stats.min_ms) : '-' }}</td>
          <td class="num" :class="bucketClass(task.type, got(r) ? r.stats.avg_ms : undefined)"><b>{{ got(r) ? ms(r.stats.avg_ms) : '-' }}</b></td>
          <td class="num">{{ got(r) ? ms(r.stats.max_ms) : '-' }}</td>
          <td class="num" v-if="continuous" :class="bucketClass(task.type, r.p95)">{{ ms(r.p95) }}</td>
          <td class="num">{{ got(r) ? ms(r.stats.stddev_ms) : '-' }}</td>
          <td><Sparkline :replies="r.replies || []" :total="r.live ? count : 0" /></td>
          <td><StatusCell :r="r" /></td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
