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
  // Set on a shared result (/s/<token>): what the share says about itself.
  // The page is then read-only and some of the task may be withheld.
  share: { type: Object, default: null },
})
const emit = defineEmits(['update:zen', 'share', 'screenshot'])

const note = ref('')
let noteTimer = null
function flash(text) { note.value = text; clearTimeout(noteTimer); noteTimer = setTimeout(() => { note.value = '' }, 2000) }
async function copyText(text, done) {
  try { await navigator.clipboard.writeText(text); flash(done) } catch { flash('复制失败：浏览器不允许访问剪贴板') }
}
const copyLink = () => copyText(props.share ? location.href : location.origin + taskPath(props.task.id), '链接已复制')
// "发起同类探测" on a shared result: the same probe type, and the same target when it was shared.
const again = () => '/' + props.task.type + (props.task.target ? '?target=' + encodeURIComponent(props.task.target) : '')
const hasTime = (t) => !!t && !String(t).startsWith('0001-')

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
      <span class="mono target" v-if="task.target" :title="task.target">{{ task.target }}</span>
      <span class="sub target" v-else>检测目标已隐藏</span>
      <span class="badge" v-if="task.params?.continuous" title="持续检测：长时间连续采样">持续 {{ task.params.count }} 次</span>
    </div>
    <div class="result-meta">
      <span class="sub result-time" v-if="hasTime(task.created_at)">{{ share ? '检测时间：' : '' }}{{ fmtTime(task.created_at) }}</span>
      <span class="sub result-time" v-else>检测时间已隐藏</span>
      <span class="badge" v-if="share" :title="share.mode === 'follow' ? '分享者选择了跟随任务：结果会更新到任务结束' : '分享者创建链接时的快照'">分享结果</span>
      <span v-if="running" class="live"><span class="pulse"></span>实时更新</span>
      <span class="sub" v-if="note" role="status">{{ note }}</span>
      <span class="spacer"></span>
      <span class="no-shot result-actions">
      <button class="btn sm" type="button" :title="share ? '复制这个分享链接' : '复制这次检测结果的地址；只有你自己（游客则只有本浏览器）能打开，给别人看请用分享'" @click="copyLink">复制链接</button>
      <button v-if="!share" class="btn sm" type="button" title="创建可撤销的结果分享链接" @click="emit('share')">分享</button>
      <button class="btn sm" type="button" title="设置并保存结果截图" @click="emit('screenshot')">保存截图</button>
      <div class="export-menu">
        <button class="btn sm" type="button" aria-haspopup="menu" :aria-expanded="menu" @click="toggleMenu">导出 ▾</button>
        <div class="nav-pop right" v-if="menu" role="menu">
          <button type="button" role="menuitem" @click="exportAs('md')">复制 Markdown 表格</button>
          <button type="button" role="menuitem" @click="exportAs('csv')">下载 CSV</button>
          <button type="button" role="menuitem" @click="exportAs('json')">下载 JSON（完整数据）</button>
        </div>
      </div>
      <button v-if="!share" class="btn sm" type="button" :aria-pressed="zen" :title="zen ? '退出清爽模式（Esc）' : '只留结果，隐藏表单和导航'" @click="emit('update:zen', !zen)">{{ zen ? '退出清爽模式' : '清爽模式' }}</button>
      <a v-else class="btn sm" :href="again()">发起同类探测</a>
      </span>
    </div>
  </div>
</template>
