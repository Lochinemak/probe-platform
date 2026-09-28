<script setup>
import { ms, statusLabel, agentPlace } from '../fmt.js'
import ErrorBadge from './ErrorBadge.vue'
defineProps({ task: Object, results: Array })
</script>

<template>
  <div class="table-wrap">
    <table class="grid">
      <thead>
        <tr>
          <th>节点</th>
          <th>位置 / 运营商</th>
          <th>DNS 服务器</th>
          <th>协议</th>
          <th class="num">耗时</th>
          <th>RCode</th>
          <th>记录</th>
          <th>状态</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in results" :key="r.agent_id">
          <td><b>{{ r.agent_name }}</b><span v-if="r.attempts?.length > 1" class="sub"> ×{{ r.attempts.length }}</span></td>
          <td class="sub">{{ agentPlace(r) || '-' }}</td>
          <td class="mono">{{ r.last?.server || '-' }}</td>
          <td class="sub">{{ r.last?.proto || '-' }}<span v-if="r.last?.truncated" title="UDP 应答被截断，已改用 TCP"> (TC)</span></td>
          <td class="num">{{ r.last?.ok ? ms(r.last.rtt_ms) : '-' }}</td>
          <td>
            <span v-if="r.last?.rcode" class="badge" :class="r.last.rcode === 'Success' ? 'ok' : 'bad'">{{ r.last.rcode === 'Success' ? 'NOERROR' : r.last.rcode }}</span>
            <span v-else class="sub">-</span>
          </td>
          <td style="white-space:normal;max-width:520px">
            <template v-if="r.last?.answers?.length">
              <div v-for="(a, i) in r.last.answers" :key="i" class="mono" style="font-size:12.5px">
                <span class="badge" style="margin-right:6px">{{ a.type }}</span>{{ a.value }}
                <span class="sub"> TTL {{ a.ttl }}s</span>
                <span v-if="a.type === 'A' && /^198\.1[89]\./.test(a.value)" class="badge bad" style="margin-left:6px" title="198.18.0.0/15：透明代理 fake-IP，节点 DNS 被劫持">fake-IP</span>
              </div>
            </template>
            <span v-else-if="r.last?.ok" class="sub">无记录</span>
            <span v-else class="sub">-</span>
          </td>
          <td>
            <span v-if="r.status === 'running'" class="badge running"><span class="pulse"></span>{{ statusLabel(r.status) }}</span>
            <ErrorBadge v-else-if="r.status === 'error' || (r.last && !r.last.ok && r.last.error)" :message="r.error || r.last?.error || '失败'" />
            <span v-else-if="r.last && !r.last.ok" class="badge warn">{{ r.last.rcode }}</span>
            <span v-else class="badge" :class="r.status">{{ statusLabel(r.status) }}</span>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
