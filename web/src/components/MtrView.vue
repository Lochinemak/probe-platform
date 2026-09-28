<script setup>
import { ms, pct, lossClass, statusLabel, agentPlace } from '../fmt.js'
import ErrorBadge from './ErrorBadge.vue'
defineProps({ task: Object, results: Array })
</script>

<template>
  <div>
    <div class="card mtr-card" v-for="r in results" :key="r.agent_id">
      <div class="mtr-head">
        <span class="name">{{ r.agent_name }}</span>
        <span class="sub">{{ agentPlace(r) }}</span>
        <span class="mono sub" v-if="r.ip">→ {{ r.ip }}</span>
        <span class="badge" v-if="r.protocol">{{ r.protocol.toUpperCase() }}<template v-if="r.port">:{{ r.port }}</template></span>
        <span class="badge" v-if="r.rounds">{{ r.rounds }} 轮</span>
        <span class="badge ok" v-if="r.reached">已到达目标</span>
        <span class="badge warn" v-else-if="r.status === 'done'">未到达目标</span>
        <span class="spacer"></span>
        <span v-if="r.status === 'running'" class="badge running"><span class="pulse"></span>{{ statusLabel(r.status) }}</span>
        <ErrorBadge v-else-if="r.status === 'error'" :message="r.error || '失败'" />
        <span v-else class="badge" :class="r.status">{{ statusLabel(r.status) }}</span>
      </div>
      <div class="table-wrap" v-if="r.hops?.length">
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
