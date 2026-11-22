<script setup>
// 公共 DNS 目录: public resolvers and carriers' provincial ones, with their
// addresses ready to copy or to try from the nodes.
import { ref, computed, onMounted } from 'vue'
import { api } from '../api.js'
import { LINES, PROVINCES } from '../geo.js'
import { navigate } from '../route.js'

const all = ref([])
const error = ref('')
const loading = ref(true)
const scope = ref('') // '' | global | national | province
const province = ref('')
const line = ref('')
const family = ref('4')
const query = ref('')
const copied = ref('')

onMounted(async () => {
  try { all.value = (await api.resolvers()).resolvers } catch (e) { error.value = e.message } finally { loading.value = false }
})

const SCOPES = [['', '全部'], ['national', '全国公共'], ['global', '全球公共'], ['province', '运营商省级']]
const scopeLabel = { global: '全球', national: '全国', province: '省级' }
const count = (s) => (s ? all.value.filter((r) => r.scope === s).length : all.value.length)
const provinces = computed(() => PROVINCES.filter((p) => all.value.some((r) => r.scope === 'province' && r.province === p)))
const lines = computed(() => LINES.filter((l) => all.value.some((r) => r.line === l)))
const shown = computed(() => {
  const q = query.value.trim().toLowerCase()
  return all.value.filter((r) => (!scope.value || r.scope === scope.value)
    && (scope.value !== 'province' || ((!province.value || r.province === province.value) && (!line.value || r.line === line.value)))
    && (family.value === '4' || r.ipv6?.length)
    && (!q || [r.name, r.org, r.province, r.line, ...r.ipv4, ...(r.ipv6 || [])].join(' ').toLowerCase().includes(q)))
})
const addrs = (r) => (family.value === '6' ? r.ipv6 || [] : r.ipv4)
async function copy(text) {
  try { await navigator.clipboard.writeText(text); copied.value = text; setTimeout(() => { if (copied.value === text) copied.value = '' }, 1500) } catch { error.value = '复制失败：浏览器不允许访问剪贴板' }
}
// Try a resolver from the nodes: the DNS probe with this server filled in.
function tryIt(addr) { navigate('/dns?dns_server=' + encodeURIComponent(addr.includes(':') && !addr.startsWith('http') ? `[${addr}]` : addr)) }
</script>

<template>
  <div>
    <div class="row" style="margin-bottom:12px">
      <div>
        <h2 style="margin:0">公共 DNS 目录</h2>
        <div class="sub">公共解析器和运营商省级解析器的地址。收录的地址都从本平台的节点实际查询过，查不通的没有收；运营商的省级 DNS 通常只对本网用户开放，所以能核实的不多。</div>
      </div>
    </div>
    <div class="error-box" v-if="error">{{ error }}</div>

    <div class="card">
      <div class="row">
        <div class="tabs sm" role="group" aria-label="级别">
          <button v-for="[v, label] in SCOPES" :key="v" type="button" :class="{ active: scope === v }" @click="scope = v">{{ label }} {{ count(v) }}</button>
        </div>
        <div class="tabs sm" role="group" aria-label="地址族">
          <button type="button" :class="{ active: family === '4' }" @click="family = '4'">IPv4</button>
          <button type="button" :class="{ active: family === '6' }" @click="family = '6'">IPv6</button>
        </div>
        <template v-if="scope === 'province'">
          <select v-model="province" aria-label="省份"><option value="">全部省份</option><option v-for="p in provinces" :key="p">{{ p }}</option></select>
          <select v-model="line" aria-label="运营商"><option value="">全部运营商</option><option v-for="l in lines" :key="l">{{ l }}</option></select>
        </template>
        <span class="spacer"></span>
        <input type="text" v-model="query" placeholder="搜索名称、地址、省份" aria-label="搜索解析器" style="width:220px" />
      </div>
    </div>

    <div class="card empty" v-if="loading">加载中…</div>
    <div class="card empty" v-else-if="!shown.length">{{ family === '6' ? '没有符合条件且提供 IPv6 地址的解析器。' : '没有符合条件的解析器。' }}</div>
    <div class="resolver-grid" v-else>
      <div class="card resolver" v-for="r in shown" :key="r.id">
        <div class="row">
          <b>{{ r.name }}</b>
          <span class="badge">{{ scopeLabel[r.scope] }}</span>
          <span class="badge" v-if="r.line">{{ r.line }}</span>
          <span class="spacer"></span>
          <span class="sub">{{ r.org }}</span>
        </div>
        <div class="resolver-addrs">
          <div v-for="a in addrs(r)" :key="a" class="row">
            <span class="mono">{{ a }}</span>
            <span class="spacer"></span>
            <button class="btn link sm" type="button" @click="copy(a)">{{ copied === a ? '已复制' : '复制' }}</button>
            <button class="btn link sm" type="button" title="用这个解析器从各节点解析一个域名" @click="tryIt(a)">用它解析</button>
          </div>
        </div>
        <div class="kv resolver-extra" v-if="r.doh || r.dot">
          <template v-if="r.doh"><div>DoH</div><div class="mono ellipsis" :title="r.doh">{{ r.doh }} <button class="btn link sm" type="button" @click="copy(r.doh)">{{ copied === r.doh ? '已复制' : '复制' }}</button></div></template>
          <template v-if="r.dot"><div>DoT</div><div class="mono">{{ r.dot }}</div></template>
        </div>
        <div class="sub" v-if="r.note" style="margin-top:6px">{{ r.note }}</div>
      </div>
    </div>
  </div>
</template>
