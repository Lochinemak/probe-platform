<script setup>
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { api } from './api.js'
import ProbeView from './views/Probe.vue'
import AgentsView from './views/Agents.vue'
import HistoryView from './views/History.vue'
import MonitorsView from './views/Monitors.vue'
import NotifyView from './views/Notify.vue'
import SettingsView from './views/Settings.vue'

// Tabs in display order. The active tab lives in the URL path (/history,
// /settings, …) so a reload, bookmark or back button lands on the same page.
// "/" and unknown paths mean the default tab.
const TABS = {
  probe: { label: '拨测' },
  monitors: { label: '监控', admin: true },
  agents: { label: '节点' },
  history: { label: '历史' },
  notify: { label: '通知', admin: true },
  settings: { label: '设置', admin: true },
}
function tabFromURL() {
  const name = location.pathname.replace(/^\/+|\/+$/g, '')
  return name in TABS ? name : 'probe'
}
const tab = ref(tabFromURL())
function go(name, replace = false) {
  tab.value = name
  if (tabFromURL() === name) return
  window.history[replace ? 'replaceState' : 'pushState'](null, '', '/' + name)
}
function onPopState() { tab.value = tabFromURL(); ensureAllowed() }
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
  ensureAllowed()
}
// An admin-only tab reached by URL (reload, bookmark, back button) without an
// admin session: ask for a login and keep the tab so a successful login lands
// there; anything else falls back to the default tab.
function ensureAllowed() {
  if (!session.value || gated.value || !TABS[tab.value].admin || isAdmin.value) return
  if (session.value.auth_required && !session.value.authenticated) { showLogin.value = true; return }
  go('probe', true)
}
function closeLogin() {
  showLogin.value = false
  if (TABS[tab.value].admin && !isAdmin.value) go('probe', true)
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
  if (TABS[tab.value].admin) go('probe', true)
  await loadSession()
}
function onUnauthorized() {
  // An admin-only call failed: the session expired or a guest hit an admin page.
  if (session.value && session.value.authenticated) { session.value.authenticated = false; session.value.role = session.value.guest_enabled ? 'guest' : '' }
  showLogin.value = true
}
function openHistory(id) { historyTaskId.value = id; go('history') }
async function onRelogin(msg) { await loadSession(); loginError.value = msg || ''; showLogin.value = true }

onMounted(() => {
  const params = new URLSearchParams(location.search)
  if (params.get('login_error')) {
    loginError.value = params.get('login_error')
    showLogin.value = true
    history.replaceState(null, '', location.pathname)
  }
  loadSession()
  window.addEventListener('probe:unauthorized', onUnauthorized)
  window.addEventListener('popstate', onPopState)
})
onBeforeUnmount(() => {
  window.removeEventListener('probe:unauthorized', onUnauthorized)
  window.removeEventListener('popstate', onPopState)
})
watch(tab, (t) => { document.title = `${TABS[t].label} · 拨测平台` }, { immediate: true })
</script>

<template>
  <div class="app">
    <header class="topbar">
      <div class="brand"><span class="dot"></span> 拨测平台</div>
      <nav class="nav" v-if="session && !gated">
        <template v-for="(def, name) in TABS" :key="name">
          <button v-if="!def.admin || isAdmin" :class="{ active: tab === name }" @click="go(name)">{{ def.label }}</button>
        </template>
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
        <button v-if="!gated" class="btn sm" type="button" @click="closeLogin">关闭</button>
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
        <HistoryView v-if="tab === 'history'" :initial-id="historyTaskId" :admin="isAdmin" />
      </div>
    </template>
  </div>
</template>
