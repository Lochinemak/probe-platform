<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { api } from '../api.js'
import { typeLabel, fmtTime, timeAgo, ms, pct } from '../fmt.js'
import { seriesColor } from '../palette.js'
import TimeSeriesChart from './TimeSeriesChart.vue'

const props = defineProps({ id: String })
const emit = defineEmits(['close', 'edit', 'open-history'])
const monitor = ref(null)
const series = ref([])
const bucketSec = ref(0)
const events = ref([])
const runs = ref([])
const range = ref('6h')
const hidden = ref({})
const error = ref('')
let timer = null

const ranges = [['1h', '1 小时'], ['6h', '6 小时'], ['24h', '24 小时'], ['7d', '7 天'], ['30d', '30 天']]

async function load() {
  try {
    const [m, s, ev, r] = await Promise.all([
      api.monitor(props.id), api.monitorSeries(props.id, range.value), api.monitorAlerts(props.id, 50), api.tasks(20, 0, props.id),
    ])
    monitor.value = m
    series.value = s.series.sort((a, b) => a.agent_id.localeCompare(b.agent_id))
    bucketSec.value = s.bucket_sec
    events.value = ev.events
    runs.value = r.tasks
  } catch (e) { if (e.status !== 401) error.value = e.message }
}
const latencySeries = computed(() => series.value.map((s) => ({ id: s.agent_id, label: s.agent_name, points: s.points.map((p) => [p.t, p.latency_ms]) })))
const lossSeries = computed(() => series.value.map((s) => ({ id: s.agent_id, label: s.agent_name, points: s.points.map((p) => [p.t, p.loss_pct]) })))
function toggle(id) { hidden.value = { ...hidden.value, [id]: !hidden.value[id] } }
function setRange(r) { range.value = r; load() }
onMounted(() => { load(); timer = setInterval(load, 30000) })
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <div>
    <div class="error-box" v-if="error">{{ error }}</div>
    <div class="card" v-if="monitor">
      <div class="row" style="margin-bottom:6px">
        <button class="btn sm" @click="emit('close')">← 返回</button>
        <h3 style="margin:0">{{ monitor.name }}</h3>
        <span class="badge" :class="monitor.type">{{ typeLabel[monitor.type] }}</span>
        <span class="mono sub">{{ monitor.target }}</span>
        <span class="badge" :class="monitor.enabled ? 'ok' : ''">{{ monitor.enabled ? '运行中' : '已停用' }}</span>
        <span class="badge error" v-if="monitor.alerting">{{ monitor.alerting }} 个节点告警中</span>
        <span class="spacer"></span>
        <button class="btn sm" @click="emit('edit', monitor)">编辑</button>
      </div>
      <div class="row" style="margin-bottom:10px">
        <div class="tabs">
          <button v-for="[r, l] in ranges" :key="r" type="button" :class="{ active: range === r }" @click="setRange(r)">{{ l }}</button>
        </div>
        <span class="sub" v-if="bucketSec">每 {{ bucketSec >= 3600 ? bucketSec / 3600 + ' 小时' : bucketSec / 60 + ' 分钟' }}聚合一个点</span>
        <span class="spacer"></span>
        <span class="legend" style="margin:0">
          <span v-for="(s, i) in series" :key="s.agent_id" class="chip" :class="{ on: !hidden[s.agent_id] }" style="padding:2px 8px" @click="toggle(s.agent_id)">
            <i :style="{ background: seriesColor(i), width: '10px', height: '10px', borderRadius: '2px', display: 'inline-block' }"></i> {{ s.agent_name }}
          </span>
        </span>
      </div>
      <template v-if="series.length">
        <div class="sub" style="margin:4px 0">延迟</div>
        <TimeSeriesChart :series="latencySeries" unit="ms" :hidden="hidden" :height="220" />
        <div class="sub" style="margin:10px 0 4px">丢包 / 失败率</div>
        <TimeSeriesChart :series="lossSeries" unit="%" :hidden="hidden" :height="150" />
      </template>
      <div v-else class="empty">这个时间范围内还没有数据</div>
    </div>

    <div class="card" v-if="monitor">
      <h3>节点状态</h3>
      <div class="table-wrap">
        <table class="grid">
          <thead><tr><th>节点</th><th>状态</th><th class="num">最近延迟</th><th class="num">最近丢包</th><th>最近一次</th><th class="num">连续失败</th><th>告警</th><th>原因</th></tr></thead>
          <tbody>
            <tr v-for="st in monitor.agents" :key="st.agent_id">
              <td><b>{{ st.agent_name }}</b></td>
              <td><span v-if="!st.last" class="sub">无数据</span><span v-else class="badge" :class="st.last.ok ? 'ok' : 'bad'">{{ st.last.ok ? '正常' : '失败' }}</span></td>
              <td class="num">{{ st.last && st.last.latency_ms >= 0 ? ms(st.last.latency_ms) : '-' }}</td>
              <td class="num">{{ st.last ? pct(st.last.loss_pct) : '-' }}</td>
              <td class="sub" :title="st.last ? fmtTime(st.last.at) : ''">{{ st.last ? timeAgo(st.last.at) : '-' }}</td>
              <td class="num">{{ st.failing }}</td>
              <td><span v-if="st.alerting" class="badge error">告警中 · {{ st.since ? timeAgo(st.since) : '' }}</span></td>
              <td class="sub" style="white-space:normal">{{ st.last?.error || st.last_error || '' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div class="row top" style="gap:16px">
      <div class="card grow">
        <h3>告警记录</h3>
        <table class="grid">
          <thead><tr><th>时间</th><th>节点</th><th></th><th>详情</th></tr></thead>
          <tbody>
            <tr v-for="ev in events" :key="ev.id">
              <td class="sub">{{ fmtTime(ev.at) }}</td>
              <td>{{ ev.agent }}</td>
              <td><span class="badge" :class="ev.kind === 'down' ? 'error' : 'ok'">{{ ev.kind === 'down' ? '异常' : '恢复' }}</span></td>
              <td class="sub" style="white-space:normal">{{ ev.message }}</td>
            </tr>
            <tr v-if="!events.length"><td colspan="4" class="empty">暂无告警</td></tr>
          </tbody>
        </table>
      </div>
      <div class="card grow">
        <h3>最近运行</h3>
        <table class="grid">
          <thead><tr><th>时间</th><th class="num">节点数</th><th></th></tr></thead>
          <tbody>
            <tr v-for="t in runs" :key="t.id" class="clickable" @click="emit('open-history', t.id)">
              <td class="sub">{{ fmtTime(t.created_at) }}</td>
              <td class="num">{{ t.agent_ids?.length || 0 }}</td>
              <td><span class="btn link sm">查看详情</span></td>
            </tr>
            <tr v-if="!runs.length"><td colspan="3" class="empty">暂无运行记录</td></tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>
