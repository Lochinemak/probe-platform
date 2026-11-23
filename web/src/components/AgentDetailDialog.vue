<script setup>
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { api } from '../api.js'
import { timeAgo, fmtTime } from '../fmt.js'
import { LINES, PROVINCES, COUNTRIES, COMMON_COUNTRIES, OTHER_COUNTRIES, placeLabel } from '../geo.js'
import CapChips from './CapChips.vue'

const props = defineProps({
  agent: { type: Object, required: true },
  admin: { type: Boolean, default: false },
  serverVersion: { type: String, default: '' },
})
const emit = defineEmits(['close', 'changed', 'install', 'remove'])

const copiedKey = ref('')
const geoEditing = ref(false)
const geoForm = ref({ cc: '', province: '', city: '', line: '' })
const geoSaving = ref(false)
const geoError = ref('')

const outdated = computed(() => {
  const a = props.agent
  return Boolean(a.version && props.serverVersion && a.version !== 'dev' && props.serverVersion !== 'dev' && a.version !== props.serverVersion)
})

const placeText = computed(() => {
  const a = props.agent
  return (a.geo && placeLabel(a.geo)) || a.location || a.geo_location || ''
})

const lineText = computed(() => {
  const a = props.agent
  return a.geo?.line || a.isp || a.geo_isp || ''
})

const authInfo = computed(() => {
  const a = props.agent
  if (a.auth === 'token') return { label: '节点独立 token', tone: 'ok' }
  if (a.auth === 'legacy') return { label: '旧版共享 token', tone: 'warn', hint: '尚未迁移到独立 token' }
  return { label: '待接入', tone: '', hint: '已在控制台创建，尚未连接过' }
})

const hasReportedMeta = computed(() => {
  const a = props.agent
  return Boolean(a.location || a.geo_location || a.isp || a.geo_isp || a.tags?.length)
})

const countryChoices = computed(() => {
  const cc = geoForm.value.cc
  return cc && !COUNTRIES.some(([code]) => code === cc) ? [[cc, cc]] : []
})

function openGeoEdit() {
  const o = props.agent.geo_override || {}
  geoForm.value = { cc: o.cc || '', province: o.province || '', city: o.city || '', line: o.line || '' }
  geoError.value = ''
  geoEditing.value = true
}

function onCountryChange() {
  geoForm.value.province = ''
  geoForm.value.city = ''
}

async function saveGeo(clear = false) {
  geoSaving.value = true
  geoError.value = ''
  const f = geoForm.value
  try {
    await api.patchAgent(props.agent.id, {
      geo: clear ? null : { cc: f.cc, province: f.cc ? f.province : '', city: f.cc ? f.city : '', line: f.line },
    })
    geoEditing.value = false
    emit('changed')
  } catch (err) {
    geoError.value = err.message
  } finally {
    geoSaving.value = false
  }
}

async function copyText(key, text) {
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    const ta = document.createElement('textarea')
    ta.value = text
    document.body.appendChild(ta)
    ta.select()
    try { document.execCommand('copy') } catch { /* ignore */ } finally { ta.remove() }
  }
  copiedKey.value = key
  setTimeout(() => { if (copiedKey.value === key) copiedKey.value = '' }, 1400)
}

watch(() => props.agent.id, () => {
  geoEditing.value = false
  geoError.value = ''
})

function onKey(e) {
  if (e.key === 'Escape') {
    e.preventDefault()
    if (geoEditing.value) geoEditing.value = false
    else emit('close')
  }
}

onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <div class="backdrop" @click.self="emit('close')">
    <div class="card dialog node-dialog" role="dialog" aria-modal="true" :aria-label="`节点详情：${agent.name}`">
      <!-- Header -->
      <div class="nd-head">
        <div class="nd-title-wrap">
          <span class="status-dot" :class="{ online: agent.online }"></span>
          <h2 class="nd-title">{{ agent.name }}</h2>
          <span class="badge" :class="agent.online ? 'ok' : ''">{{ agent.online ? '在线' : (admin && !agent.auth ? '待接入' : '离线') }}</span>
          <span v-if="admin && agent.auth === 'legacy'" class="badge warn" title="还在用旧版共享 token">共享 token</span>
          <span v-if="admin && outdated" class="badge warn" title="与服务端版本不一致，重连后自动更新">待更新</span>
        </div>
        <button class="nd-close" type="button" aria-label="关闭节点详情" @click="emit('close')">
          <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round">
            <path d="M5 5l10 10M15 5L5 15" />
          </svg>
        </button>
      </div>

      <!-- Summary Tiles -->
      <div class="nd-grid">
        <div class="nd-tile">
          <div class="nd-tile-top">
            <span class="nd-k">地域 / 线路</span>
            <button v-if="admin && !geoEditing" class="btn link xs" type="button" @click="openGeoEdit">修正</button>
          </div>
          <div class="nd-v nd-place">
            <span :class="{ warn: !placeText && admin }">{{ placeText || '未识别' }}</span>
            <span v-if="lineText" class="badge">{{ lineText }}</span>
            <span v-if="admin && agent.geo?.src === 'manual'" class="nd-tag-muted">手动修正</span>
          </div>
        </div>

        <div class="nd-tile" v-if="admin">
          <div class="nd-tile-top">
            <span class="nd-k">公网 IP</span>
            <button v-if="agent.public_ip" class="btn link xs" type="button" @click="copyText('ip', agent.public_ip)">
              {{ copiedKey === 'ip' ? '已复制' : '复制' }}
            </button>
          </div>
          <div class="nd-v mono">{{ agent.public_ip || '—' }}</div>
        </div>

        <div class="nd-tile">
          <div class="nd-tile-top">
            <span class="nd-k">运行状态</span>
          </div>
          <div class="nd-v">
            <template v-if="agent.online">
              <span class="ok">在线</span>
              <span class="nd-sep">·</span>
              <span>{{ agent.running || 0 }} 个任务运行中</span>
            </template>
            <template v-else-if="admin">
              <span>{{ agent.auth ? `离线（${timeAgo(agent.last_seen)}）` : '从未连接' }}</span>
            </template>
            <template v-else>
              <span>离线</span>
            </template>
          </div>
          <div class="nd-sub" v-if="admin && agent.auth && agent.last_seen && !agent.last_seen.startsWith('0001-')">
            最后在线 {{ fmtTime(agent.last_seen) }}
          </div>
        </div>

        <div class="nd-tile" v-if="admin">
          <div class="nd-tile-top">
            <span class="nd-k">版本 / 系统</span>
          </div>
          <div class="nd-v mono">
            {{ agent.version || '—' }}
            <span v-if="agent.os" class="nd-sub-inline">· {{ agent.os }}/{{ agent.arch }}</span>
          </div>
          <div class="nd-sub warn" v-if="outdated">服务端版本 {{ serverVersion }}，节点重连后自动更新</div>
        </div>

        <template v-if="!admin">
          <div class="nd-tile">
            <div class="nd-tile-top"><span class="nd-k">位置</span></div>
            <div class="nd-v">{{ agent.location || '—' }}</div>
          </div>
          <div class="nd-tile">
            <div class="nd-tile-top"><span class="nd-k">运营商</span></div>
            <div class="nd-v">{{ agent.isp || '—' }}</div>
          </div>
        </template>
      </div>

      <!-- Inline Geo Override Editor (Admin) -->
      <form v-if="admin && geoEditing" class="sub-panel nd-geo-form" @submit.prevent="saveGeo(false)">
        <div class="nd-geo-head">
          <b>修正地域与线路</b>
          <span class="sub">当前：{{ agent.geo ? `${placeLabel(agent.geo) || '未识别'}${agent.geo.line ? ' · ' + agent.geo.line : ''}` : '未识别' }}</span>
        </div>
        <p class="sub" style="margin:0 0 10px">只填需要覆盖的项，留空继续由公网 IP 与上报标签自动识别；改地域时需选择国家或地区。</p>
        <div class="nd-geo-fields">
          <div class="field">
            <label>国家或地区</label>
            <select v-model="geoForm.cc" @change="onCountryChange">
              <option value="">自动识别</option>
              <option v-for="[cc, name] in countryChoices" :key="cc" :value="cc">{{ name }}</option>
              <optgroup label="常用"><option v-for="[cc, name] in COMMON_COUNTRIES" :key="cc" :value="cc">{{ name }}</option></optgroup>
              <optgroup label="其他（按拼音）"><option v-for="[cc, name] in OTHER_COUNTRIES" :key="cc" :value="cc">{{ name }}</option></optgroup>
            </select>
          </div>
          <div class="field" v-if="geoForm.cc === 'CN'">
            <label>省份</label>
            <select v-model="geoForm.province">
              <option value="">未指定</option>
              <option v-for="p in PROVINCES" :key="p">{{ p }}</option>
            </select>
          </div>
          <div class="field" v-else-if="geoForm.cc">
            <label>省 / 州</label>
            <input type="text" v-model="geoForm.province" maxlength="40" />
          </div>
          <div class="field" v-if="geoForm.cc">
            <label>城市</label>
            <input type="text" v-model="geoForm.city" maxlength="40" placeholder="可留空" />
          </div>
          <div class="field">
            <label>线路</label>
            <select v-model="geoForm.line">
              <option value="">自动识别</option>
              <option v-for="l in LINES" :key="l">{{ l }}</option>
            </select>
          </div>
        </div>
        <div class="error-box" v-if="geoError" style="margin:10px 0 0">{{ geoError }}</div>
        <div class="row" style="margin-top:12px">
          <button class="btn sm primary" type="submit" :disabled="geoSaving">{{ geoSaving ? '保存中…' : '保存修正' }}</button>
          <button class="btn sm" type="button" :disabled="geoSaving" @click="saveGeo(true)">恢复自动识别</button>
          <button class="btn link sm" type="button" @click="geoEditing = false">取消</button>
        </div>
      </form>

      <!-- Capabilities -->
      <div class="nd-section">
        <div class="nd-sec-title">探测能力</div>
        <CapChips :caps="agent.capabilities" />
      </div>

      <!-- Secondary Metadata (Admin) -->
      <div class="nd-section" v-if="admin">
        <div class="nd-sec-title">更多信息</div>
        <div class="nd-meta-list">
          <div class="nd-meta-row">
            <span class="nd-meta-k">节点 ID</span>
            <span class="nd-meta-v mono">
              {{ agent.id }}
              <button class="btn link xs" type="button" @click="copyText('id', agent.id)">{{ copiedKey === 'id' ? '已复制' : '复制' }}</button>
            </span>
          </div>
          <div class="nd-meta-row">
            <span class="nd-meta-k">认证方式</span>
            <span class="nd-meta-v">
              <span :class="authInfo.tone">{{ authInfo.label }}</span>
              <span v-if="authInfo.hint" class="sub" style="margin-left:6px">{{ authInfo.hint }}</span>
            </span>
          </div>
          <template v-if="hasReportedMeta">
            <div class="nd-meta-row" v-if="agent.location || agent.isp">
              <span class="nd-meta-k">上报标签</span>
              <span class="nd-meta-v">{{ [agent.location, agent.isp].filter(Boolean).join(' · ') }}</span>
            </div>
            <div class="nd-meta-row" v-if="agent.geo_location || agent.geo_isp">
              <span class="nd-meta-k">IP 归属地</span>
              <span class="nd-meta-v">{{ [agent.geo_location, agent.geo_isp].filter(Boolean).join(' · ') }}</span>
            </div>
            <div class="nd-meta-row" v-if="agent.tags?.length">
              <span class="nd-meta-k">标签</span>
              <span class="nd-meta-v nd-tags">
                <span v-for="t in agent.tags" :key="t" class="badge">{{ t }}</span>
              </span>
            </div>
          </template>
          <div class="nd-meta-row" v-if="agent.connected_at">
            <span class="nd-meta-k">本次连接</span>
            <span class="nd-meta-v">{{ fmtTime(agent.connected_at) }}（{{ timeAgo(agent.connected_at) }}）</span>
          </div>
          <div class="nd-meta-row" v-if="agent.first_seen && !agent.first_seen.startsWith('0001-')">
            <span class="nd-meta-k">首次接入</span>
            <span class="nd-meta-v">{{ fmtTime(agent.first_seen) }}</span>
          </div>
        </div>
      </div>

      <!-- Footer Actions -->
      <div class="nd-foot" v-if="admin">
        <button class="btn sm danger" type="button" @click="emit('remove', agent)">删除节点</button>
        <span class="spacer"></span>
        <button v-if="!geoEditing" class="btn sm" type="button" @click="openGeoEdit">修正地域</button>
        <button class="btn sm outline" type="button" @click="emit('install', agent)">安装命令</button>
      </div>
      <div class="nd-foot" v-else>
        <span class="sub">管理员登录后可查看公网 IP、系统版本等信息并管理节点。</span>
        <span class="spacer"></span>
        <button class="btn sm" type="button" @click="emit('close')">关闭</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.node-dialog {
  width: min(600px, 100%);
  padding: 20px 22px 18px;
}
.nd-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  padding-bottom: 14px;
  margin-bottom: 16px;
  border-bottom: 1px solid var(--border);
}
.nd-title-wrap {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  min-width: 0;
}
.nd-title {
  margin: 0;
  font-size: 17px;
  font-weight: 700;
  word-break: break-word;
}
.nd-close {
  flex: none;
  width: 30px;
  height: 30px;
  border-radius: 8px;
  border: 1px solid transparent;
  background: transparent;
  color: var(--muted);
  display: grid;
  place-items: center;
  cursor: pointer;
  transition: background-color .15s var(--ease), color .15s var(--ease);
}
.nd-close:hover { background: var(--bg-3); color: var(--text); }
.nd-close svg { width: 16px; height: 16px; }

