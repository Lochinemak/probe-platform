<script setup>
import { ref, reactive, onMounted } from 'vue'
import { api } from '../api.js'

const emit = defineEmits(['relogin'])
const view = ref(null)
const error = ref('')
const notice = ref('')
const form = reactive({ guest_access: true, admin_user: 'admin', base_url: '', agent_image: '', logto_endpoint: '', logto_app_id: '', logto_app_secret: '', logto_admins: '' })
const pw = reactive({ current: '', next: '', confirm: '' })
const busy = ref('')

async function load() {
  try {
    view.value = await api.settings()
    Object.assign(form, {
      guest_access: view.value.guest_access, admin_user: view.value.admin_user, base_url: view.value.base_url, agent_image: view.value.agent_image,
      logto_endpoint: view.value.logto_endpoint, logto_app_id: view.value.logto_app_id, logto_app_secret: '', logto_admins: view.value.logto_admins,
    })
  } catch (e) { if (e.status !== 401) error.value = e.message }
}
async function save(section) {
  error.value = ''; notice.value = ''; busy.value = section
  const patch = {}
  if (section === 'access') { patch.guest_access = form.guest_access; patch.admin_user = form.admin_user }
  if (section === 'general') { patch.base_url = form.base_url; patch.agent_image = form.agent_image }
  if (section === 'logto') {
    patch.logto_endpoint = form.logto_endpoint; patch.logto_app_id = form.logto_app_id; patch.logto_admins = form.logto_admins
    if (form.logto_app_secret) patch.logto_app_secret = form.logto_app_secret
    if (!form.base_url && !view.value.base_url) { error.value = 'Logto 登录需要先填写「站点地址」（用于回调地址）'; busy.value = ''; return }
  }
  try {
    view.value = await api.updateSettings(patch)
    form.logto_app_secret = ''
    notice.value = '已保存，立即生效'
  } catch (e) { error.value = e.message } finally { busy.value = '' }
}
async function clearSecret() {
  if (!confirm('清除 Logto 应用密钥？')) return
  try { view.value = await api.updateSettings({ logto_app_secret: '' }); notice.value = '已清除密钥' } catch (e) { error.value = e.message }
}
async function testLogto() {
  error.value = ''; notice.value = ''; busy.value = 'test'
  try { const r = await api.testLogto(); notice.value = `Logto 连接正常：issuer ${r.issuer}` } catch (e) { error.value = `Logto 连接失败：${e.message}` } finally { busy.value = '' }
}
async function changePassword() {
  error.value = ''; notice.value = ''
  if (pw.next !== pw.confirm) { error.value = '两次输入的新密码不一致'; return }
  busy.value = 'password'
  try {
    await api.changePassword(pw.current, pw.next)
    pw.current = pw.next = pw.confirm = ''
    emit('relogin', '密码已修改，请重新登录')
  } catch (e) { error.value = e.message } finally { busy.value = '' }
}
async function copy(text) { try { await navigator.clipboard.writeText(text); notice.value = '已复制' } catch { /* ignore */ } }
onMounted(load)
</script>

