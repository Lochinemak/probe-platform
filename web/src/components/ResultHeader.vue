<script setup>
// The title line of a result: what was probed and when, plus the actions that
// act on the whole result (copy its link, export it, clean view).
import { ref, onBeforeUnmount } from 'vue'
import { fmtTime, typeLabel } from '../fmt.js'
import { TYPE_INFO } from '../validate.js'
import { taskPath } from '../route.js'
import { toMarkdown, toCSV, toJSON, download, exportFilename } from '../export.js'

const props = defineProps({
  task: Object,
  rows: Array, // derived rows as shown: filtered and in table order
  results: Array, // raw results, for the JSON export
  running: Boolean,
  zen: Boolean,
})
const emit = defineEmits(['update:zen'])

const note = ref('')
let noteTimer = null
function flash(text) { note.value = text; clearTimeout(noteTimer); noteTimer = setTimeout(() => { note.value = '' }, 2000) }
async function copyText(text, done) {
  try { await navigator.clipboard.writeText(text); flash(done) } catch { flash('复制失败：浏览器不允许访问剪贴板') }
}
const copyLink = () => copyText(location.origin + taskPath(props.task.id), '链接已复制')

const menu = ref(false)
function closeMenu(e) { if (!e || !e.target.closest?.('.export-menu')) { menu.value = false; document.removeEventListener('click', closeMenu) } }
function toggleMenu() {
  menu.value = !menu.value
  if (menu.value) setTimeout(() => document.addEventListener('click', closeMenu), 0)
}
function exportAs(kind) {
  closeMenu()
  if (kind === 'md') return copyText(toMarkdown(props.task, props.rows), 'Markdown 表格已复制')
  if (kind === 'csv') download(exportFilename(props.task, 'csv'), toCSV(props.task, props.rows), 'text/csv;charset=utf-8')
  else download(exportFilename(props.task, 'json'), toJSON(props.task, props.results), 'application/json')
  flash('已开始下载')
}
onBeforeUnmount(() => { clearTimeout(noteTimer); document.removeEventListener('click', closeMenu) })
</script>

<template>
  <div class="result-head">
    <div class="result-title">
      <span class="badge" :class="task.type">{{ typeLabel[task.type] }}</span>
      <h2>{{ TYPE_INFO[task.type]?.title || task.type }}</h2>
      <span class="mono target" :title="task.target">{{ task.target }}</span>
      <span class="badge" v-if="task.params?.continuous" title="持续检测：长时间连续采样">持续 {{ task.params.count }} 次</span>
    </div>
    <div class="result-meta">
      <span class="sub">{{ fmtTime(task.created_at) }}</span>
      <span v-if="running" class="live"><span class="pulse"></span>实时更新</span>
      <span class="sub" v-if="note" role="status">{{ note }}</span>
      <span class="spacer"></span>
      <button class="btn sm" type="button" title="复制这次检测结果的地址；游客的结果只有本浏览器能打开" @click="copyLink">复制链接</button>
      <div class="export-menu">
        <button class="btn sm" type="button" aria-haspopup="menu" :aria-expanded="menu" @click="toggleMenu">导出 ▾</button>
        <div class="nav-pop right" v-if="menu" role="menu">
          <button type="button" role="menuitem" @click="exportAs('md')">复制 Markdown 表格</button>
          <button type="button" role="menuitem" @click="exportAs('csv')">下载 CSV</button>
          <button type="button" role="menuitem" @click="exportAs('json')">下载 JSON（完整数据）</button>
        </div>
      </div>
      <button class="btn sm" type="button" :aria-pressed="zen" :title="zen ? '退出清爽模式（Esc）' : '只留结果，隐藏表单和导航'" @click="emit('update:zen', !zen)">{{ zen ? '退出清爽模式' : '清爽模式' }}</button>
    </div>
  </div>
</template>
