<script setup>
// IP 定位: where an address is and whose it is, from the server's IP
// database, plus a comparison of the address this browser reaches the
// dashboard from with the one it reaches an overseas site from. Two different
// answers mean traffic is being split between routes (a proxy that sends
// domestic and foreign destinations different ways).
import { ref, computed, watch, shallowRef, onMounted } from 'vue'
import { api } from '../api.js'
import { placeLabel } from '../geo.js'
import { MAP_SCOPES, loadMap, regionId } from '../maps.js'
import { isIPv4, isIPv6 } from '../validate.js'
import ChoroplethMap from '../components/ChoroplethMap.vue'

const query = ref('')
const result = ref(null)
const error = ref('')
const busy = ref(false)

async function lookup(ip) {
  const q = String(ip ?? query.value).trim().replace(/^\[|\]$/g, '')
  error.value = ''
  if (!isIPv4(q) && !isIPv6(q)) { error.value = '请输入有效的 IPv4 或 IPv6 地址'; return }
  busy.value = true
  try { result.value = await api.ipLookup(q); query.value = q } catch (e) { error.value = e.message } finally { busy.value = false }
}

// --- the two exits ---
const here = ref(null) // as this server sees the browser
const abroad = ref(null) // as an overseas site sees it
const abroadState = ref('idle') // idle | working | done | error
const abroadError = ref('')
async function loadHere() { try { here.value = await api.ipMe() } catch { /* the lookup form still works */ } }
async function useMine() { if (!here.value) await loadHere(); if (here.value) { result.value = here.value; query.value = here.value.ip } }

// Asked only on a click: it tells a third party this browser's address.
const ABROAD = [
  { name: 'Cloudflare', url: 'https://www.cloudflare.com/cdn-cgi/trace', read: async (r) => (/^ip=(.+)$/m.exec(await r.text()) || [])[1] },
  { name: 'ipify', url: 'https://api.ipify.org?format=json', read: async (r) => (await r.json()).ip },
]
const abroadVia = ref('')
async function checkAbroad() {
  abroadState.value = 'working'
  abroadError.value = ''
  for (const s of ABROAD) {
    try {
      const r = await fetch(s.url, { cache: 'no-store', credentials: 'omit', referrerPolicy: 'no-referrer' })
      const ip = r.ok ? await s.read(r) : ''
      if (!ip) continue
      abroadVia.value = s.name
      abroad.value = await api.ipLookup(ip.trim())
      abroadState.value = 'done'
      return
    } catch { /* try the next one */ }
  }
  abroadState.value = 'error'
  abroadError.value = '没能连上境外的检测服务（Cloudflare、ipify 都失败了）。可能是网络不通，也可能被浏览器插件拦截。'
}
const verdict = computed(() => {
  if (!here.value || !abroad.value) return null
  if (here.value.ip === abroad.value.ip) return { same: true, text: '两个出口是同一个地址：访问本站和访问境外站点走的是同一条线路。' }
  if (here.value.version !== abroad.value.version) return { same: false, text: '两个出口的地址族不同（IPv4 / IPv6），不能直接比较；同一条线路的 v4 和 v6 地址本来就不一样。' }
  return { same: false, text: '两个出口不是同一个地址：访问境外站点走了另一条线路，通常是代理按目的地分流。' }
})

// --- map: the looked-up address and the two exits ---
const marks = computed(() => [
  result.value && { info: result.value, label: '查询的地址', fill: 'var(--accent)' },
  here.value && { info: here.value, label: '本站看到的出口', fill: 'var(--ok)' },
  abroad.value && { info: abroad.value, label: '境外看到的出口', fill: 'var(--warn)' },
].filter((m) => m && m.info.geo?.cc))
const scope = ref(MAP_SCOPES[0]?.id || '')
watch(marks, (list) => {
  // China's provinces when everything to show is in China, the world otherwise.
  const want = list.length && list.every((m) => m.info.geo.cc === 'CN' && m.info.geo.province) ? 'china' : 'world'
  if (MAP_SCOPES.some((s) => s.id === want)) scope.value = want
})
const map = shallowRef(null)
watch(scope, async (id) => { map.value = null; if (id) { try { const m = await loadMap(id); if (scope.value === id) map.value = m } catch { /* no map, the text is all there */ } } }, { immediate: true })
const mapValues = computed(() => {
  const out = {}
  for (const m of marks.value) {
    const id = regionId(scope.value, m.info.geo)
    if (!id) continue
    if (!out[id]) out[id] = { fill: m.fill, title: m.label, lines: [] }
    else out[id].title += '、' + m.label
    out[id].lines.push([m.label, m.info.ip])
  }
  return out
})

const kindText = { private: '内网 / 保留地址', fake_ip: 'fake-IP（透明代理分配的假地址）' }
const sourceText = { offline: '离线 IP 库', online: 'ip-api.com', 'offline+online': '离线 IP 库 + ip-api.com' }
const coords = (i) => (i?.lat !== undefined && i?.lat !== null ? `${i.lat.toFixed(2)}, ${i.lon.toFixed(2)}` : '')
onMounted(loadHere)
</script>

