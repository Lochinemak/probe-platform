<script setup>
import { ref, watch } from 'vue'
import { ms, bytes, mbps, fmtTime, bucketClass } from '../fmt.js'
import { useSort } from '../useSort.js'
import SortTh from './SortTh.vue'
import StatusCell from './StatusCell.vue'

const props = defineProps({ task: Object, results: Array })
const emit = defineEmits(['order'])
const phase = (k) => (r) => (r.avg?.total_ms !== undefined ? r.avg[k] : undefined)
const sort = useSort(() => props.results, {
  name: (r) => r.agent_name, place: (r) => r.place, line: (r) => r.line, ip: (r) => r.ip, code: (r) => r.last?.status_code || undefined,
  dns: phase('dns_ms'), connect: phase('connect_ms'), tls: phase('tls_ms'), ttfb: phase('ttfb_ms'), transfer: phase('transfer_ms'), total: phase('total_ms'),
  size: (r) => (r.last?.ok ? r.last.body_bytes : undefined), speed: (r) => r.throughput,
})
const rows = sort.sorted
watch(rows, (v) => emit('order', v), { immediate: true })
const open = ref({})
// 198.18.0.0/15: the node's DNS answers come from a transparent proxy's
// fake-IP pool, so the request was relayed by the proxy.
const FAKE_IP_HINT = '198.18.x.x 为透明代理（OpenClash 等）fake-IP：请求经代理转发，状态码与断言仍有效，但各阶段耗时反映的是代理链路而非本机网络'
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
          <SortTh :sort="sort" k="name">检测点</SortTh>
          <SortTh :sort="sort" k="place">地域</SortTh>
          <SortTh :sort="sort" k="line">线路</SortTh>
          <SortTh :sort="sort" k="ip">响应 IP</SortTh>
          <SortTh :sort="sort" k="code">HTTP 状态</SortTh>
          <th>协议</th>
          <SortTh :sort="sort" k="dns" num>DNS</SortTh>
          <SortTh :sort="sort" k="connect" num>连接</SortTh>
          <SortTh :sort="sort" k="tls" num>TLS</SortTh>
          <SortTh :sort="sort" k="ttfb" num>首字节</SortTh>
          <SortTh :sort="sort" k="transfer" num>下载</SortTh>
          <SortTh :sort="sort" k="total" num title="整个请求的耗时（ms）：解析、连接、TLS、等待首字节、下载">完整耗时</SortTh>
          <SortTh :sort="sort" k="size" num>大小</SortTh>
          <SortTh v-if="task.params?.speed_test" :sort="sort" k="speed" num>速率</SortTh>
          <th>耗时分布</th>
          <th>状态</th>
        </tr>
      </thead>
      <tbody>
        <template v-for="r in rows" :key="r.agent_id">
          <tr class="clickable" @click="toggle(r.agent_id)">
            <td class="sub"><button type="button" class="th-btn" :aria-expanded="!!open[r.agent_id]" :aria-label="`${r.agent_name} 的请求详情与响应头`" @click.stop="toggle(r.agent_id)">{{ open[r.agent_id] ? '▾' : '▸' }}</button></td>
            <td><b>{{ r.agent_name }}</b><span v-if="r.attempts?.length > 1" class="sub"> ×{{ r.attempts.length }}</span></td>
            <td class="sub">{{ r.place || '-' }}</td>
            <td><span class="badge" v-if="r.line">{{ r.line }}</span><span v-else class="sub">-</span></td>
            <td class="mono">{{ r.ip || '-' }}<span v-if="r.fakeIP" class="badge warn" style="margin-left:6px" :title="FAKE_IP_HINT">fake-IP</span><div class="sub ipgeo" v-if="r.ipGeo && !r.fakeIP">{{ r.ipGeo }}</div></td>
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
            <td class="num" :class="bucketClass('http', r.avg?.total_ms)"><b>{{ r.avg?.total_ms !== undefined ? ms(r.avg.total_ms) : '-' }}</b></td>
            <td class="num">{{ r.last?.ok ? bytes(r.last.body_bytes) : '-' }}</td>
            <td class="num" v-if="task.params?.speed_test"><b>{{ r.throughput ? mbps(r.throughput) : '-' }}</b></td>
            <td>
              <div class="phases" v-if="r.avg?.total_ms !== undefined" style="width:160px">
                <i v-for="p in phaseWidths(r.avg)" :key="p.k" :class="p.k" :style="{ width: p.w + '%' }" :title="p.k + ' ' + ms(p.v) + 'ms'"></i>
              </div>
            </td>
            <td @click.stop><StatusCell :r="r" /></td>
          </tr>
          <tr v-if="open[r.agent_id]">
            <td colspan="17" style="white-space:normal">
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
                  <div>IP</div><div class="mono">{{ a.ip }}<span v-if="a.fake_ip" class="sub" style="margin-left:8px">{{ FAKE_IP_HINT }}</span></div>
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
