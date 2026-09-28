<script setup>
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { api } from './api.js'
import ProbeView from './views/Probe.vue'
import AgentsView from './views/Agents.vue'
import HistoryView from './views/History.vue'

const tab = ref('probe')
const session = ref(null)
const password = ref('')
const loginError = ref('')
const busy = ref(false)
const historyTaskId = ref('')

async function loadSession() {
  try { session.value = await api.session() } catch (e) { session.value = { auth_required: false, authenticated: true, error: e.message } }
}
async function login() {
  busy.value = true
  loginError.value = ''
  try {
    await api.login(password.value)
    password.value = ''
    await loadSession()
  } catch (e) { loginError.value = e.message } finally { busy.value = false }
}
async function logout() {
  await api.logout().catch(() => {})
  await loadSession()
}
function onUnauthorized() { if (session.value) session.value.authenticated = false }
function openHistory(id) { historyTaskId.value = id; tab.value = 'history' }

onMounted(() => { loadSession(); window.addEventListener('probe:unauthorized', onUnauthorized) })
onBeforeUnmount(() => window.removeEventListener('probe:unauthorized', onUnauthorized))
</script>

<template>
  <div class="app">
    <header class="topbar">
      <div class="brand"><span class="dot"></span> 拨测平台</div>
      <nav class="nav" v-if="session && session.authenticated">
        <button :class="{ active: tab === 'probe' }" @click="tab = 'probe'">拨测</button>
        <button :class="{ active: tab === 'agents' }" @click="tab = 'agents'">节点</button>
        <button :class="{ active: tab === 'history' }" @click="tab = 'history'">历史</button>
      </nav>
      <div class="spacer"></div>
      <div class="meta" v-if="session">
        <span v-if="session.version">v{{ session.version }}</span>
        <button v-if="session.auth_required && session.authenticated" class="btn sm" @click="logout">退出</button>
      </div>
    </header>

    <div v-if="!session" class="empty">加载中…</div>

    <div v-else-if="session.auth_required && !session.authenticated" class="card login">
      <h2>登录</h2>
      <form @submit.prevent="login">
        <div class="field">
          <label>管理密码</label>
          <input type="password" v-model="password" autofocus autocomplete="current-password" />
        </div>
        <div class="error-box" v-if="loginError" style="margin-top:10px">{{ loginError }}</div>
        <button class="btn primary" style="margin-top:14px;width:100%" :disabled="busy || !password">进入</button>
      </form>
    </div>

    <template v-else>
      <ProbeView v-show="tab === 'probe'" @open-history="openHistory" />
      <AgentsView v-if="tab === 'agents'" :agent-image="session.agent_image" />
      <HistoryView v-if="tab === 'history'" :initial-id="historyTaskId" />
    </template>
  </div>
</template>
