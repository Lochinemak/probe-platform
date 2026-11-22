<script setup>
import { watch } from 'vue'
import { ms, bucketClass } from '../fmt.js'
import { useSort } from '../useSort.js'
import SortTh from './SortTh.vue'
import StatusCell from './StatusCell.vue'

const props = defineProps({ task: Object, results: Array })
const emit = defineEmits(['order'])
const sort = useSort(() => props.results, {
  name: (r) => r.agent_name, place: (r) => r.place, line: (r) => r.line, server: (r) => r.last?.server,
  rtt: (r) => (r.last?.ok ? r.last.rtt_ms : undefined), answers: (r) => (r.last?.answers || []).map((a) => a.value).sort().join(' '),
})
const rows = sort.sorted
watch(rows, (v) => emit('order', v), { immediate: true })
</script>

<template>
  <div class="table-wrap">
    <table class="grid">
      <thead>
        <tr>
          <SortTh :sort="sort" k="name" class="col-name">检测点</SortTh>
          <SortTh :sort="sort" k="place">地域</SortTh>
          <SortTh :sort="sort" k="line">线路</SortTh>
          <SortTh :sort="sort" k="server" class="col-ip">解析器</SortTh>
          <th>协议</th>
          <SortTh :sort="sort" k="rtt" num>解析耗时</SortTh>
          <th>RCode</th>
          <SortTh :sort="sort" k="answers" title="按解析结果排序，相同结果的节点排在一起">解析结果 / 归属</SortTh>
          <th>状态</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in rows" :key="r.agent_id">
          <td class="col-name"><b>{{ r.agent_name }}</b><span v-if="r.attempts?.length > 1" class="sub"> ×{{ r.attempts.length }}</span></td>
          <td class="sub">{{ r.place || '-' }}</td>
          <td><span class="badge" v-if="r.line">{{ r.line }}</span><span v-else class="sub">-</span></td>
          <td class="mono col-ip">{{ r.last?.server || '-' }}</td>
          <td class="sub">{{ r.last?.proto || '-' }}<span v-if="r.last?.truncated" title="UDP 应答被截断，已改用 TCP"> (TC)</span></td>
          <td class="num" :class="bucketClass('dns', r.last?.ok ? r.last.rtt_ms : undefined)">{{ r.last?.ok ? ms(r.last.rtt_ms) : '-' }}</td>
          <td>
            <span v-if="r.last?.rcode" class="badge" :class="r.last.rcode === 'Success' ? 'ok' : 'bad'">{{ r.last.rcode === 'Success' ? 'NOERROR' : r.last.rcode }}</span>
            <span v-else class="sub">-</span>
          </td>
          <td style="white-space:normal;max-width:520px">
            <template v-if="r.last?.answers?.length">
              <div v-for="(a, i) in r.last.answers" :key="i" class="mono" style="font-size:12.5px">
                <span class="badge" style="margin-right:6px">{{ a.type }}</span><span class="ip-text">{{ a.value }}</span>
                <span class="sub"> TTL {{ a.ttl }}s</span><span class="sub" v-if="a.geo"> · {{ a.geo }}</span>
                <span v-if="a.type === 'A' && /^198\.1[89]\./.test(a.value)" class="badge bad" style="margin-left:6px" title="198.18.0.0/15：透明代理 fake-IP，节点 DNS 被劫持">fake-IP</span>
              </div>
            </template>
            <span v-else-if="r.last?.ok" class="sub">无记录</span>
            <span v-else class="sub">-</span>
          </td>
          <td><StatusCell :r="r" /></td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
