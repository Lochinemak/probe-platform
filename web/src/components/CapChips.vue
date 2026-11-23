<script setup>
// The capability column of the node list. Every current agent can do TCP,
// HTTP, UDP, TLS, DNS compare, node status and custom resolving, so listing
// them on every row is only noise. What actually differs between nodes is
// shown as four fixed chips (lit or dimmed, same order on every row so the
// column reads like a matrix); an agent too old for some probes gets one
// warning chip that names them on hover.
import { computed } from 'vue'

const props = defineProps({ caps: { type: Array, default: null } })

const NAMES = {
  tcp: 'TCP', http: 'HTTP', udp: 'UDP', tls: 'TLS', dns_multi: 'DNS 对比', status: '节点状态',
  resolver: '指定连接 IP / 解析', http3: 'HTTP/3', icmp: 'Ping', icmp_raw: 'raw socket', mtr: 'MTR', ipv6: 'IPv6',
}
const BASE = ['tcp', 'http', 'udp', 'tls', 'dns_multi', 'status', 'resolver']

const view = computed(() => {
  const c = props.caps
  if (!c || !c.length) return null
  const has = (k) => c.includes(k)
  const chips = [
    {
      label: 'Ping', on: has('icmp'),
      title: has('icmp_raw') ? 'Ping：raw socket' : has('icmp') ? 'Ping：非特权 ICMP（无 raw socket 权限）' : '没有 ICMP 权限，不能 Ping',
    },
    { label: 'MTR', on: has('mtr'), title: has('mtr') ? '可做路由分析（MTR）' : '没有 raw socket 权限，MTR 不可用' },
    { label: 'IPv6', on: has('ipv6'), title: has('ipv6') ? '有 IPv6 出口' : '没有 IPv6 出口' },
    { label: 'H3', on: has('http3'), title: has('http3') ? '支持 HTTP/3' : '不支持 HTTP/3（agent 版本过旧，或是不带 QUIC 的精简构建）' },
  ]
  const missing = BASE.filter((k) => !has(k)).map((k) => NAMES[k])
  const all = c.map((k) => NAMES[k] || k).join('、')
  return { chips, missing, all }
})
</script>

<template>
  <span v-if="!view" class="sub">-</span>
  <div v-else class="caps" :title="'支持：' + view.all">
    <span v-for="ch in view.chips" :key="ch.label" class="cap" :class="{ off: !ch.on }" :title="ch.title">{{ ch.label }}</span>
    <span v-if="view.missing.length" class="cap warn" :title="'agent 版本过旧，不支持：' + view.missing.join('、')">缺 {{ view.missing.length }} 项</span>
  </div>
</template>

<style scoped>
/* Two by two (Ping MTR / IPv6 H3): narrow enough for the wide admin table on
   a laptop and for the scrolling table on a phone, and every row lines up. */
.caps { display: inline-grid; grid-template-columns: repeat(2, 1fr); gap: 4px; vertical-align: middle; }
.cap {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 1px 7px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 600;
  line-height: 1.5;
  color: var(--ok);
  background: var(--ok-soft);
  border: 1px solid var(--ok-border);
  cursor: default;
}
.cap.off {
  color: var(--muted);
  background: transparent;
  border-style: dashed;
  border-color: var(--border);
  text-decoration: line-through;
  opacity: .6;
}
.cap.warn { grid-column: 1 / -1; color: var(--warn); background: var(--warn-soft); border-color: var(--warn-border); }
</style>
