<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../api.js'

const channels = ref([])
const types = ref({})
const editing = ref(null)
const error = ref('')
const notice = ref('')
const testing = ref('')

const typeLabels = { telegram: 'Telegram 机器人', wecom: '企业微信群机器人', dingtalk: '钉钉群机器人', bark: 'Bark（iOS 推送）', webhook: '通用 Webhook', smtp: '邮件 (SMTP)' }
const fieldLabels = {
  bot_token: ['Bot Token', '@BotFather 给的 token'], chat_id: ['Chat ID', '用户或群的 chat_id'],
  webhook_url: ['Webhook 地址', '群机器人的完整 URL'], secret: ['加签密钥（可选）', '钉钉「加签」的 SEC 开头密钥'],
  url: ['地址', 'Bark 填 https://api.day.app/<你的key>；Webhook 填接收 JSON 的地址'], auth_header: ['认证头（可选）', '例如 Authorization: Bearer xxx'],
  host: ['SMTP 主机', 'smtp.qq.com'], port: ['端口', '465 或 587'], username: ['用户名', ''], password: ['密码 / 授权码', ''],
  from: ['发件人', 'probe@example.com'], to: ['收件人', '多个用逗号分隔'], tls: ['加密', 'ssl（465）或 starttls（587），留空自动'],
}

async function load() {
  try { const r = await api.channels(); channels.value = r.channels; types.value = r.types } catch (e) { if (e.status !== 401) error.value = e.message }
}
function startCreate() { editing.value = { name: '', type: 'telegram', config: {}, enabled: true }; error.value = ''; notice.value = '' }
function startEdit(c) { editing.value = JSON.parse(JSON.stringify(c)); error.value = ''; notice.value = '' }
async function save() {
  const e = editing.value
  try {
    if (e.id) await api.updateChannel(e.id, e)
    else await api.createChannel(e)
    editing.value = null
    await load()
  } catch (err) { error.value = err.message }
}
async function remove(c) {
  if (!confirm(`删除通知渠道「${c.name}」？使用它的监控会自动解除关联。`)) return
  try { await api.deleteChannel(c.id); await load() } catch (e) { error.value = e.message }
}
async function test(c) {
  testing.value = c.id; notice.value = ''; error.value = ''
  try { await api.testChannel(c.id); notice.value = `已向「${c.name}」发送测试消息` } catch (e) { error.value = `测试失败：${e.message}` } finally { testing.value = '' }
}
onMounted(load)
</script>

<template>
  <div>
    <div class="error-box" v-if="error">{{ error }}</div>
    <div class="card" v-if="notice" style="border-color:var(--ok)"><span class="ok">{{ notice }}</span></div>

    <div class="card" v-if="editing">
      <h3>{{ editing.id ? '编辑渠道' : '新建渠道' }}</h3>
      <form @submit.prevent="save">
        <div class="row" style="gap:16px">
          <div class="field"><label>名称</label><input type="text" v-model="editing.name" required /></div>
          <div class="field"><label>类型</label>
            <select v-model="editing.type"><option v-for="(fields, t) in types" :key="t" :value="t">{{ typeLabels[t] || t }}</option></select>
          </div>
          <label class="field inline" style="margin-top:18px"><input type="checkbox" v-model="editing.enabled" /> 启用</label>
        </div>
        <div class="row" style="gap:16px;margin-top:10px">
          <div class="field" v-for="f in types[editing.type] || []" :key="f" :class="{ grow: ['url', 'webhook_url'].includes(f) }">
            <label>{{ (fieldLabels[f] || [f])[0] }}</label>
            <input :type="['password', 'bot_token', 'secret'].includes(f) ? 'password' : 'text'" v-model="editing.config[f]" :placeholder="(fieldLabels[f] || ['', ''])[1]" spellcheck="false" autocomplete="off" />
          </div>
        </div>
        <div class="row" style="margin-top:12px">
          <button class="btn primary" type="submit">保存</button>
          <button class="btn" type="button" @click="editing = null">取消</button>
        </div>
      </form>
    </div>

    <div class="card">
      <div class="row" style="margin-bottom:10px">
        <h3 style="margin:0">通知渠道</h3>
        <span class="spacer"></span>
        <button class="btn primary sm" style="padding:6px 14px" @click="startCreate" v-if="!editing">新建渠道</button>
      </div>
      <table class="grid">
        <thead><tr><th></th><th>名称</th><th>类型</th><th>配置</th><th></th></tr></thead>
        <tbody>
          <tr v-for="c in channels" :key="c.id">
            <td><span class="status-dot" :class="{ online: c.enabled }"></span></td>
            <td><b>{{ c.name }}</b></td>
            <td>{{ typeLabels[c.type] || c.type }}</td>
            <td class="sub mono" style="white-space:normal">{{ Object.entries(c.config).filter(([k, v]) => v && !['bot_token', 'password', 'secret'].includes(k)).map(([k, v]) => k + '=' + v).join('  ') }}</td>
            <td>
              <button class="btn sm" @click="test(c)" :disabled="testing === c.id">{{ testing === c.id ? '发送中…' : '发送测试' }}</button>
              <button class="btn sm" @click="startEdit(c)">编辑</button>
              <button class="btn sm danger" @click="remove(c)">删除</button>
            </td>
          </tr>
          <tr v-if="!channels.length"><td colspan="5" class="empty">还没有通知渠道。支持 Telegram、企业微信、钉钉、Bark、通用 Webhook 和邮件。</td></tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
