<script setup>
// A shared result (/s/<token>): read-only, opened without a session. The
// server has already removed whatever the person who shared it chose to
// withhold, so this page simply renders what it is given.
import { ref, watch, onBeforeUnmount } from 'vue'
import { api, subscribeShare } from '../api.js'
import { fmtTime } from '../fmt.js'
import { TYPE_INFO } from '../validate.js'
import TaskResults from '../components/TaskResults.vue'

const props = defineProps({ token: String })
const share = ref(null)
const task = ref(null)
const results = ref([])
const running = ref(false)
const error = ref('')
const loading = ref(true)
let unsubscribe = null
function close() { if (unsubscribe) { unsubscribe(); unsubscribe = null } }

function applyEvent(kind, ev) {
  if (kind === 'snapshot') {
    task.value = ev.task
    results.value = ev.results || []
    if (ev.done) running.value = false
  } else if (kind === 'progress') {
    const r = results.value.find((x) => x.agent_id === ev.agent_id)
    if (r) {
      r.progress = r.progress || []
      r.progress.push(ev.progress)
      if (r.progress.length > 1000) r.progress.splice(0, r.progress.length - 1000)
    }
  } else if (kind === 'result') {
    const i = results.value.findIndex((x) => x.agent_id === ev.agent_id)
    if (i >= 0) results.value[i] = ev.result
    else results.value.push(ev.result)
  } else if (kind === 'done') {
    running.value = false
  }
}

async function load(token) {
  close()
  loading.value = true
  error.value = ''
  task.value = null
  try {
    const page = await api.share(token)
    share.value = page.share
    task.value = page.task
    results.value = page.results || []
    running.value = !!page.share.live
    document.title = `${TYPE_INFO[page.task.type]?.title || '探测结果'} · 分享结果 · 拨测平台`
    if (page.share.live) unsubscribe = subscribeShare(token, applyEvent, () => { running.value = false })
  } catch (e) {
    error.value = e.status === 404 ? '这个分享链接无效、已过期，或已被撤销。' : e.status === 429 ? '打开得太频繁了，请稍后再试。' : '无法打开分享结果：' + e.message
    document.title = '分享结果 · 拨测平台'
  } finally { loading.value = false }
}
watch(() => props.token, load, { immediate: true })
onBeforeUnmount(close)
</script>

<template>
  <div>
    <div class="card empty" v-if="loading">正在加载分享结果…</div>
    <div class="card empty" v-else-if="error">
      <p>{{ error }}</p>
      <a class="btn" href="/">发起新的探测</a>
    </div>
    <template v-else>
      <div class="card">
        <TaskResults :task="task" :results="results" :running="running" :share="share" />
      </div>
      <p class="sub" style="text-align:center">
        这是一份只读的分享结果<template v-if="share.mode === 'snapshot'">（创建链接时的快照）</template>，{{ fmtTime(share.expires_at) }} 到期。
        <template v-if="!share.show_target || !share.show_ips || !share.show_nodes">分享者隐藏了{{ [!share.show_target && '探测目标', !share.show_ips && '响应 IP', !share.show_nodes && '节点名称'].filter(Boolean).join('、') }}。</template>
      </p>
    </template>
  </div>
</template>
