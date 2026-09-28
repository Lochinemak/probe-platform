<script setup>
import { ref } from 'vue'
import { ms, bytes, mbps, statusLabel, agentPlace, fmtTime } from '../fmt.js'
import ErrorBadge from './ErrorBadge.vue'

defineProps({ task: Object, results: Array })
const open = ref({})
function toggle(id) { open.value[id] = !open.value[id] }
function codeClass(c) { return c ? 'code-' + Math.floor(c / 100) : '' }
function phaseWidths(t) {
  if (!t || !t.total_ms) return []
  const parts = [['dns', t.dns_ms], ['conn', t.connect_ms], ['tls', t.tls_ms], ['ttfb', t.ttfb_ms], ['xfer', t.transfer_ms]]
  const sum = parts.reduce((s, p) => s + (p[1] || 0), 0) || t.total_ms
  return parts.map(([k, v]) => ({ k, w: ((v || 0) / sum) * 100, v }))
}
</script>

<template>
  <div class="table-wrap">
    <table class="grid">
      <thead>
        <tr>
          <th></th>
          <th>节点</th>
          <th>位置 / 运营商</th>
          <th>IP</th>
          <th>状态码</th>
          <th>协议</th>
          <th class="num">DNS</th>
          <th class="num">连接</th>
          <th class="num">TLS</th>
          <th class="num">首字节</th>
          <th class="num">下载</th>
          <th class="num">总耗时</th>
          <th class="num">大小</th>
          <th class="num" v-if="task.params?.speed_test">速率</th>
          <th>耗时分布</th>
          <th>状态</th>
        </tr>
      </thead>
      <tbody>
        <template v-for="r in results" :key="r.agent_id">
          <tr class="clickable" @click="toggle(r.agent_id)">
            <td class="sub">{{ open[r.agent_id] ? '▾' : '▸' }}</td>
            <td><b>{{ r.agent_name }}</b><span v-if="r.attempts?.length > 1" class="sub"> ×{{ r.attempts.length }}</span></td>
            <td class="sub">{{ agentPlace(r) || '-' }}</td>
            <td class="mono">{{ r.last?.ip || '-' }}</td>
            <td>
              <span v-if="r.last?.status_code" class="badge" :class="codeClass(r.last.status_code)">{{ r.last.status_code }}</span>
              <span v-else class="sub">-</span>
            </td>
            <td class="sub">{{ r.last?.proto || '-' }}<span v-if="r.last?.tls_version"> · {{ r.last.tls_version }}</span></td>
            <td class="num">{{ r.avg?.total_ms !== undefined ? ms(r.avg.dns_ms) : '-' }}</td>
            <td class="num">{{ r.avg?.total_ms !== undefined ? ms(r.avg.connect_ms) : '-' }}</td>
            <td class="num">{{ r.avg?.total_ms !== undefined ? ms(r.avg.tls_ms) : '-' }}</td>
            <td class="num">{{ r.avg?.total_ms !== undefined ? ms(r.avg.ttfb_ms) : '-' }}</td>
            <td class="num">{{ r.avg?.total_ms !== undefined ? ms(r.avg.transfer_ms) : '-' }}</td>
            <td class="num"><b>{{ r.avg?.total_ms !== undefined ? ms(r.avg.total_ms) : '-' }}</b></td>
            <td class="num">{{ r.last?.ok ? bytes(r.last.body_bytes) : '-' }}</td>
            <td class="num" v-if="task.params?.speed_test"><b>{{ r.throughput ? mbps(r.throughput) : '-' }}</b></td>
            <td>
              <div class="phases" v-if="r.avg?.total_ms !== undefined" style="width:160px">
                <i v-for="p in phaseWidths(r.avg)" :key="p.k" :class="p.k" :style="{ width: p.w + '%' }" :title="p.k + ' ' + ms(p.v) + 'ms'"></i>
              </div>
            </td>
            <td>
              <span v-if="r.status === 'running'" class="badge running"><span class="pulse"></span>{{ statusLabel(r.status) }}</span>
              <ErrorBadge v-else-if="r.status === 'error' || (r.last && !r.last.ok)" :message="r.error || r.last?.error || '失败'" />
              <ErrorBadge v-else-if="r.assertFail" :message="'断言未通过：\n' + r.failedAssertions.join('\n')" />
              <span v-else class="badge" :class="r.status">{{ statusLabel(r.status) }}</span>
            </td>
          </tr>
          <tr v-if="open[r.agent_id]">
            <td colspan="16" style="white-space:normal">
              <div class="detail" v-for="a in r.attempts" :key="a.seq">
                <div class="row" style="margin-bottom:6px">
                  <span class="badge">#{{ a.seq + 1 }}</span>
                  <span v-if="a.status_code" class="badge" :class="codeClass(a.status_code)">{{ a.status }}</span>
                  <span v-if="!a.ok" class="bad">{{ a.error }}</span>
                  <span v-for="(as, i) in a.assertions || []" :key="i" class="badge" :class="as.pass ? 'ok' : 'bad'" :title="as.detail">{{ as.pass ? '✓' : '✗' }} {{ as.name }}</span>
                  <span v-if="a.throughput_mbps" class="badge ok">{{ mbps(a.throughput_mbps) }} · {{ bytes(a.speed_bytes) }} / {{ ms(a.speed_ms) }} ms</span>
                  <span class="mono sub">{{ a.final_url && a.final_url !== a.url ? a.url + ' → ' + a.final_url : a.url }}</span>
                </div>
                <div class="phases" v-if="a.ok">
                  <i v-for="p in phaseWidths(a.timing)" :key="p.k" :class="p.k" :style="{ width: p.w + '%' }"></i>
                </div>
                <div class="legend" v-if="a.ok">
                  <span><i class="dns" style="background:#a78bfa"></i>DNS {{ ms(a.timing.dns_ms) }}</span>
                  <span><i style="background:#38bdf8"></i>TCP {{ ms(a.timing.connect_ms) }}</span>
                  <span><i style="background:#f472b6"></i>TLS {{ ms(a.timing.tls_ms) }}</span>
                  <span><i style="background:#fbbf24"></i>首字节 {{ ms(a.timing.ttfb_ms) }}</span>
                  <span><i style="background:#34d399"></i>下载 {{ ms(a.timing.transfer_ms) }}</span>
                  <span><b>总计 {{ ms(a.timing.total_ms) }} ms</b></span>
                </div>
                <div class="kv" style="margin-top:8px" v-if="a.ok">
                  <div>IP</div><div class="mono">{{ a.ip }}</div>
                  <div>协议</div><div>{{ a.proto }}</div>
                  <template v-if="a.tls_version">
                    <div>TLS</div><div>{{ a.tls_version }} · {{ a.tls_cipher }}</div>
                    <div>证书</div><div>{{ a.cert_subject }} <span class="sub">由 {{ a.cert_issuer }} 签发，{{ fmtTime(a.cert_not_after) }} 到期</span></div>
                  </template>
                  <template v-if="a.redirects?.length">
                    <div>跳转链</div><div class="mono">{{ a.url }}<template v-for="(h, i) in a.redirects" :key="i"> <span class="sub">→ {{ h.split(' ')[0] }} →</span> {{ h.split(' ').slice(1).join(' ') }}</template></div>
                  </template>
                  <template v-for="(as, i) in a.assertions || []" :key="'as' + i">
                    <div>断言 · {{ as.name }}</div><div :class="as.pass ? 'ok' : 'bad'">{{ as.pass ? '通过' : '未通过' }} <span class="sub">{{ as.detail }}</span></div>
                  </template>
                  <div>大小</div><div>{{ bytes(a.body_bytes) }} <span class="sub" v-if="a.content_length >= 0">(Content-Length {{ a.content_length }})</span></div>
                  <template v-for="k in ['Server', 'Content-Type', 'Cache-Control', 'X-Cache', 'Via', 'Cf-Ray', 'X-Powered-By', 'Location']" :key="k">
                    <template v-if="a.headers?.[k]"><div>{{ k }}</div><div class="mono">{{ a.headers[k] }}</div></template>
                  </template>
                </div>
              </div>
              <div v-if="!r.attempts?.length" class="sub">暂无数据</div>
            </td>
          </tr>
        </template>
      </tbody>
    </table>
  </div>
</template>
