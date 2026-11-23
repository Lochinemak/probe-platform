<script setup>
import { ref, computed, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { api } from '../api.js'
import { timeAgo, fmtTime } from '../fmt.js'
import { placeLabel } from '../geo.js'
import Onboarding from '../components/Onboarding.vue'
import AgentDetailDialog from '../components/AgentDetailDialog.vue'

const props = defineProps({
  agentImage: { type: String, default: 'ghcr.io/lochinemak/probe-agent:latest' },
  serverVersion: { type: String, default: '' },
  admin: { type: Boolean, default: true },
})

const agents = ref([])
const error = ref('')
const downloads = ref([])
const legacyToken = ref('none') // shared token of earlier versions: none | enabled | disabled
const selected = ref(null) // node whose install command is shown in Onboarding
const onboarding = ref(null)
const detailId = ref('') // node currently open in AgentDetailDialog
const statusFilter = ref('all') // all | online | offline
const keyword = ref('')

function outdated(a) {
  return Boolean(a.version && props.serverVersion && a.version !== 'dev' && props.serverVersion !== 'dev' && a.version !== props.serverVersion)
}
function placeOf(a) {
  return (a.geo && placeLabel(a.geo)) || a.location || a.geo_location || ''
}
function lineOf(a) {
  return a.geo?.line || a.isp || a.geo_isp || ''
}

async function loadDownloads() {
  try { downloads.value = (await api.agentFiles()).files || [] } catch { downloads.value = [] }
}

let timer = null
async function load() {
  try {
    const r = await api.agents()
    agents.value = r.agents
    legacyToken.value = r.legacy_token || 'none'
  } catch (e) { if (e.status !== 401) error.value = e.message }
}

async function remove(a) {
  const msg = `删除节点 ${a.name}？\n\n它的 token 会立即失效${a.online ? '，正在运行的 agent 会被断开' : ''}，那台机器上的 agent 再也连不上；历史记录保留。` +
    '\n要重新接入只能新建节点。卸载那台机器上的 agent 请用「接入新节点」卡片里的卸载命令。'
  if (!confirm(msg)) return
  try {
    await api.deleteAgent(a.id)
    if (selected.value?.id === a.id) selected.value = null
    if (detailId.value === a.id) detailId.value = ''
    await load()
  } catch (e) { error.value = e.message }
}

async function showInstall(a) {
  detailId.value = ''
  selected.value = a
  await nextTick()
  onboarding.value?.$el?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

async function addNewNode() {
  if (selected.value) {
    onboarding.value?.startOver?.()
    selected.value = null
  }
  await nextTick()
  onboarding.value?.$el?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

const online = computed(() => agents.value.filter((a) => a.online).length)
const outdatedCount = computed(() => agents.value.filter(outdated).length)
const legacyNodes = computed(() => agents.value.filter((a) => a.auth === 'legacy'))
const detailAgent = computed(() => agents.value.find((a) => a.id === detailId.value) || null)

const filteredAgents = computed(() => {
  const q = keyword.value.trim().toLowerCase()
  return agents.value.filter((a) => {
    if (statusFilter.value === 'online' && !a.online) return false
    if (statusFilter.value === 'offline' && a.online) return false
    if (!q) return true
    const hay = [
      a.name, a.id, placeOf(a), lineOf(a),
      props.admin ? a.public_ip : '',
      ...(a.tags || []),
    ].join(' ').toLowerCase()
    return hay.includes(q)
  })
})

async function setLegacy(enabled) {
  if (!enabled) {
    const left = legacyNodes.value
    const msg = left.length
      ? `仍有 ${left.length} 个节点在用共享 token：${left.map((a) => a.name).join('、')}。\n\n停用后它们会立即掉线，直到用各自的「安装命令」重装。确定停用？`
      : '停用旧版共享 token？此后只有节点自己的 token 能连接和下载 agent。'
    if (!confirm(msg)) return
  }
  try { await api.updateSettings({ legacy_agent_token: enabled }); await load() } catch (e) { error.value = e.message }
}

onMounted(() => { load(); if (props.admin) loadDownloads(); timer = setInterval(load, 5000) })
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div>
    <div class="error-box" v-if="error">{{ error }}</div>

    <div class="card" v-if="props.admin && legacyToken === 'enabled'" style="border-color:var(--warn)">
      <h3>迁移到节点独立 token <span class="sub">{{ legacyNodes.length ? `${legacyNodes.length} 个节点仍在用旧版共享 token` : '全部节点已迁移' }}</span></h3>
      <p class="sub" style="margin-top:0">现在每个节点都有自己的 token，服务端按 token 认节点，不再相信节点自报的名字。旧版共享 token 目前仍被接受，但只对升级前就存在、尚未迁移的节点有效：</p>
      <ul class="sub" style="margin:0 0 10px;padding-left:20px">
        <li><b>systemd / OpenWrt / Windows 节点自动迁移</b>：它们自更新到新版后，服务端会把节点自己的 token 交给它，agent 存到二进制旁的 <code>probe-agent.token</code> 并立即改用它重连，无需任何操作。</li>
        <li><b>Docker / 群晖 / 威联通节点</b>不自更新，需要手动迁移：点开该节点详情里的「安装命令」，按 Docker / 群晖 / 威联通的命令重建容器（同一个 token 覆盖安装，节点和历史不变）。</li>
        <li>标着 <span class="badge warn">共享 token</span> 的节点就是还没迁移的。全部迁移后点下面的按钮停用共享 token，此后只有节点自己的 token 能连接、下载 agent，环境变量里的 <code>PROBE_AGENT_TOKEN</code> 也可以删掉。</li>
      </ul>
      <button class="btn sm danger" type="button" @click="setLegacy(false)">停用共享 token</button>
    </div>
    <p class="sub" v-if="props.admin && legacyToken === 'disabled'" style="margin:0 0 12px">旧版共享 token 已停用，只有节点自己的 token 能连接。
      <button class="btn link sm" type="button" @click="setLegacy(true)">重新启用</button></p>

    <div class="card">
      <div class="node-bar">
        <div class="node-bar-left">
          <h3 style="margin:0">节点列表</h3>
          <span class="node-pill">
            <span class="status-dot" :class="{ online: online > 0 }"></span>
            {{ online }} / {{ agents.length }} 在线
          </span>
          <span v-if="props.admin && outdatedCount" class="badge warn">{{ outdatedCount }} 待更新</span>
        </div>
        <div class="node-bar-right">
          <div v-if="agents.length && online > 0 && online < agents.length" class="tabs sm" role="group" aria-label="按在线状态筛选">
            <button type="button" :class="{ active: statusFilter === 'all' }" @click="statusFilter = 'all'">全部</button>
            <button type="button" :class="{ active: statusFilter === 'online' }" @click="statusFilter = 'online'">在线</button>
            <button type="button" :class="{ active: statusFilter === 'offline' }" @click="statusFilter = 'offline'">离线</button>
          </div>
          <input
            v-if="agents.length >= 6"
            v-model="keyword"
            type="text"
            class="node-search"
            :placeholder="props.admin ? '筛选节点、地域、IP…' : '筛选节点、地域…'"
            aria-label="筛选节点"
          />
          <button v-if="props.admin" class="btn sm outline" type="button" @click="addNewNode">+ 接入节点</button>
        </div>
      </div>

      <div class="table-wrap node-table-wrap">
        <table class="grid node-table" :class="{ 'is-admin': props.admin }">
          <thead>
            <tr>
              <th class="col-node">节点</th>
              <th class="col-geo">地域 / 线路</th>
              <th v-if="props.admin" class="col-ip">公网 IP</th>
              <th class="col-state">状态</th>
              <th class="col-act"></th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="a in filteredAgents"
              :key="a.id"
              class="clickable node-row"
              :class="{ 'is-offline': !a.online, 'is-active': detailId === a.id }"
              tabindex="0"
              @click="detailId = a.id"
              @keydown.enter.prevent="detailId = a.id"
              @keydown.space.prevent="detailId = a.id"
            >
              <td>
                <div class="node-cell">
                  <div class="node-main">
                    <span class="status-dot" :class="{ online: a.online }"></span>
                    <span class="node-name" :title="a.name">{{ a.name }}</span>
                  </div>
                  <div v-if="props.admin && (a.auth === 'legacy' || !a.auth || outdated(a))" class="node-flags">
                    <span v-if="a.auth === 'legacy'" class="badge warn node-flag" title="还在用旧版共享 token">共享 token</span>
                    <span v-else-if="!a.auth" class="badge node-flag" title="已在控制台创建，尚未连接过">待接入</span>
                    <span v-if="outdated(a)" class="badge warn node-flag" title="与服务端版本不一致，重连后自动更新">待更新</span>
                  </div>
                </div>
              </td>
              <td>
                <div class="geo-cell">
                  <span v-if="placeOf(a)" class="geo-place" :title="placeOf(a)">{{ placeOf(a) }}</span>
                  <span v-else :class="props.admin ? 'warn' : 'sub'">未识别</span>
                  <span v-if="lineOf(a)" class="line-pill">{{ lineOf(a) }}</span>
                </div>
              </td>
              <td v-if="props.admin" class="col-ip">
                <span class="mono ip-cell" :title="a.public_ip || ''">{{ a.public_ip || '—' }}</span>
              </td>
              <td>
                <div class="state-cell">
                  <template v-if="a.online">
                    <span class="state-online">在线</span>
                    <span v-if="a.running > 0" class="badge running">{{ a.running }} 运行中</span>
                  </template>
                  <span v-else class="sub" :title="props.admin && a.auth ? fmtTime(a.last_seen) : ''">
                    {{ props.admin ? (a.auth ? timeAgo(a.last_seen) : '从未连接') : '离线' }}
                  </span>
                </div>
              </td>
              <td class="col-act">
                <span class="node-more">
                  <span class="node-more-label">详情</span>
                  <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <path d="M6 3.5L10.5 8 6 12.5" />
                  </svg>
                </span>
              </td>
            </tr>
            <tr v-if="!filteredAgents.length">
              <td :colspan="props.admin ? 5 : 4" class="empty">
                {{ agents.length ? '没有匹配的节点' : '暂无节点' }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <p class="sub node-foot-hint" v-if="agents.length">
        <template v-if="props.admin">点击任意节点查看探测能力、系统版本、修正地域或获取安装命令。</template>
        <template v-else>点击任意节点查看探测能力与详情；管理员登录后可查看公网 IP、版本并管理节点。</template>
      </p>
    </div>

    <AgentDetailDialog
      v-if="detailAgent"
      :agent="detailAgent"
      :admin="props.admin"
      :server-version="props.serverVersion"
      @close="detailId = ''"
      @changed="load"
      @install="showInstall"
      @remove="remove"
    />

    <Onboarding
      v-if="props.admin"
      ref="onboarding"
      :agent-image="props.agentImage"
      :server-version="props.serverVersion"
      :downloads="downloads"
      :node="selected"
      @changed="load"
      @clear="selected = null"
    />
  </div>
</template>

<style scoped>
.node-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px 14px;
  flex-wrap: wrap;
  margin-bottom: 14px;
}
.node-bar-left, .node-bar-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.node-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 2px 10px;
  border-radius: 999px;
  background: var(--bg);
  border: 1px solid var(--border);
  color: var(--text-2);
  font-size: 12px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.node-search {
  width: 190px;
  padding: 5px 10px !important;
  font-size: 12.5px !important;
  min-height: 30px;
}

.node-table-wrap { overflow-x: hidden; }
.node-table {
  table-layout: fixed;
  width: 100%;
}
.node-table th, .node-table td {
  padding: 12px 14px;
}

/* Column proportions — fit cleanly within card without horizontal scroll */
.node-table.is-admin .col-node { width: 32%; }
.node-table.is-admin .col-geo { width: 26%; }
.node-table.is-admin .col-ip { width: 20%; }
.node-table.is-admin .col-state { width: 15%; }
.node-table.is-admin .col-act { width: 68px; }

.node-table:not(.is-admin) .col-node { width: 38%; }
.node-table:not(.is-admin) .col-geo { width: 35%; }
.node-table:not(.is-admin) .col-state { width: 20%; }
.node-table:not(.is-admin) .col-act { width: 68px; }

.node-row:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
}
.node-row.is-active td { background: var(--emerald-soft); }
.node-row.is-offline .node-name,
.node-row.is-offline .geo-place,
.node-row.is-offline .ip-cell {
  color: var(--muted);
}

.node-cell {
  display: flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
}
.node-main {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.node-name {
  font-weight: 600;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
.node-flags {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex: none;
}
.node-flag { flex: none; }

.geo-cell {
  display: flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
}
.geo-place {
  color: var(--text-2);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
.line-pill {
  flex: none;
  display: inline-flex;
  align-items: center;
  padding: 1px 7px;
  border-radius: 6px;
  font-size: 11.5px;
  font-weight: 600;
  color: var(--text-2);
  background: var(--bg-3);
  border: 1px solid var(--border);
  line-height: 1.45;
}

.ip-cell {
  display: block;
  color: var(--text-2);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.state-cell {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  overflow: hidden;
}
.state-online {
  font-size: 13px;
  font-weight: 600;
  color: var(--ok);
  flex: none;
}

.col-act { text-align: right !important; }
.node-more {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  font-size: 12.5px;
  font-weight: 500;
  color: var(--muted);
  transition: color .15s var(--ease);
}
.node-more svg {
  width: 14px;
  height: 14px;
  transition: transform .15s var(--ease);
}
.node-row:hover .node-more,
.node-row.is-active .node-more {
  color: var(--accent);
}
.node-row:hover .node-more svg {
  transform: translateX(2px);
}

.node-foot-hint {
  margin: 10px 2px 0;
  font-size: 12px;
}

@media (max-width: 760px) {
  .node-table th, .node-table td { padding: 10px 8px; }
  .node-table.is-admin .col-ip { display: none; }
  .node-table.is-admin .col-node,
  .node-table:not(.is-admin) .col-node { width: 40%; }
  .node-table.is-admin .col-geo,
  .node-table:not(.is-admin) .col-geo { width: 28%; }
  .node-table.is-admin .col-state,
  .node-table:not(.is-admin) .col-state { width: 24%; }
  .node-table.is-admin .col-act,
  .node-table:not(.is-admin) .col-act { width: 26px; padding-left: 0; }
  .node-cell { flex-direction: column; align-items: flex-start; gap: 4px; }
  .node-main { max-width: 100%; }
  .node-flags { padding-left: 15px; flex-wrap: wrap; }
  .geo-cell { flex-wrap: wrap; gap: 4px; }
  .state-cell { font-size: 12px; }
  .node-more-label { display: none; }
  .node-search { flex: 1 1 160px; width: auto; }
}
</style>
