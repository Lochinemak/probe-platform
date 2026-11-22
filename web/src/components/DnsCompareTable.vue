<script setup>
// DNS 对比: for each node, what every resolver it asked said. Resolvers whose
// answers are the same share a group number; group 1 is the majority.
import { watch } from 'vue'
import { ms } from '../fmt.js'
import { useSort } from '../useSort.js'
import SortTh from './SortTh.vue'
import StatusCell from './StatusCell.vue'

const props = defineProps({ task: Object, results: Array })
const emit = defineEmits(['order'])
const VERDICT = { consistent: ['一致', 'ok'], inconsistent: ['不一致', 'warn'], failed: ['全部失败', 'bad'] }
const sort = useSort(() => props.results, {
  name: (r) => r.agent_name, place: (r) => r.place, line: (r) => r.line,
  verdict: (r) => ({ consistent: 0, inconsistent: 1, failed: 2 })[r.verdict], slowest: (r) => r.slowest,
})
const rows = sort.sorted
watch(rows, (v) => emit('order', v), { immediate: true })
const values = (e) => (e.attempt?.answers || []).filter((a) => a.value)
</script>

<template>
  <div class="table-wrap">
    <table class="grid">
      <thead>
        <tr>
          <SortTh :sort="sort" k="name" class="col-name">检测点</SortTh>
          <SortTh :sort="sort" k="place">地域</SortTh>
          <SortTh :sort="sort" k="line">线路</SortTh>
          <SortTh :sort="sort" k="verdict" title="各解析器返回的记录是否相同。CDN 会有意给不同解析器不同的地址，不一致不一定是故障">一致性</SortTh>
          <th>解析器</th>
          <th>解析结果 / 归属</th>
          <SortTh :sort="sort" k="slowest" num title="成功的解析器里最慢的一次（ms）">解析耗时</SortTh>
          <th>状态</th>
        </tr>
      </thead>
      <tbody>
        <template v-for="r in rows" :key="r.agent_id">
          <tr v-for="(e, i) in (r.entries.length ? r.entries : [null])" :key="r.agent_id + i" :class="{ 'group-first': i === 0 }">
            <template v-if="i === 0">
              <td class="col-name" :rowspan="r.entries.length || 1"><b>{{ r.agent_name }}</b></td>
              <td class="sub" :rowspan="r.entries.length || 1">{{ r.place || '-' }}</td>
              <td :rowspan="r.entries.length || 1"><span class="badge" v-if="r.line">{{ r.line }}</span><span v-else class="sub">-</span></td>
              <td :rowspan="r.entries.length || 1">
                <span v-if="VERDICT[r.verdict]" class="badge" :class="VERDICT[r.verdict][1]">{{ VERDICT[r.verdict][0] }}</span>
                <span v-else class="sub">-</span>
                <div class="sub" v-if="r.groups > 1">{{ r.groups }} 种结果</div>
              </td>
            </template>
            <template v-if="e">
              <td><b>{{ e.label || e.server }}</b><div class="sub mono col-ip" v-if="e.attempt?.server && e.label">{{ e.attempt.server }}</div></td>
              <td style="white-space:normal;max-width:460px">
                <span v-if="e.attempt?.ok && r.groups > 1" class="badge" :class="r.groupOf[e.key] === 1 ? 'ok' : 'warn'" style="margin-right:6px" :title="r.groupOf[e.key] === 1 ? '与多数解析器相同' : '与多数解析器不同'">第 {{ r.groupOf[e.key] }} 组</span>
                <template v-if="values(e).length">
                  <span v-for="(a, j) in values(e)" :key="j" class="mono" style="margin-right:10px"><span class="ip-text">{{ a.value }}</span><span class="sub ipgeo" v-if="a.geo" style="margin-left:6px">{{ a.geo }}</span></span>
                </template>
                <span v-else-if="e.attempt?.ok" class="sub">{{ (e.attempt.answers || []).length ? '记录值已隐藏' : '无记录' }}</span>
                <span v-else class="bad">{{ e.attempt?.error || e.attempt?.rcode || '失败' }}</span>
              </td>
              <td class="num">{{ e.attempt?.ok ? ms(e.attempt.rtt_ms) : '-' }}</td>
            </template>
            <template v-else><td colspan="3" class="sub">{{ r.live ? '等待解析结果…' : '-' }}</td></template>
            <td v-if="i === 0" :rowspan="r.entries.length || 1"><StatusCell :r="r" /></td>
          </tr>
        </template>
      </tbody>
    </table>
  </div>
</template>
