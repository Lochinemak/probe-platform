<script setup>
// The numbers above a result: fastest / slowest / average node, how many
// succeeded, how many timed out, and how far the task has got.
import { computed } from 'vue'
import { dur, bucketClass } from '../fmt.js'

const props = defineProps({ task: Object, rows: Array })
const WORDS = {
  ping: ['最快延迟', '最慢延迟', '平均延迟'],
  tcping: ['最短连接', '最长连接', '平均连接'],
  http: ['最短请求', '最长请求', '平均请求'],
  dns: ['最短解析', '最长解析', '平均解析'],
  mtr: ['末跳最快', '末跳最慢', '末跳平均'],
  udp: ['最快响应', '最慢响应', '平均响应'],
  tls: ['最短握手', '最长握手', '平均握手'],
  dns_compare: ['最快节点', '最慢节点', '平均解析'],
  status: ['—', '—', '—'],
}
const words = computed(() => WORDS[props.task.type] || WORDS.ping)
const measured = computed(() => props.rows.filter((r) => r.metric !== undefined && r.metric !== null && !Number.isNaN(r.metric)))
const fastest = computed(() => measured.value.reduce((a, b) => (!a || b.metric < a.metric ? b : a), null))
const slowest = computed(() => measured.value.reduce((a, b) => (!a || b.metric > a.metric ? b : a), null))
const average = computed(() => (measured.value.length ? measured.value.reduce((s, r) => s + r.metric, 0) / measured.value.length : undefined))
const finished = computed(() => props.rows.filter((r) => r.finished))
const succeeded = computed(() => finished.value.filter((r) => !r.failed).length)
const timedOut = computed(() => props.rows.filter((r) => r.timedOut).length)
const failedOther = computed(() => finished.value.length - succeeded.value - timedOut.value)
const rate = computed(() => (finished.value.length ? Math.round((succeeded.value / finished.value.length) * 100) + '%' : '—'))
const who = (r) => (r ? [r.agent_name, r.place].filter(Boolean).join(' · ') : '')
</script>

<template>
  <div class="metrics" :class="{ few: task.type === 'status' }">
    <template v-if="task.type !== 'status'">
    <div class="metric">
      <div class="stat-label">{{ words[0] }}</div>
      <div class="metric-value" :class="bucketClass(task.type, fastest?.metric)">{{ fastest ? dur(fastest.metric) : '—' }}</div>
      <div class="sub ellipsis" :title="who(fastest)">{{ who(fastest) || '暂无数据' }}</div>
    </div>
    <div class="metric">
      <div class="stat-label">{{ words[1] }}</div>
      <div class="metric-value" :class="bucketClass(task.type, slowest?.metric)">{{ slowest ? dur(slowest.metric) : '—' }}</div>
      <div class="sub ellipsis" :title="who(slowest)">{{ who(slowest) || '暂无数据' }}</div>
    </div>
    <div class="metric">
      <div class="stat-label">{{ words[2] }}</div>
      <div class="metric-value" :class="bucketClass(task.type, average)">{{ average !== undefined ? dur(average) : '—' }}</div>
      <div class="sub">{{ measured.length }} 个节点有数据</div>
    </div>
    </template>
    <div class="metric">
      <div class="stat-label">成功率</div>
      <div class="metric-value" :class="{ ok: finished.length && succeeded === finished.length, bad: finished.length && !succeeded }">{{ rate }}</div>
      <div class="sub">成功 {{ succeeded }} / 已返回 {{ finished.length }}</div>
    </div>
    <div class="metric">
      <div class="stat-label">超时</div>
      <div class="metric-value" :class="{ warn: timedOut > 0 }">{{ timedOut }}</div>
      <div class="sub">{{ failedOther > 0 ? `其他失败 ${failedOther}` : '没有回应的节点数' }}</div>
    </div>
    <div class="metric">
      <div class="stat-label">进度</div>
      <div class="metric-value">{{ finished.length }}<span class="sub"> / {{ rows.length }}</span></div>
      <div class="sub">已返回节点</div>
    </div>
  </div>
</template>
