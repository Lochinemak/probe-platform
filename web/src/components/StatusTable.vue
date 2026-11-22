<script setup>
// 节点状态: what each node's own machine reports. Administrators only.
import { watch } from 'vue'
import { size, uptime } from '../fmt.js'
import { useSort } from '../useSort.js'
import SortTh from './SortTh.vue'
import StatusCell from './StatusCell.vue'

const props = defineProps({ task: Object, results: Array })
const emit = defineEmits(['order'])
const used = (total, free) => (total > 0 ? ((total - free) / total) * 100 : undefined)
const sort = useSort(() => props.results, {
  name: (r) => r.agent_name, place: (r) => r.place, os: (r) => r.sys?.platform || r.sys?.os, uptime: (r) => r.sys?.uptime_sec,
  load: (r) => (r.sys?.load?.length && r.sys.cpus ? r.sys.load[0] / r.sys.cpus : undefined),
  mem: (r) => used(r.sys?.mem_total, r.sys?.mem_available), disk: (r) => used(r.sys?.disk_total, r.sys?.disk_free), version: (r) => r.sys?.agent_version,
})
const rows = sort.sorted
watch(rows, (v) => emit('order', v), { immediate: true })
const level = (p) => (p === undefined ? '' : p >= 90 ? 'bad' : p >= 75 ? 'warn' : 'ok')
// Load per core: above 1 the machine has more to do than it has cores.
const loadLevel = (s) => (!s?.load?.length ? '' : s.load[0] / (s.cpus || 1) >= 1.5 ? 'bad' : s.load[0] / (s.cpus || 1) >= 1 ? 'warn' : 'ok')
</script>

<template>
  <div class="table-wrap">
    <table class="grid">
      <thead>
        <tr>
          <SortTh :sort="sort" k="name" class="col-name">节点</SortTh>
          <SortTh :sort="sort" k="place">地域</SortTh>
          <SortTh :sort="sort" k="os">运行环境</SortTh>
          <SortTh :sort="sort" k="uptime" num>运行时长</SortTh>
          <SortTh :sort="sort" k="load" num title="1 / 5 / 15 分钟平均负载；Windows 没有这个指标">负载</SortTh>
          <SortTh :sort="sort" k="mem" num>内存</SortTh>
          <SortTh :sort="sort" k="disk" num title="agent 所在分区">磁盘</SortTh>
          <SortTh :sort="sort" k="version">agent</SortTh>
          <th>系统 DNS</th>
          <th>状态</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in rows" :key="r.agent_id">
          <td class="col-name"><b>{{ r.agent_name }}</b><div class="sub" v-if="r.sys?.hostname && r.sys.hostname !== r.agent_name">{{ r.sys.hostname }}</div></td>
          <td class="sub">{{ r.place || '-' }}<span v-if="r.line"> · {{ r.line }}</span></td>
          <td>
            <template v-if="r.sys">{{ r.sys.platform || r.sys.os }}<div class="sub">{{ r.sys.os }}/{{ r.sys.arch }} · {{ r.sys.cpus }} 核<span v-if="r.sys.kernel"> · {{ r.sys.kernel }}</span></div></template>
            <span v-else class="sub">-</span>
          </td>
          <td class="num">{{ uptime(r.sys?.uptime_sec) }}</td>
          <td class="num" :class="loadLevel(r.sys)">{{ r.sys?.load?.length ? r.sys.load.map((v) => v.toFixed(2)).join(' / ') : '-' }}</td>
          <td class="num">
            <template v-if="r.sys?.mem_total">
              <span :class="level(used(r.sys.mem_total, r.sys.mem_available))">{{ r.sys.mem_available ? Math.round(used(r.sys.mem_total, r.sys.mem_available)) + '%' : '-' }}</span>
              <div class="sub">{{ r.sys.mem_available ? `可用 ${size(r.sys.mem_available)} / ` : '共 ' }}{{ size(r.sys.mem_total) }}</div>
            </template>
            <template v-else>-</template>
          </td>
          <td class="num">
            <template v-if="r.sys?.disk_total">
              <span :class="level(used(r.sys.disk_total, r.sys.disk_free))">{{ Math.round(used(r.sys.disk_total, r.sys.disk_free)) }}%</span>
              <div class="sub" :title="r.sys.disk_path">可用 {{ size(r.sys.disk_free) }} / {{ size(r.sys.disk_total) }}</div>
            </template>
            <template v-else>-</template>
          </td>
          <td class="sub">{{ r.sys?.agent_version || '-' }}</td>
          <td class="mono sub">{{ r.sys?.nameservers?.join(' ') || '-' }}</td>
          <td><StatusCell :r="r" /></td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
