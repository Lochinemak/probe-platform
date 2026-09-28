<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { api } from '../api.js'
import { timeAgo, fmtTime } from '../fmt.js'
import Onboarding from '../components/Onboarding.vue'

const props = defineProps({
  agentImage: { type: String, default: 'ghcr.io/lochinemak/probe-agent:latest' },
  serverVersion: { type: String, default: '' },
  admin: { type: Boolean, default: true },
})
const agents = ref([])
const error = ref('')
const downloads = ref([])
function outdated(a) {
  return a.version && props.serverVersion && a.version !== 'dev' && props.serverVersion !== 'dev' && a.version !== props.serverVersion
}
async function loadDownloads() {
  try { downloads.value = (await api.agentFiles()).files || [] } catch { downloads.value = [] }
}
let timer = null
async function load() {
  try { agents.value = (await api.agents()).agents } catch (e) { if (e.status !== 401) error.value = e.message }
}
async function remove(a) {
  if (!confirm(`删除离线节点 ${a.name} 的记录？（节点重新连接会自动恢复）`)) return
  try { await api.deleteAgent(a.id); await load() } catch (e) { error.value = e.message }
}
const online = computed(() => agents.value.filter((a) => a.online).length)
onMounted(() => { load(); if (props.admin) loadDownloads(); timer = setInterval(load, 5000) })
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div>
    <div class="error-box" v-if="error">{{ error }}</div>
    <div class="card">
      <h3>节点列表 <span class="sub">{{ online }} 在线 / {{ agents.length }} 总数</span></h3>
      <div class="table-wrap" v-if="!props.admin">
        <table class="grid">
          <thead><tr><th></th><th>名称</th><th>位置</th><th>运营商</th><th>能力</th><th class="num">运行中</th></tr></thead>
          <tbody>
            <tr v-for="a in agents" :key="a.id">
              <td><span class="status-dot" :class="{ online: a.online }"></span></td>
              <td><b>{{ a.name }}</b></td>
              <td>{{ a.location || '-' }}</td>
              <td>{{ a.isp || '-' }}</td>
              <td><span v-for="c in a.capabilities" :key="c" class="badge" style="margin-right:4px">{{ c }}</span></td>
              <td class="num">{{ a.running || 0 }}</td>
            </tr>
            <tr v-if="!agents.length"><td colspan="6" class="empty">暂无节点</td></tr>
          </tbody>
        </table>
        <p class="sub">游客视图只显示节点的位置与运营商；管理员登录后可查看公网 IP、版本等信息并接入新节点。</p>
      </div>
      <div class="table-wrap" v-else>
        <table class="grid">
          <thead>
            <tr>
              <th></th><th>名称</th><th>位置</th><th>运营商</th><th>公网 IP</th><th>系统</th><th>版本</th><th>能力</th><th class="num">运行中</th><th>最后在线</th><th></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="a in agents" :key="a.id">
              <td><span class="status-dot" :class="{ online: a.online }"></span></td>
              <td><b>{{ a.name }}</b><div class="sub" v-if="a.tags?.length">{{ a.tags.join(', ') }}</div></td>
              <td>{{ a.location || a.geo_location || '-' }}<div class="sub" v-if="a.location && a.geo_location && a.location !== a.geo_location">{{ a.geo_location }}</div></td>
              <td>{{ a.isp || a.geo_isp || '-' }}<div class="sub" v-if="a.isp && a.geo_isp && a.isp !== a.geo_isp">{{ a.geo_isp }}</div></td>
              <td class="mono">{{ a.public_ip || '-' }}</td>
              <td class="sub">{{ a.os }}/{{ a.arch }}</td>
              <td class="sub">{{ a.version }} <span v-if="outdated(a)" class="badge warn" title="与服务端版本不一致，节点重连后会自动更新">待更新</span></td>
              <td>
                <span v-for="c in a.capabilities" :key="c" class="badge" :class="{ ok: c === 'icmp_raw' || c === 'mtr' }" style="margin-right:4px">{{ c }}</span>
                <span v-if="a.capabilities && !a.capabilities.includes('mtr')" class="badge warn" title="没有 raw socket 权限，MTR 不可用">无 MTR</span>
              </td>
              <td class="num">{{ a.running || 0 }}</td>
              <td class="sub" :title="fmtTime(a.last_seen)">{{ a.online ? '在线' : timeAgo(a.last_seen) }}</td>
              <td><button v-if="!a.online" class="btn sm danger" @click="remove(a)">删除</button></td>
            </tr>
            <tr v-if="!agents.length"><td colspan="11" class="empty">暂无节点</td></tr>
          </tbody>
        </table>
      </div>
    </div>

    <Onboarding v-if="props.admin" :agent-image="props.agentImage" :server-version="props.serverVersion" :downloads="downloads" />
  </div>
</template>