<template>
  <div>
    <div class="row" style="margin-bottom:12px">
      <div>
        <h2 style="margin:0">IP 定位</h2>
        <div class="sub">查询公网 IP 的归属地、运营商和线路类型。位置是 IP 库的估算，不是设备的实际位置。</div>
      </div>
    </div>

    <form class="card probe-main" @submit.prevent="lookup()">
      <div class="target-box" :class="{ invalid: !!error }">
        <input class="target-input" type="text" v-model="query" placeholder="输入 IPv4 或 IPv6 地址" aria-label="IP 地址" spellcheck="false" autocomplete="off" />
      </div>
      <div class="probe-actions">
        <button class="btn primary" type="submit" :disabled="busy">{{ busy ? '查询中…' : '查询' }}</button>
        <button class="btn outline" type="button" @click="useMine">查询我的 IP</button>
      </div>
      <div class="field-error" v-if="error" role="alert" style="flex-basis:100%;margin:0">{{ error }}</div>
    </form>

    <div class="card" v-if="result">
      <div class="row">
        <h2 class="mono" style="margin:0;font-size:20px">{{ result.ip }}</h2>
        <span class="badge">IPv{{ result.version }}</span>
        <span class="badge warn" v-if="result.kind !== 'public'">{{ kindText[result.kind] }}</span>
      </div>
      <div class="kv ip-kv" v-if="result.kind === 'public'">
        <div>位置</div><div>{{ result.location || (result.geo ? placeLabel(result.geo) : '未知') }}</div>
        <div>运营商</div><div>{{ result.isp || '未知' }}<span class="badge" v-if="result.geo?.line" style="margin-left:8px">{{ result.geo.line }}</span></div>
        <div>ASN</div><div>{{ result.asn || '—' }}</div>
        <div>网络类型</div><div>{{ result.net_type || '—' }}</div>
        <div>估算坐标</div><div>{{ coords(result) || '—' }}</div>
        <div>数据来源</div><div>{{ sourceText[result.source] || '没有查到' }}</div>
      </div>
      <p class="sub" v-else style="margin-bottom:0">{{ result.kind === 'fake_ip' ? '198.18.0.0/15 是 Clash、OpenClash 等透明代理的 fake-IP 地址段，不对应任何真实位置。' : '这是内网、回环或保留地址，没有归属地。' }}</p>
      <p class="sub" v-if="result.kind === 'public' && !result.asn" style="margin-bottom:0">ASN、网络类型和坐标来自在线查询；离线库已有归属地或在线查询未开启时不显示。</p>
    </div>

    <div class="card">
      <h3>当前浏览器的出口对照</h3>
      <p class="sub" style="margin-top:0">访问本站用的出口和访问境外站点用的出口分开显示。两者不同，说明网络按目的地分流（常见于代理软件的"国内直连、国外走代理"）。</p>
      <div class="egress">
        <div class="metric">
          <div class="stat-label">本站看到的出口</div>
          <div class="metric-value">{{ here?.ip || '识别中…' }}</div>
          <div class="sub" v-if="here">{{ here.kind === 'public' ? [here.location || placeLabel(here.geo), here.isp].filter(Boolean).join(' · ') || '归属地未知' : kindText[here.kind] }}</div>
        </div>
        <div class="metric">
          <div class="stat-label">境外站点看到的出口</div>
          <template v-if="abroad">
            <div class="metric-value">{{ abroad.ip }}</div>
            <div class="sub">{{ [abroad.location || placeLabel(abroad.geo), abroad.isp, abroad.net_type].filter(Boolean).join(' · ') || '归属地未知' }} · 经 {{ abroadVia }} 检测</div>
          </template>
          <template v-else>
            <button class="btn" type="button" style="margin:6px 0" :disabled="abroadState === 'working'" @click="checkAbroad">{{ abroadState === 'working' ? '检测中…' : '检测境外出口' }}</button>
            <div class="sub">点击后浏览器会直接请求 Cloudflare（失败时改用 ipify）的"我的 IP"接口，对方会看到你的出口地址。不点不会发起。</div>
          </template>
        </div>
      </div>
      <div class="error-box" v-if="abroadError" style="margin:12px 0 0">{{ abroadError }}</div>
      <p v-if="verdict" :class="verdict.same ? 'ok' : 'warn'" style="margin:12px 0 0">{{ verdict.text }}</p>
    </div>

    <div class="card" v-if="marks.length && map">
      <div class="row">
        <h3 style="margin:0">地图</h3>
        <span class="spacer"></span>
        <div class="legend" style="margin:0"><span v-for="m in marks" :key="m.label"><i :style="{ background: m.fill }"></i>{{ m.label }}</span></div>
      </div>
      <ChoroplethMap :map="map" :values="mapValues" label="地图：地址所在的省份或国家" />
      <div class="sub map-credit">按省份 / 国家标出，不是精确位置。{{ map.credit }}</div>
    </div>
  </div>
</template>
