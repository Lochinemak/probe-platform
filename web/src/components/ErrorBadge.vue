<script setup>
import { ref, onBeforeUnmount } from 'vue'
import { errorLabel } from '../fmt.js'

// Short badge in the table; the full message opens in a fixed-position
// popover teleported to <body> so it never widens the table or gets clipped
// by the horizontal scroll container.
const props = defineProps({ message: { type: String, default: '' } })
const open = ref(false)
const pos = ref({ top: 0, left: 0 })
const copied = ref(false)

function close() {
  open.value = false
  document.removeEventListener('click', close)
  document.removeEventListener('keydown', onKey)
  window.removeEventListener('scroll', close, true)
}
function onKey(e) { if (e.key === 'Escape') close() }
function toggle(e) {
  if (open.value) return close()
  const r = e.currentTarget.getBoundingClientRect()
  const width = Math.min(460, window.innerWidth - 24)
  pos.value = { top: r.bottom + 6, left: Math.max(12, Math.min(r.left, window.innerWidth - width - 12)), width }
  open.value = true
  copied.value = false
  setTimeout(() => {
    document.addEventListener('click', close)
    document.addEventListener('keydown', onKey)
    window.addEventListener('scroll', close, true)
  }, 0)
}
async function copy() {
  try { await navigator.clipboard.writeText(props.message); copied.value = true } catch { /* clipboard unavailable */ }
}
onBeforeUnmount(close)
</script>

<template>
  <span class="badge error clickable" title="点击查看详情" @click.stop="toggle">{{ errorLabel(message) }}</span>
  <Teleport to="body">
    <div v-if="open" class="popover" :style="{ top: pos.top + 'px', left: pos.left + 'px', width: pos.width + 'px' }" @click.stop>
      <div class="popover-title">错误详情</div>
      <div class="popover-text">{{ message }}</div>
      <div class="popover-actions">
        <button class="btn sm" type="button" @click="copy">{{ copied ? '已复制' : '复制' }}</button>
        <button class="btn sm" type="button" @click="close">关闭</button>
      </div>
    </div>
  </Teleport>
</template>
