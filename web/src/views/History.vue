<script setup>
import { ref, onMounted, watch } from 'vue'
import { api } from '../api.js'
import { typeLabel, fmtTime, timeAgo } from '../fmt.js'
import TaskResults from '../components/TaskResults.vue'

const props = defineProps({ initialId: String, admin: Boolean })
const tasks = ref([])
const error = ref('')
const loading = ref(false)
const current = ref(null) // snapshot { task, results }
const page = ref(0)
const pageSize = 50
const showMonitors = ref(false)

async function load() {
  loading.value = true
  try { tasks.value = (await api.tasks(pageSize, page.value * pageSize, showMonitors.value ? '*' : '')).tasks } catch (e) { if (e.status !== 401) error.value = e.message } finally { loading.value = false }
}
async function open(id) {
  error.value = ''
  try { current.value = await api.task(id) } catch (e) { error.value = e.message }
}
function paramSummary(p) {
  return Object.entries(p || {}).filter(([, v]) => v !== '' && v !== false && v !== 0).map(([k, v]) => `${k}=${v}`).join(' ')
}
// Who started a task, for the admin's view: "admin:<name>", "guest:<id>" or
// "" for scheduled monitor runs. Guests get a short id so different visitors
// can be told apart.
function ownerLabel(t) {
  if (t.monitor_id) return '定时监控'
  const o = t.owner || ''
  if (o.startsWith('admin:')) return `管理员 ${o.slice(6)}`
  if (o.startsWith('guest:')) return `游客 ${o.slice(6, 14)}`
  return '—'
}
watch(() => props.initialId, (id) => { if (id) open(id) })
// Logging in or out changes what the server lets us see.
watch(() => props.admin, () => { page.value = 0; current.value = null; load() })
onMounted(() => { load(); if (props.initialId) open(props.initialId) })
</script>

<template>
  <div>
    <div class="error-box" v-if="error">{{ error }}</div>
    <div class="card" v-if="current">
      <div class="row" style="margin-bottom:10px">
        <button class="btn sm" @click="current = null">← 返回列表</button>
        <span class="sub mono">{{ current.task.id }}</span>
      </div>
      <TaskResults :task="current.task" :results="current.results || []" :running="false" />
    </div>
    <div class="card" v-else>
      <div class="row" style="margin-bottom:10px">
        <h3 style="margin:0">历史记录</h3>
        <span class="spacer"></span>
        <button class="btn sm" :disabled="page === 0" @click="page--; load()">上一页</button>
        <span class="sub">第 {{ page + 1 }} 页</span>
        <button class="btn sm" :disabled="tasks.length < pageSize" @click="page++; load()">下一页</button>
        <label class="field inline sub" v-if="admin"><input type="checkbox" v-model="showMonitors" @change="page = 0; load()" /> 含定时监控的运行</label>
        <button class="btn sm" @click="load">刷新</button>
      </div>
      <p class="sub" v-if="!admin" style="margin:0 0 10px">游客只能看到本浏览器发起的拨测记录；管理员登录后可以查看所有人的记录。</p>
      <div class="table-wrap">
        <table class="grid">
          <thead><tr><th>类型</th><th>目标</th><th class="num">节点数</th><th>参数</th><th v-if="admin">来源</th><th>时间</th></tr></thead>
          <tbody>
            <tr v-for="t in tasks" :key="t.id" class="clickable" @click="open(t.id)">
              <td><span class="badge" :class="t.type">{{ typeLabel[t.type] }}</span></td>
              <td class="mono">{{ t.target }}</td>
              <td class="num">{{ t.agent_ids?.length || 0 }}</td>
              <td class="sub mono">{{ paramSummary(t.params) }}</td>
              <td class="sub" v-if="admin" :title="t.owner || ''">{{ ownerLabel(t) }}</td>
              <td class="sub" :title="fmtTime(t.created_at)">{{ timeAgo(t.created_at) }}</td>
            </tr>
            <tr v-if="!tasks.length && !loading"><td :colspan="admin ? 6 : 5" class="empty">暂无记录</td></tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>
