<script setup>
import { ref, computed, watch, onMounted, onBeforeUnmount, defineAsyncComponent } from 'vue'
import { api } from './api.js'
import { route, navigate, taskPath } from './route.js'
import { theme, cycleTheme, applyTheme } from './theme.js'
import ProbeView from './views/Probe.vue'
import AgentsView from './views/Agents.vue'
import HistoryView from './views/History.vue'
import MonitorsView from './views/Monitors.vue'
import NotifyView from './views/Notify.vue'
import SettingsView from './views/Settings.vue'
import CoverageView from './views/Coverage.vue'
import SharePage from './views/SharePage.vue'

// The tool pages are loaded when first opened, so they do not weigh on the
// probe page.
const IpLookupView = defineAsyncComponent(() => import('./views/IpLookup.vue'))
const DnsServersView = defineAsyncComponent(() => import('./views/DnsServers.vue'))
const DnsGlobalView = defineAsyncComponent(() => import('./views/DnsGlobal.vue'))

// Pages in nav order. The current one lives in the URL path (see route.js),
// so a reload, bookmark or back button lands on the same page. `menu` pages
// sit behind the 管理 menu instead of taking a slot in the bar.
const TABS = {
  probe: { label: '拨测' },
  ip: { label: 'IP 定位' },
  coverage: { label: '节点覆盖' },
  'dns-servers': { label: 'DNS 目录' },
  'dns-global': { label: '全球 DNS' },
  monitors: { label: '监控', admin: true },
  agents: { label: '节点' },
  history: { label: '历史' },
  notify: { label: '通知', admin: true, menu: true },
  settings: { label: '设置', admin: true, menu: true },
}
const tab = computed(() => route.page)
// A shared result is public: it shows whatever the session is, without the
// navigation, and before the login wall when guest access is off.
const sharing = computed(() => route.page === 'share')
function go(name, replace = false) {
  menuOpen.value = false
  if (route.page === name && !route.taskId) return
  navigate(name === 'probe' ? '/' : '/' + name, { replace })
}
function goHome() {
  if (sharing.value) window.location.href = '/'
  else go('probe')
}
const menuOpen = ref(false)
function onDocClick(e) { if (!e.target.closest?.('.nav-menu')) menuOpen.value = false }
const session = ref(null)
const showLogin = ref(false)
const username = ref('admin')
const password = ref('')
const loginError = ref('')
const busy = ref(false)
const monitorId = ref('')

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
  if (!session.value || gated.value || sharing.value || !TABS[tab.value].admin || isAdmin.value) return
  if (session.value.auth_required && !session.value.authenticated) { showLogin.value = true; return }
  go('probe', true)
}
function closeLogin() {
  showLogin.value = false
  if (TABS[tab.value]?.admin && !isAdmin.value) go('probe', true)
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
  if (TABS[tab.value]?.admin) go('probe', true)
  await loadSession()
}
function onUnauthorized() {
  // An admin-only call failed: the session expired or a guest hit an admin page.
  if (session.value && session.value.authenticated) { session.value.authenticated = false; session.value.role = session.value.guest_enabled ? 'guest' : '' }
  showLogin.value = true
}
// A task opened from a list (history, a monitor's runs) is shown on the probe
// page under its own link.
function openTask(id) { navigate(taskPath(id)) }
async function onRelogin(msg) { await loadSession(); loginError.value = msg || ''; showLogin.value = true }

const THEME_LABEL = { auto: '跟随系统', dark: '深色', light: '浅色' }
function onDocKeydown(e) { if (e.key === 'Escape' && showLogin.value && !gated.value) closeLogin() }