<template>
  <div v-if="view">
    <div class="error-box" v-if="error">{{ error }}</div>
    <div class="card" v-if="notice" style="border-color:var(--ok)"><span class="ok">{{ notice }}</span></div>
    <p class="sub" v-if="!view.login_enabled" style="margin:0 0 12px"><span class="badge warn">开放模式</span> 目前没有任何登录方式，所有访客都是管理员。请设置密码或配置 Logto。</p>

    <div class="card">
      <h3>访问控制</h3>
      <form @submit.prevent="save('access')" class="row" style="gap:16px">
        <label class="field inline" style="margin-top:18px"><input type="checkbox" v-model="form.guest_access" /> 允许游客使用拨测（节点详情隐藏、频率受限）</label>
        <div class="field"><label>管理员用户名</label><input type="text" v-model="form.admin_user" /></div>
        <button class="btn primary" style="margin-top:18px" :disabled="busy === 'access'">保存</button>
      </form>
      <p class="sub">关闭游客访问后，所有功能都需要登录。</p>
    </div>

    <div class="card">
      <h3>管理员密码 <span class="sub" v-if="view.password_set">已设置（来源：{{ view.password_source === 'env' ? '环境变量，修改后以此处为准' : '本页面' }}）</span><span class="sub" v-else>未设置</span></h3>
      <form @submit.prevent="changePassword" class="row" style="gap:16px">
        <div class="field" v-if="view.password_set"><label>当前密码</label><input type="password" v-model="pw.current" autocomplete="current-password" /></div>
        <div class="field"><label>新密码（≥ 8 位）</label><input type="password" v-model="pw.next" autocomplete="new-password" /></div>
        <div class="field"><label>确认新密码</label><input type="password" v-model="pw.confirm" autocomplete="new-password" /></div>
        <button class="btn primary" style="margin-top:18px" :disabled="busy === 'password' || !pw.next">修改密码</button>
      </form>
      <p class="sub">修改后所有已登录会话（包括当前）都会失效，需要重新登录。</p>
    </div>

    <div class="card">
      <h3>Logto 登录 <span class="badge ok" v-if="view.logto_enabled">已启用</span><span class="badge" v-else>未启用</span></h3>
      <form @submit.prevent="save('logto')">
        <div class="row" style="gap:16px">
          <div class="field grow"><label>Logto 地址</label><input type="text" v-model="form.logto_endpoint" placeholder="https://auth.example.com" spellcheck="false" /></div>
          <div class="field"><label>App ID</label><input type="text" v-model="form.logto_app_id" spellcheck="false" /></div>
          <div class="field grow"><label>App Secret <span class="sub" v-if="view.logto_secret_set">已设置，留空则保持不变</span></label>
            <div class="row" style="gap:6px"><input type="password" v-model="form.logto_app_secret" autocomplete="new-password" style="flex:1" :placeholder="view.logto_secret_set ? '••••••••' : 'Traditional Web 应用必填'" />
              <button type="button" class="btn sm" v-if="view.logto_secret_set" @click="clearSecret">清除</button></div>
          </div>
        </div>
        <div class="row" style="gap:16px;margin-top:10px">
          <div class="field grow"><label>允许的管理员（可选，逗号分隔的邮箱 / 用户名 / sub；留空 = 该 Logto 的任何用户）</label><input type="text" v-model="form.logto_admins" spellcheck="false" /></div>
        </div>
        <div class="row" style="margin-top:12px">
          <button class="btn primary" :disabled="busy === 'logto'">保存</button>
          <button class="btn" type="button" @click="testLogto" :disabled="busy === 'test' || !view.logto_enabled">{{ busy === 'test' ? '测试中…' : '测试连接' }}</button>
        </div>
      </form>
      <div class="detail" style="margin-top:12px">
        <div class="kv">
          <div>应用类型</div><div>Traditional Web（需要 App Secret）或 Single Page App（只用 PKCE，密钥留空）</div>
          <div>Redirect URI</div><div class="mono">{{ view.logto_redirect_url || '（先在下方填写站点地址）' }} <button v-if="view.logto_redirect_url" type="button" class="btn link sm" @click="copy(view.logto_redirect_url)">复制</button></div>
          <div>Scopes</div><div class="mono">openid profile email</div>
        </div>
        <p class="sub" style="margin:8px 0 0">把 Redirect URI 登记到 Logto 应用的「Redirect URIs」，保存后右上角登录框会出现「通过 Logto 登录」。</p>
      </div>
    </div>

    <div class="card">
      <h3>常规</h3>
      <form @submit.prevent="save('general')" class="row" style="gap:16px">
        <div class="field grow"><label>站点地址（告警链接、Logto 回调）</label><input type="text" v-model="form.base_url" placeholder="https://probe.example.com" spellcheck="false" /></div>
        <div class="field grow"><label>节点页展示的 agent 镜像</label><input type="text" v-model="form.agent_image" spellcheck="false" /></div>
        <button class="btn primary" style="margin-top:18px" :disabled="busy === 'general'">保存</button>
      </form>
    </div>
  </div>
  <div v-else class="empty">加载中…</div>
</template>
