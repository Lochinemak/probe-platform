<script setup>
// Capability breakdown shown inside the node detail dialog. The four
// differentiators (Ping, MTR, IPv6, HTTP/3) appear as scannable status cards
// with plain-language sublabels; universal base probes are summarized below
// (or flagged if an outdated agent lacks any of them).
import { computed } from 'vue'

const props = defineProps({ caps: { type: Array, default: null } })

const NAMES = {
  tcp: 'TCP', http: 'HTTP', udp: 'UDP', tls: 'TLS', dns_multi: 'DNS 对比', status: '节点状态',
  resolver: '指定解析', http3: 'HTTP/3', icmp: 'Ping', icmp_raw: 'raw socket', mtr: 'MTR', ipv6: 'IPv6',
}
const BASE = ['tcp', 'http', 'udp', 'tls', 'dns_multi', 'status', 'resolver']

const view = computed(() => {
  const c = props.caps
  if (!c || !c.length) return null
  const has = (k) => c.includes(k)
  const items = [
    {
      label: 'Ping',
      on: has('icmp'),
      note: has('icmp_raw') ? 'raw socket' : has('icmp') ? '非特权 ICMP' : '无 ICMP 权限',
    },
    {
      label: 'MTR',
      on: has('mtr'),
      note: has('mtr') ? '路由分析可用' : '缺 raw socket',
    },
    {
      label: 'IPv6',
      on: has('ipv6'),
      note: has('ipv6') ? '有 IPv6 出口' : '无 IPv6 出口',
    },
    {
      label: 'HTTP/3',
      on: has('http3'),
      note: has('http3') ? '支持 QUIC' : '不支持',
    },
  ]
  const baseOn = BASE.filter((k) => has(k)).map((k) => NAMES[k])
  const missing = BASE.filter((k) => !has(k)).map((k) => NAMES[k])
  const extra = c.filter((k) => !NAMES[k])
  return { items, baseOn, missing, extra }
})
</script>

<template>
  <div v-if="!view" class="sub">暂未上报探测能力（节点连接后自动检测）</div>
  <div v-else class="cap-panel">
    <div class="cap-grid">
      <div v-for="it in view.items" :key="it.label" class="cap-item" :class="{ off: !it.on }">
        <div class="cap-item-head">
          <span class="cap-dot" :class="{ on: it.on }"></span>
          <span class="cap-name">{{ it.label }}</span>
        </div>
        <div class="cap-note">{{ it.note }}</div>
      </div>
    </div>
    <div v-if="view.missing.length" class="cap-warn">
      agent 版本过旧，暂不支持：{{ view.missing.join('、') }}
    </div>
    <div v-if="view.baseOn.length || view.extra.length" class="cap-base">
      <span class="cap-base-label">基础能力</span>
      <span>{{ [...view.baseOn, ...view.extra].join(' · ') }}</span>
    </div>
  </div>
</template>

<style scoped>
.cap-panel { display: flex; flex-direction: column; gap: 10px; }
.cap-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px;
}
.cap-item {
  background: var(--ok-soft);
  border: 1px solid var(--ok-border);
  border-radius: 10px;
  padding: 9px 10px;
  min-width: 0;
}
.cap-item.off {
  background: var(--bg);
  border-color: var(--border);
  border-style: dashed;
}
.cap-item-head {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 3px;
}
.cap-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--muted);
  opacity: 0.45;
  flex: none;
}
.cap-dot.on {
  background: var(--ok);
  opacity: 1;
}
.cap-name {
  font-size: 13px;
  font-weight: 700;
  color: var(--ok);
}
.cap-item.off .cap-name {
  color: var(--muted);
  font-weight: 600;
}
.cap-note {
  font-size: 11.5px;
  color: var(--text-2);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.cap-item.off .cap-note { color: var(--muted); }
.cap-warn {
  font-size: 12.5px;
  color: var(--warn);
  background: var(--warn-soft);
  border: 1px solid var(--warn-border);
  border-radius: 8px;
  padding: 7px 10px;
}
.cap-base {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 6px 10px;
  font-size: 12px;
  color: var(--muted);
  line-height: 1.5;
}
.cap-base-label {
  font-weight: 600;
  color: var(--text-2);
  flex: none;
}
@media (max-width: 540px) {
  .cap-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
</style>
