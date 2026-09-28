<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { api } from './api.js'
import ProbeView from './views/Probe.vue'
import AgentsView from './views/Agents.vue'
import HistoryView from './views/History.vue'
import MonitorsView from './views/Monitors.vue'
import NotifyView from './views/Notify.vue'
import SettingsView from './views/Settings.vue'

const tab = ref('probe')
const session = ref(null)
const showLogin = ref(false)
const username = ref('admin')
const password = ref('')
const loginError = ref('')
const busy = ref(false)
const historyTaskId = ref('')

const isAdmin = computed(() => session.value?.role === 'admin')
// With guest access off and no session the whole app sits behind the login.
const gated = computed(() => session.value && session.value.auth_required && !session.value.authenticated && !session.value.guest_enabled)

async function loadSession() {
  try { session.value = await api.session() } catch (e) { session.value = { auth_required: false, authenticated: true, role: 'admin', guest_enabled: true, login: {}, error: e.message } }
}
async function login() {
  busy.value = true
  loginError.value = ''
  try {
    await api.login(username.value, password.value)
    password.value = ''
    showLogin.value = false
    await loadSession()
  } catch (e) { loginError.value = e.message } finally { busy.value = false }
}
async function logout() {
  await api.logout().catch(() => {})
  await loadSession()
  if (!['probe', 'history', 'agents'].includes(tab.value)) tab.value = 'probe'
}
function onUnauthorized() {
  // An admin-only call failed: the session expired or a guest hit an admin page.
  if (session.value && session.value.authenticated) { session.value.authenticated = false; session.value.role = session.value.guest_enabled ? 'guest' : '' }
  showLogin.value = true
}
function openHistory(id) { historyTaskId.value = id; tab.value = 'history' }
async function onRelogin(msg) { await loadSession(); loginError.value = msg || ''; showLogin.value = true; tab.value = 'probe' }

onMounted(() => {
  const params = new URLSearchParams(location.search)
  if (params.get('login_error')) {
    loginError.value = params.get('login_error')
    showLogin.value = true
    history.replaceState(null, '', location.pathname)
  }
  loadSession()
  window.addEventListener('probe:unauthorized', onUnauthorized)
})
onBeforeUnmount(() => window.removeEventListener('probe:unauthorized', onUnauthorized))
</script>

<template>
  <div class="app">
    <header class="topbar">
      <div class="brand"><span class="dot"></span> 拨测平台</div>
      <nav class="nav" v-if="session && !gated">
        <button :class="{ active: tab === 'probe' }" @click="tab = 'probe'">拨测</button>
        <button v-if="isAdmin" :class="{ active: tab === 'monitors' }" @click="tab = 'monitors'">监控</button>
        <button :class="{ active: tab === 'agents' }" @click="tab = 'agents'">节点</button>
        <button :class="{ active: tab === 'history' }" @click="tab = 'history'">历史</button>
        <button v-if="isAdmin" :class="{ active: tab === 'notify' }" @click="tab = 'notify'">通知</button>
        <button v-if="isAdmin" :class="{ active: tab === 'settings' }" @click="tab = 'settings'">设置</button>
      </nav>
      <div class="spacer"></div>
      <div class="meta" v-if="session">
        <span v-if="session.version">v{{ session.version }}</span>
        <template v-if="session.auth_required">
          <template v-if="isAdmin">
            <span class="badge ok" :title="session.user?.via === 'logto' ? '通过 Logto 登录' : '密码登录'">{{ session.user?.name || '管理员' }}</span>
            <button class="btn sm" @click="logout">退出</button>
          </template>
          <template v-else>
            <span class="badge">游客</span>
            <button class="btn sm" @click="showLogin = true; loginError = ''">管理员登录</button>
          </template>
        </template>
      </div>
    </header>

    <div v-if="!session" class="empty">加载中…</div>

    <!-- Login: full page when guests are off, modal otherwise -->
    <div v-else-if="gated || showLogin" class="card login" :class="{ modal: !gated }">
      <div class="row" style="margin-bottom:12px">
        <h2 style="margin:0">管理员登录</h2>
        <span class="spacer"></span>
        <button v-if="!gated" class="btn sm" type="button" @click="showLogin = false">关闭</button>
      </div>
      <form @submit.prevent="login" v-if="session.login?.password">
        <div class="field"><label>用户名</label><input type="text" v-model="username" autocomplete="username" /></div>
        <div class="field" style="margin-top:8px"><label>密码</label><input type="password" v-model="password" autofocus autocomplete="current-password" /></div>
        <div class="error-box" v-if="loginError" style="margin-top:10px">{{ loginError }}</div>
        <button class="btn primary" style="margin-top:14px;width:100%" :disabled="busy || !password">登录</button>
      </form>
      <div class="error-box" v-else-if="loginError" style="margin-top:10px">{{ loginError }}</div>
      <div v-if="session.login?.logto" style="margin-top:12px">
        <div class="sub" style="text-align:center;margin:8px 0" v-if="session.login?.password">或</div>
        <a class="btn" style="display:block;text-align:center" href="/api/auth/logto/login">通过 Logto 登录</a>
      </div>
      <p class="sub" v-if="!gated" style="margin:12px 0 0">游客可以直接使用拨测功能；登录后可管理监控、通知渠道和节点。</p>
    </div>

    <template v-if="session && !gated">
      <div v-show="!showLogin">
        <ProbeView v-show="tab === 'probe'" :role="session.role" @open-history="openHistory" />
        <MonitorsView v-if="tab === 'monitors' && isAdmin" @open-history="openHistory" />
        <NotifyView v-if="tab === 'notify' && isAdmin" />
        <SettingsView v-if="tab === 'settings' && isAdmin" @relogin="onRelogin" />
        <AgentsView v-if="tab === 'agents'" :agent-image="session.agent_image" :server-version="session.version" :admin="isAdmin" />
        <HistoryView v-if="tab === 'history'" :initial-id="historyTaskId" />
      </div>
    </template>
  </div>
</template>