.nd-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}
.nd-tile {
  background: var(--bg);
  border: 1px solid var(--border-subtle);
  border-radius: 10px;
  padding: 10px 12px;
  min-width: 0;
}
.nd-tile-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 4px;
}
.nd-k { font-size: 11.5px; font-weight: 600; color: var(--muted); }
.nd-v {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--text);
  word-break: break-word;
  line-height: 1.45;
}
.nd-place {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.nd-tag-muted {
  font-size: 11px;
  font-weight: 500;
  color: var(--muted);
}
.nd-sep { color: var(--muted); margin: 0 5px; font-weight: 400; }
.nd-sub { font-size: 11.5px; color: var(--muted); margin-top: 3px; }
.nd-sub.warn { color: var(--warn); }
.nd-sub-inline { font-family: var(--sans); font-size: 12px; font-weight: 500; color: var(--muted); }

.btn.xs { padding: 1px 6px; font-size: 11.5px; line-height: 1.4; }

.nd-geo-form { margin-top: 12px; }
.nd-geo-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 4px;
  font-size: 13px;
}
.nd-geo-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px 12px;
}
.nd-geo-fields select, .nd-geo-fields input { width: 100%; }

.nd-section {
  margin-top: 16px;
  padding-top: 14px;
  border-top: 1px solid var(--border-subtle);
}
.nd-sec-title {
  font-size: 12px;
  font-weight: 700;
  color: var(--muted);
  margin-bottom: 10px;
}

.nd-meta-list {
  display: grid;
  gap: 7px;
  font-size: 13px;
}
.nd-meta-row {
  display: grid;
  grid-template-columns: 82px minmax(0, 1fr);
  gap: 8px;
  align-items: baseline;
}
.nd-meta-k { color: var(--muted); font-size: 12px; }
.nd-meta-v { color: var(--text-2); word-break: break-word; }
.nd-tags { display: flex; flex-wrap: wrap; gap: 4px; }

.nd-foot {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 18px;
  padding-top: 14px;
  border-top: 1px solid var(--border);
}

@media (max-width: 560px) {
  .node-dialog { padding: 16px; }
  .nd-grid, .nd-geo-fields { grid-template-columns: 1fr; }
  .nd-meta-row { grid-template-columns: 72px minmax(0, 1fr); }
}
</style>
