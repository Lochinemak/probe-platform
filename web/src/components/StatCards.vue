<script setup>
// The summary above the probe form: how many nodes are online and where, and
// how busy the last 24 hours were. Guests see the same numbers.
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { api } from '../api.js'

const stats = ref(null)
let timer = null
async function load() {
  try { stats.value = await api.stats() } catch { /* the form works without the summary */ }
}
function rate(v) { return v === null || v === undefined ? '—' : Math.round(v * 100) + '%' }
onMounted(() => { load(); timer = setInterval(load, 30000) })
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div class="stats" v-if="stats">
    <div class="stat main">
      <div class="stat-label">在线节点</div>
      <div class="stat-value">{{ stats.nodes.online }}<span class="sub"> / {{ stats.nodes.total }}</span></div>
      <div class="stat-split">
        <span>中国大陆 <b>{{ stats.nodes.mainland }}</b></span>
        <span>其他地区 <b>{{ stats.nodes.overseas }}</b></span>
        <span v-if="stats.nodes.unknown" title="公网 IP 查不到归属地、也没有填位置的节点；可以在「节点」页修正">待识别 <b class="warn">{{ stats.nodes.unknown }}</b></span>
      </div>
    </div>
    <div class="stat">
      <div class="stat-label">覆盖地区</div>
      <div class="stat-value">{{ stats.nodes.regions }}</div>
      <div class="sub">有在线节点的省份 / 国家</div>
    </div>
    <div class="stat">
      <div class="stat-label">24h 检测</div>
      <div class="stat-value">{{ stats.tasks_24h }}</div>
      <div class="sub" v-if="stats.monitor_runs_24h">另有定时监控 {{ stats.monitor_runs_24h }} 次</div>
      <div class="sub" v-else>手动发起的检测次数</div>
    </div>
    <div class="stat">
      <div class="stat-label">24h 节点执行成功率</div>
      <div class="stat-value">{{ rate(stats.success_rate_24h) }}</div>
      <div class="sub" title="节点跑完任务并交回结果的比例；目标不通（例如 Ping 全丢）仍算执行成功">节点跑完并交回结果的比例</div>
    </div>
  </div>
</template>
