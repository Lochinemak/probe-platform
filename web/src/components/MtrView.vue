<script setup>
import { reactive } from 'vue'
import { ms, pct, lossClass, bucketClass } from '../fmt.js'
import StatusCell from './StatusCell.vue'
const props = defineProps({ task: Object, results: Array })
// Each node's hop table can be folded away; with many nodes they start folded
// so the summaries can be compared at a glance.
const folded = reactive({})
const isOpen = (r) => (folded[r.agent_id] === undefined ? props.results.length <= 3 : !folded[r.agent_id])
function toggle(r) { folded[r.agent_id] = isOpen(r) }
</script>

<template>
  <div>
    <div class="card mtr-card" v-for="r in results" :key="r.agent_id">
      <div class="mtr-head">
        <button type="button" class="th-btn" :aria-expanded="isOpen(r)" :aria-label="`${r.agent_name} 的逐跳明细`" @click="toggle(r)">{{ isOpen(r) ? '▾' : '▸' }}</button>
        <span class="name">{{ r.agent_name }}</span>
        <span class="sub">{{ r.place }}</span>
        <span class="badge" v-if="r.line">{{ r.line }}</span>
        <span class="mono sub" v-if="r.ip">→ {{ r.ip }}</span>
        <span class="sub" v-if="r.ipGeo">{{ r.ipGeo }}</span>
        <span class="badge" v-if="r.protocol">{{ r.protocol.toUpperCase() }}<template v-if="r.port">:{{ r.port }}</template></span>
        <span class="badge" v-if="r.rounds">{{ r.rounds }} 轮</span>
        <span class="badge ok" v-if="r.reached">已到达目标</span>
        <span class="badge warn" v-else-if="r.status === 'done'">未到达目标</span>
        <span class="spacer"></span>
        <StatusCell v-if="r.live || r.status === 'error' || r.stopped" :r="r" />
      </div>
      <div class="mtr-summary" v-if="r.hops?.length">
        <span>跳数 <b>{{ r.hopCount }}</b></span>
        <span>最后一跳 <b class="mono">{{ r.lastHop?.hosts?.[0] || '* * *' }}</b></span>
        <span>已采样 <b>{{ r.sampled }}</b></span>
        <span>末跳丢包 <b :class="lossClass(r.lastHop?.loss_pct)">{{ r.lastHop ? pct(r.lastHop.loss_pct) : '-' }}</b></span>
        <span>平均延迟 <b :class="bucketClass('mtr', r.metric)">{{ r.metric !== undefined ? ms(r.metric) + ' ms' : '-' }}</b></span>
      </div>
      <div class="table-wrap" v-if="r.hops?.length && isOpen(r)">
        <table class="grid">
          <thead>
            <tr>
              <th class="num">#</th>
              <th>主机</th>
              <th class="num">丢包</th>
              <th class="num">发送</th>
              <th class="num">最后</th>
              <th class="num">平均</th>
              <th class="num">最好</th>
              <th class="num">最差</th>
              <th class="num">抖动</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="h in r.hops" :key="h.ttl" :class="{ reached: h.reached }">
              <td class="num sub">{{ h.ttl }}</td>
              <td class="hop-host" style="white-space:normal">
                <template v-if="h.hosts?.length">
                  <div v-for="(host, i) in h.hosts" :key="host">
                    {{ host }}
                    <span class="name" v-if="h.names?.[i]">({{ h.names[i] }})</span>
                    <span class="geo" v-if="h.geo?.[i]">{{ h.geo[i] }}</span>
                  </div>
                </template>
                <span v-else class="sub">* * *</span>
              </td>
              <td class="num" :class="lossClass(h.loss_pct)">
                <span class="bar"><i :style="{ width: h.loss_pct + '%' }"></i></span>{{ pct(h.loss_pct) }}
              </td>
              <td class="num">{{ h.sent }}</td>
              <td class="num">{{ h.received ? ms(h.last_ms) : '-' }}</td>
              <td class="num"><b>{{ h.received ? ms(h.avg_ms) : '-' }}</b></td>
              <td class="num">{{ h.received ? ms(h.best_ms) : '-' }}</td>
              <td class="num">{{ h.received ? ms(h.worst_ms) : '-' }}</td>
              <td class="num">{{ h.received ? ms(h.stddev_ms) : '-' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else-if="r.status === 'running'" class="sub">等待第一轮结果…</div>
      <div v-else-if="r.status !== 'error'" class="sub">无数据</div>
    </div>
  </div>
</template>
