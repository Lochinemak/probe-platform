<script setup>
// 分享: create a public, revocable link to this result and manage the links
// already made. What a link reveals is chosen here and enforced by the
// server; the defaults share the measurements and nothing that identifies the
// target or the nodes.
import { ref, reactive, onMounted, onBeforeUnmount } from 'vue'
import { api } from '../api.js'
import { fmtTime, timeAgo } from '../fmt.js'
import { sharePath } from '../route.js'

const props = defineProps({ task: Object, running: Boolean })
const emit = defineEmits(['close'])

const form = reactive({ mode: 'follow', expires: '7d', show_target: false, show_time: true, show_ips: false, show_nodes: false })
const shares = ref([])
const error = ref('')
const busy = ref(false)
const created = ref(null)
const copied = ref('')
const urlOf = (s) => location.origin + sharePath(s.token)

async function load() {
  try { shares.value = (await api.shares(props.task.id)).shares } catch (e) { error.value = '加载分享记录失败：' + e.message }
}
async function create() {
  busy.value = true
  error.value = ''
  try {
    created.value = (await api.createShare(props.task.id, { ...form })).share
    await load()
  } catch (e) { error.value = e.message } finally { busy.value = false }
}
async function revoke(s) {
  if (!confirm('撤销后这个链接立即失效，且不能恢复。确定撤销？')) return
  try {
    await api.revokeShare(s.token)
    if (created.value?.token === s.token) created.value = null
    await load()
  } catch (e) { error.value = '撤销分享失败：' + e.message }
}
async function copy(s) {
  try { await navigator.clipboard.writeText(urlOf(s)); copied.value = s.token; setTimeout(() => { if (copied.value === s.token) copied.value = '' }, 1500) } catch { error.value = '复制失败，请手动选择链接' }
}
function shown(s) {
  const parts = [s.show_target && '目标', s.show_time && '时间', s.show_ips && '响应 IP', s.show_nodes && '节点名称'].filter(Boolean)
  return parts.length ? '公开 ' + parts.join('、') : '仅公开检测结果'
}
function state(s) { return s.revoked_at ? '已撤销' : s.active ? '有效' : '已过期' }
function onKey(e) { if (e.key === 'Escape') { e.preventDefault(); emit('close') } }
onMounted(() => { load(); window.addEventListener('keydown', onKey) })
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <div class="backdrop" @click.self="emit('close')">
    <div class="card dialog" role="dialog" aria-modal="true" aria-label="分享探测结果">
      <div class="row" style="margin-bottom:10px">
        <h2 style="margin:0;font-size:16px">分享探测结果</h2>
        <span class="spacer"></span>
        <button class="btn sm" type="button" aria-label="关闭分享设置" @click="emit('close')">关闭</button>
      </div>
      <p class="sub" style="margin-top:0">生成一个不用登录就能打开的只读链接，可以随时撤销。链接里公开什么由下面的开关决定，服务器会把没勾选的内容从数据里去掉，而不只是不显示。</p>

      <div class="field" v-if="running">
        <label>分享模式</label>
        <div class="tabs sm" role="group" aria-label="分享模式">
          <button type="button" :class="{ active: form.mode === 'follow' }" @click="form.mode = 'follow'">跟随任务</button>
          <button type="button" :class="{ active: form.mode === 'snapshot' }" @click="form.mode = 'snapshot'">当前快照</button>
        </div>
        <span class="sub">{{ form.mode === 'follow' ? '持续接收后续结果，任务结束后自动冻结。' : '只分享创建链接时已经返回的结果。' }}</span>
      </div>
      <div class="field" style="margin-top:12px">
        <label>有效期</label>
        <div class="tabs sm" role="group" aria-label="有效期">
          <button v-for="[v, label] in [['24h', '24 小时'], ['7d', '7 天'], ['30d', '30 天']]" :key="v" type="button" :class="{ active: form.expires === v }" @click="form.expires = v">{{ label }}</button>
        </div>
      </div>
      <fieldset class="share-flags">
        <legend>公开内容</legend>
        <label><input type="checkbox" v-model="form.show_target" /> 探测目标 <span class="sub">目标地址，以及跳转链、证书主体、响应头里出现的名字</span></label>
        <label><input type="checkbox" v-model="form.show_time" /> 检测时间</label>
        <label><input type="checkbox" v-model="form.show_ips" /> 响应 IP <span class="sub">解析到的地址、路由跳点、DNS 记录值</span></label>
        <label><input type="checkbox" v-model="form.show_nodes" /> 节点名称与位置 <span class="sub">关闭时只显示"节点 1、2…"和所在省份、线路</span></label>
      </fieldset>
      <div class="error-box" v-if="error" style="margin:12px 0 0">{{ error }}</div>
      <div class="row" style="margin-top:12px">
        <button class="btn primary" type="button" :disabled="busy" @click="create">{{ busy ? '正在创建…' : '创建分享链接' }}</button>
      </div>

      <div class="share-link" v-if="created">
        <input type="text" readonly :value="urlOf(created)" aria-label="分享链接" @focus="$event.target.select()" />
        <button class="btn sm" type="button" @click="copy(created)">{{ copied === created.token ? '已复制' : '复制' }}</button>
        <a class="btn sm" :href="urlOf(created)" target="_blank" rel="noopener">打开</a>
      </div>

      <h3 style="margin:18px 0 8px">分享记录</h3>
      <div class="sub" v-if="!shares.length">暂无分享记录</div>
      <div class="share-row" v-for="s in shares" :key="s.token">
        <span class="badge" :class="s.active ? 'ok' : ''">{{ state(s) }}</span>
        <span>{{ s.mode === 'follow' ? '跟随任务' : '当前快照' }} · {{ shown(s) }}</span>
        <span class="sub" :title="'到期 ' + fmtTime(s.expires_at)">创建于 {{ timeAgo(s.created_at) }} · 访问 {{ s.visits }} 次 · {{ fmtTime(s.expires_at) }} 到期</span>
        <span class="spacer"></span>
        <template v-if="s.active">
          <button class="btn link sm" type="button" @click="copy(s)">{{ copied === s.token ? '已复制' : '复制链接' }}</button>
          <button class="btn link sm danger" type="button" @click="revoke(s)">撤销</button>
        </template>
      </div>
    </div>
  </div>
</template>
