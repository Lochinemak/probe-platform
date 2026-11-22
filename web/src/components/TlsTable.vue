<script setup>
// TLS 证书: one row per node with the handshake time and the leaf
// certificate; the row opens to the full chain as the server sent it.
import { ref, watch } from 'vue'
import { ms, fmtTime, bucketClass } from '../fmt.js'
import { useSort } from '../useSort.js'
import SortTh from './SortTh.vue'
import StatusCell from './StatusCell.vue'

const props = defineProps({ task: Object, results: Array })
const emit = defineEmits(['order'])
const sort = useSort(() => props.results, {
  name: (r) => r.agent_name, place: (r) => r.place, line: (r) => r.line, ip: (r) => r.ip,
  handshake: (r) => r.metric, days: (r) => (r.tls?.chain?.length ? r.tls.days_left : undefined),
  issuer: (r) => r.leaf?.issuer, version: (r) => r.tls?.version,
})
const rows = sort.sorted
watch(rows, (v) => emit('order', v), { immediate: true })
const open = ref({})
function toggle(id) { open.value[id] = !open.value[id] }
const cn = (dn) => (/(?:^|,)CN=([^,]+)/.exec(dn || '') || [])[1] || dn || '-'
const day = (t) => (t ? fmtTime(t).slice(0, 10) : '-')
function daysClass(d) { return d < 0 ? 'bad' : d < 15 ? 'bad' : d < 30 ? 'warn' : 'ok' }
</script>

<template>
  <div class="table-wrap">
    <table class="grid">
      <thead>
        <tr>
          <th></th>
          <SortTh :sort="sort" k="name" class="col-name">检测点</SortTh>
          <SortTh :sort="sort" k="place">地域</SortTh>
          <SortTh :sort="sort" k="line">线路</SortTh>
          <SortTh :sort="sort" k="ip" class="col-ip">响应 IP</SortTh>
          <SortTh :sort="sort" k="handshake" num title="TLS 握手耗时（ms），不含 TCP 连接">握手耗时</SortTh>
          <SortTh :sort="sort" k="version">协议</SortTh>
          <th>证书</th>
          <SortTh :sort="sort" k="issuer">签发者</SortTh>
          <SortTh :sort="sort" k="days" num>证书到期</SortTh>
          <th>校验</th>
          <th>状态</th>
        </tr>
      </thead>
      <tbody>
        <template v-for="r in rows" :key="r.agent_id">
          <tr class="clickable" @click="toggle(r.agent_id)">
            <td class="sub"><button type="button" class="th-btn" :aria-expanded="!!open[r.agent_id]" :aria-label="`${r.agent_name} 的证书链`" @click.stop="toggle(r.agent_id)">{{ open[r.agent_id] ? '▾' : '▸' }}</button></td>
            <td class="col-name"><b>{{ r.agent_name }}</b><span v-if="r.attempts?.length > 1" class="sub"> ×{{ r.attempts.length }}</span></td>
            <td class="sub">{{ r.place || '-' }}</td>
            <td><span class="badge" v-if="r.line">{{ r.line }}</span><span v-else class="sub">-</span></td>
            <td class="mono col-ip">{{ r.ip || '-' }}<span v-if="r.port && r.port !== 443" class="sub">:{{ r.port }}</span><div class="sub ipgeo" v-if="r.ipGeo">{{ r.ipGeo }}</div></td>
            <td class="num" :class="bucketClass('tls', r.metric)"><b>{{ ms(r.metric) }}</b></td>
            <td class="sub">{{ r.tls?.version || '-' }}<span v-if="r.tls?.alpn"> · {{ r.tls.alpn }}</span></td>
            <td class="ellipsis" style="max-width:220px" :title="r.leaf?.subject">{{ r.leaf ? (r.leaf.subject ? cn(r.leaf.subject) : '已隐藏') : '-' }}</td>
            <td class="sub ellipsis" style="max-width:200px" :title="r.leaf?.issuer">{{ r.leaf?.issuer ? cn(r.leaf.issuer) : '-' }}</td>
            <td class="num" :class="r.leaf ? daysClass(r.tls.days_left) : ''">
              <template v-if="r.leaf">{{ day(r.leaf.not_after) }} <span class="sub">{{ r.tls.days_left < 0 ? `已过期 ${-r.tls.days_left} 天` : `剩 ${r.tls.days_left} 天` }}</span></template>
              <template v-else>-</template>
            </td>
            <td>
              <span v-if="r.leaf" class="badge" :class="r.tls.verified ? 'ok' : 'bad'" :title="r.tls.verify_error || '证书链在该节点的信任库里校验通过，且与主机名匹配'">{{ r.tls.verified ? '通过' : '未通过' }}</span>
              <span v-else class="sub">-</span>
            </td>
            <td @click.stop><StatusCell :r="r" /></td>
          </tr>
          <tr v-if="open[r.agent_id]">
            <td colspan="12" style="white-space:normal">
              <div class="detail" v-if="r.tls?.chain?.length">
                <div class="kv" style="margin-bottom:8px">
                  <div>协议 / 套件</div><div>{{ r.tls.version }} · {{ r.tls.cipher }}<span v-if="r.tls.alpn"> · ALPN {{ r.tls.alpn }}</span><span v-if="r.tls.ocsp_stapled"> · OCSP 装订</span></div>
                  <div>握手</div><div>{{ r.attempts.map((a) => (a.ok ? `${ms(a.handshake_ms)} ms（连接 ${ms(a.connect_ms)} ms）` : a.error || '失败')).join('，') }}</div>
                  <template v-if="!r.tls.verified"><div>校验</div><div class="bad">{{ r.tls.verify_error }}</div></template>
                </div>
                <div class="cert" v-for="(c, i) in r.tls.chain" :key="i">
                  <div><span class="badge">{{ i === 0 ? '站点证书' : c.is_ca ? '中间 / 根证书' : '证书' }}</span> <b>{{ c.subject || '（主体已隐藏）' }}</b></div>
                  <div class="kv">
                    <div>签发者</div><div>{{ c.issuer || '（已隐藏）' }}</div>
                    <div>有效期</div><div>{{ fmtTime(c.not_before) }} 至 {{ fmtTime(c.not_after) }}</div>
                    <div>密钥 / 签名</div><div>{{ c.key_type || '-' }} · {{ c.sig_alg || '-' }}</div>
                    <template v-if="c.sans?.length"><div>适用名称</div><div class="mono">{{ c.sans.join('  ') }}</div></template>
                    <template v-if="c.serial"><div>序列号</div><div class="mono">{{ c.serial }}</div></template>
                  </div>
                </div>
              </div>
              <div v-else class="sub">{{ r.last?.error || (r.live ? '等待握手结果…' : '没有拿到证书') }}</div>
            </td>
          </tr>
        </template>
      </tbody>
    </table>
  </div>
</template>