onMounted(() => {
  applyTheme()
  if (route.query.login_error) {
    loginError.value = route.query.login_error
    showLogin.value = true
    navigate(location.pathname, { replace: true })
  } else if (route.page === 'monitors' && route.query.id) {
    monitorId.value = route.query.id
  }
  loadSession()
  window.addEventListener('probe:unauthorized', onUnauthorized)
  document.addEventListener('click', onDocClick)
  window.addEventListener('keydown', onDocKeydown)
})
onBeforeUnmount(() => {
  window.removeEventListener('probe:unauthorized', onUnauthorized)
  document.removeEventListener('click', onDocClick)
  window.removeEventListener('keydown', onDocKeydown)
})
// The probe page titles itself after the probe type.
watch(tab, (t) => { if (TABS[t] && t !== 'probe') document.title = `${TABS[t].label} · 拨测平台`; ensureAllowed() }, { immediate: true })
</script>

<template>
  <div class="app-shell">
    <div class="topbar-wrap">
      <header class="topbar">
        <button type="button" class="brand" @click="goHome">
          <span class="brand-icon" aria-hidden="true">
            <svg viewBox="0 0 36 28" fill="none">
              <path d="M9 7.5a11 11 0 0 0 0 13" stroke="currentColor" stroke-opacity=".35" stroke-width="2" stroke-linecap="round" />
              <path d="M5.5 4.5a15.5 15.5 0 0 0 0 19" stroke="currentColor" stroke-opacity=".18" stroke-width="2" stroke-linecap="round" />
              <path d="M27 7.5a11 11 0 0 1 0 13" stroke="currentColor" stroke-opacity=".35" stroke-width="2" stroke-linecap="round" />
              <path d="M30.5 4.5a15.5 15.5 0 0 1 0 19" stroke="currentColor" stroke-opacity=".18" stroke-width="2" stroke-linecap="round" />
              <path d="M18 4a7 7 0 0 0-7 7c0 5.1 7 12.2 7 12.2S25 16.1 25 11a7 7 0 0 0-7-7z" fill="currentColor" />
              <circle cx="18" cy="11" r="2.7" fill="#fff" />
            </svg>
          </span>
          <span>拨测平台</span>
        </button>
        <nav class="nav" v-if="session && !gated && !sharing">
          <template v-for="(def, name) in TABS" :key="name">
            <button v-if="!def.menu && (!def.admin || isAdmin)" :class="{ active: tab === name }" :aria-current="tab === name ? 'page' : undefined" @click="go(name)">{{ def.label }}</button>
          </template>
          <div class="nav-menu" v-if="isAdmin">
            <button :class="{ active: TABS[tab]?.menu }" aria-haspopup="menu" :aria-expanded="menuOpen" @click="menuOpen = !menuOpen">{{ TABS[tab]?.menu ? TABS[tab].label : '管理' }} ▾</button>
            <div class="nav-pop" v-if="menuOpen" role="menu">
              <template v-for="(def, name) in TABS" :key="name">
                <button v-if="def.menu" role="menuitem" :class="{ active: tab === name }" @click="go(name)">{{ def.label }}</button>
              </template>
            </div>
          </div>
        </nav>
        <div v-else></div>
        <div class="meta">
          <button type="button" class="pill-btn theme-btn" :title="`外观主题：${THEME_LABEL[theme]}（点击切换）`" :aria-label="`切换主题，当前：${THEME_LABEL[theme]}`" @click="cycleTheme">
            <svg v-if="theme === 'dark'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" /></svg>
            <svg v-else-if="theme === 'light'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="4" /><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41" /></svg>
            <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="2" y="3" width="20" height="14" rx="2" /><path d="M8 21h8M12 17v4" /></svg>
            <span>{{ THEME_LABEL[theme] }}</span>
          </button>
          <a class="pill-btn" v-if="sharing" href="/">发起新的探测</a>
          <template v-else-if="session && session.auth_required">
            <template v-if="isAdmin">
              <span class="badge ok" :title="session.user?.via === 'logto' ? '通过 Logto 登录' : '密码登录'">{{ session.user?.name || '管理员' }}</span>
              <button class="pill-btn" @click="logout">退出</button>
            </template>
            <template v-else>
              <span class="badge">游客</span>
              <button class="pill-btn" @click="showLogin = true; loginError = ''">管理员登录</button>
            </template>
          </template>
        </div>
      </header>
    </div>

    <main class="app">
      <SharePage v-if="sharing" :token="route.token" />

      <div v-else-if="!session" class="empty">加载中…</div>

      <!-- Full-page login when guest access is disabled -->
      <div v-else-if="gated" class="card login">
        <div class="row" style="margin-bottom:14px">
          <h2 style="margin:0">管理员登录</h2>
        </div>
        <form @submit.prevent="login" v-if="session.login?.password">
          <div class="field"><label>用户名</label><input type="text" v-model="username" autocomplete="username" /></div>
          <div class="field" style="margin-top:10px"><label>密码</label><input type="password" v-model="password" autofocus autocomplete="current-password" /></div>
          <div class="error-box" v-if="loginError" style="margin-top:12px">{{ loginError }}</div>
          <button class="btn primary" style="margin-top:16px;width:100%" :disabled="busy || !password">登录</button>
        </form>
        <div class="error-box" v-else-if="loginError" style="margin-top:10px">{{ loginError }}</div>
        <div v-if="session.login?.logto" style="margin-top:12px">
          <div class="sub" style="text-align:center;margin:8px 0" v-if="session.login?.password">或</div>
          <a class="btn" style="display:flex;width:100%" href="/api/auth/logto/login">通过 Logto 登录</a>
        </div>
      </div>

      <template v-else>
        <ProbeView v-show="tab === 'probe'" :role="session.role" />
        <IpLookupView v-if="tab === 'ip'" />
        <CoverageView v-if="tab === 'coverage'" />
        <DnsServersView v-if="tab === 'dns-servers'" />
        <DnsGlobalView v-if="tab === 'dns-global'" :admin="isAdmin" />
        <MonitorsView v-if="tab === 'monitors' && isAdmin" :initial-id="monitorId" @open-history="openTask" />
        <NotifyView v-if="tab === 'notify' && isAdmin" />
        <SettingsView v-if="tab === 'settings' && isAdmin" @relogin="onRelogin" />
        <AgentsView v-if="tab === 'agents'" :agent-image="session.agent_image" :server-version="session.version" :admin="isAdmin" />
        <HistoryView v-if="tab === 'history'" :admin="isAdmin" />

        <!-- Modal login when guest access is enabled -->
        <div v-if="showLogin" class="backdrop" @click.self="closeLogin">
          <div class="card login modal" role="dialog" aria-modal="true" aria-label="管理员登录">
            <div class="row" style="margin-bottom:14px">
              <h2 style="margin:0">管理员登录</h2>
              <span class="spacer"></span>
              <button class="btn sm" type="button" @click="closeLogin">关闭</button>
            </div>
            <form @submit.prevent="login" v-if="session.login?.password">
              <div class="field"><label>用户名</label><input type="text" v-model="username" autocomplete="username" /></div>
              <div class="field" style="margin-top:10px"><label>密码</label><input type="password" v-model="password" autofocus autocomplete="current-password" /></div>
              <div class="error-box" v-if="loginError" style="margin-top:12px">{{ loginError }}</div>
              <button class="btn primary" style="margin-top:16px;width:100%" :disabled="busy || !password">登录</button>
            </form>
            <div class="error-box" v-else-if="loginError" style="margin-top:10px">{{ loginError }}</div>
            <div v-if="session.login?.logto" style="margin-top:12px">
              <div class="sub" style="text-align:center;margin:8px 0" v-if="session.login?.password">或</div>
              <a class="btn" style="display:flex;width:100%" href="/api/auth/logto/login">通过 Logto 登录</a>
            </div>
            <p class="sub" style="margin:14px 0 0">游客可以直接使用拨测功能；登录后可管理监控、通知渠道和节点。</p>
          </div>
        </div>
      </template>
    </main>

    <footer class="app-footer" v-if="!sharing && session?.version">
      <span>拨测平台</span>
      <span class="ver-pill">v{{ session.version }}</span>
    </footer>
  </div>
</template>
