<script setup>
// 保存截图: renders the result view to a PNG. The picture follows what is on
// screen (current filter, order, map state); the switches here only hide
// parts of it for the duration of the capture, by putting marker classes on
// the result element (see the `.shot` rules in style.css).
import { ref, reactive, watch, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { exportFilename, exportTitle } from '../export.js'

const props = defineProps({
  target: { type: Object, default: null }, // the element to capture
  task: Object,
  running: Boolean,
  finished: Number, // nodes that have reported
  total: Number,
  shared: Boolean, // a shared result: what is hidden there cannot be shown here
})
const emit = defineEmits(['close'])

const opt = reactive({ scope: 'full', target: true, time: true, map: true, ips: !props.shared, names: true, stamp: true, scale: 2, wait: true })
const state = ref('idle') // idle | waiting | working | ready | error
const message = ref('')
const blob = ref(null)
const preview = ref('')
let cancelled = false

function classes() {
  return ['shot', opt.scope === 'simple' && 'shot-simple', !opt.target && 'shot-no-target', !opt.time && 'shot-no-time', !opt.map && 'shot-no-map',
    !opt.ips && 'shot-no-ips', !opt.names && 'shot-no-names', !opt.stamp && 'shot-no-stamp'].filter(Boolean)
}
function reset() {
  if (preview.value) URL.revokeObjectURL(preview.value)
  blob.value = null
  preview.value = ''
  if (state.value === 'ready' || state.value === 'error') { state.value = 'idle'; message.value = '' }
}
watch(opt, reset)

async function generate() {
  reset()
  cancelled = false
  if (props.running && opt.wait) {
    state.value = 'waiting'
    await new Promise((resolve) => {
      const t = setInterval(() => { if (!props.running || cancelled) { clearInterval(t); resolve() } }, 200)
    })
    if (cancelled) { state.value = 'idle'; return }
  }
  state.value = 'working'
  const el = props.target
  const cls = classes()
  el.classList.add(...cls)
  try {
    await nextTick()
    await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))) // let the hidden parts leave the layout
    const { domToBlob } = await import('modern-screenshot')
    const bg = getComputedStyle(document.documentElement).getPropertyValue('--bg-2').trim() || '#fff'
    const b = await domToBlob(el, { scale: opt.scale, backgroundColor: bg, maximumCanvasSize: 16000, font: false, style: { padding: '20px' } })
    if (!b) throw new Error('浏览器未能生成截图图片')
    blob.value = b
    preview.value = URL.createObjectURL(b)
    state.value = 'ready'
    message.value = props.running ? '任务仍在运行，截图是当前进度。' : ''
  } catch (e) {
    state.value = 'error'
    message.value = '截图生成失败：' + (e?.message || e)
  } finally { el.classList.remove(...cls) }
}
function cancelWait() { cancelled = true }

function download() {
  const a = document.createElement('a')
  a.href = preview.value
  a.download = exportFilename(props.task, 'png')
  document.body.appendChild(a)
  a.click()
  a.remove()
  message.value = '已开始下载 PNG 图片。'
}
async function copyImage() {
  try {
    if (!window.ClipboardItem || !navigator.clipboard?.write) throw new Error('unsupported')
    await navigator.clipboard.write([new ClipboardItem({ 'image/png': blob.value })])
    message.value = '图片已复制，可直接粘贴到聊天或文档。'
  } catch { message.value = '当前浏览器不支持直接复制图片，请使用下载。' }
}
function onKey(e) { if (e.key === 'Escape') { e.preventDefault(); cancelled = true; emit('close') } }
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => { window.removeEventListener('keydown', onKey); cancelled = true; if (preview.value) URL.revokeObjectURL(preview.value) })
</script>

<template>
  <div class="backdrop" @click.self="emit('close')">
    <div class="card dialog" role="dialog" aria-modal="true" aria-label="保存结果截图">
      <div class="row" style="margin-bottom:10px">
        <h2 style="margin:0;font-size:16px">保存结果截图</h2>
        <span class="spacer"></span>
        <button class="btn sm" type="button" aria-label="关闭截图设置" @click="emit('close')">关闭</button>
      </div>
      <p class="sub" style="margin-top:0">{{ exportTitle(task) || '探测结果' }} · 截图沿用当前的筛选、排序和地图状态。</p>

      <div class="field">
        <label>截图范围</label>
        <div class="tabs sm" role="group" aria-label="截图范围">
          <button type="button" :class="{ active: opt.scope === 'simple' }" @click="opt.scope = 'simple'">简易</button>
          <button type="button" :class="{ active: opt.scope === 'full' }" @click="opt.scope = 'full'">完整</button>
        </div>
        <span class="sub">{{ opt.scope === 'simple' ? '指标、地图与响应 IP 汇总' : '同时包含节点明细' }}</span>
      </div>
      <fieldset class="share-flags">
        <legend>展示内容</legend>
        <label><input type="checkbox" v-model="opt.target" /> 探测目标</label>
        <label><input type="checkbox" v-model="opt.time" /> 检测时间</label>
        <label><input type="checkbox" v-model="opt.map" /> 地图</label>
        <label><input type="checkbox" v-model="opt.ips" /> 响应 IP <span class="sub">汇总与明细里的地址</span></label>
        <label><input type="checkbox" v-model="opt.names" /> 节点名称 <span class="sub">关闭后只保留地域与线路</span></label>
        <label><input type="checkbox" v-model="opt.stamp" /> 生成时间</label>
      </fieldset>
      <div class="row" style="margin-top:12px;gap:20px">
        <div class="field"><label>图片清晰度</label>
          <div class="tabs sm" role="group" aria-label="图片清晰度">
            <button type="button" :class="{ active: opt.scale === 2 }" @click="opt.scale = 2">标准 · 2×</button>
            <button type="button" :class="{ active: opt.scale === 3 }" @click="opt.scale = 3">高清 · 3×</button>
          </div>
        </div>
        <div class="field" v-if="running"><label>生成时机</label>
          <div class="tabs sm" role="group" aria-label="生成时机">
            <button type="button" :class="{ active: opt.wait }" @click="opt.wait = true">等待完成</button>
            <button type="button" :class="{ active: !opt.wait }" @click="opt.wait = false">立即生成</button>
          </div>
        </div>
      </div>
      <p class="sub" v-if="running">当前已返回 {{ finished }} / {{ total }} 个节点。</p>

      <div class="row" style="margin-top:14px">
        <button v-if="state === 'waiting'" class="btn" type="button" @click="cancelWait">取消等待</button>
        <button v-else class="btn primary" type="button" :disabled="state === 'working' || !target" @click="generate">
          {{ state === 'working' ? '正在生成…' : state === 'ready' ? '重新生成' : running && opt.wait ? '等待完成并生成' : '生成 PNG' }}
        </button>
        <span class="sub" v-if="state === 'waiting'">等待任务完成…</span>
        <template v-if="state === 'ready'">
          <button class="btn" type="button" @click="download">下载</button>
          <button class="btn" type="button" @click="copyImage">复制图片</button>
        </template>
      </div>
      <p class="sub" :class="{ bad: state === 'error' }" v-if="message" role="status" style="margin:10px 0 0">{{ message }}</p>
      <div class="shot-preview" v-if="preview"><img :src="preview" alt="截图预览" /></div>
    </div>
  </div>
</template>
