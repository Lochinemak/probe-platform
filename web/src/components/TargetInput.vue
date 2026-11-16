<script setup>
// The target box: a text input that offers the recently used targets of the
// current probe type. Focus opens the list, typing filters it, ↑/↓ pick an
// entry, Enter fills it in, Delete removes the picked entry and Alt+Delete
// clears the whole list. Enter with nothing picked submits the form as usual.
import { ref, computed, watch } from 'vue'

const props = defineProps({
  modelValue: { type: String, default: '' },
  items: { type: Array, default: () => [] },
  placeholder: { type: String, default: '' },
  invalid: Boolean,
})
const emit = defineEmits(['update:modelValue', 'remove', 'clear'])

const input = ref(null)
const open = ref(false)
const active = ref(-1)
const listId = 'recent-' + Math.random().toString(36).slice(2, 8)

const shown = computed(() => {
  const q = props.modelValue.trim().toLowerCase()
  // An exact match is what is already typed; offering it back is noise.
  return props.items.filter((t) => !q || (t.toLowerCase().includes(q) && t.toLowerCase() !== q))
})
watch(shown, (l) => { if (active.value >= l.length) active.value = l.length - 1 })

function pick(t) {
  emit('update:modelValue', t)
  open.value = false
  active.value = -1
  input.value?.focus()
}
function onInput(e) {
  emit('update:modelValue', e.target.value)
  open.value = true
  active.value = -1
}
function onKey(e) {
  const n = shown.value.length
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    if (!n) return
    e.preventDefault()
    if (!open.value) { open.value = true; active.value = e.key === 'ArrowDown' ? 0 : n - 1; return }
    active.value = e.key === 'ArrowDown' ? (active.value + 1) % n : (active.value <= 0 ? n - 1 : active.value - 1)
  } else if (e.key === 'Enter') {
    if (open.value && active.value >= 0) { e.preventDefault(); pick(shown.value[active.value]) } else open.value = false
  } else if (e.key === 'Escape') {
    if (open.value) { e.preventDefault(); open.value = false; active.value = -1 }
  } else if (e.key === 'Delete') {
    if (e.altKey && props.items.length) { e.preventDefault(); emit('clear'); active.value = -1 } else if (open.value && active.value >= 0) { e.preventDefault(); emit('remove', shown.value[active.value]) }
  }
}
function focus() { input.value?.focus() }
defineExpose({ focus })
</script>

<template>
  <div class="target-box" :class="{ invalid }">
    <slot name="prefix"></slot>
    <input ref="input" class="target-input" type="text" :value="modelValue" :placeholder="placeholder" spellcheck="false" autocomplete="off" autocapitalize="off"
      role="combobox" aria-label="探测目标" aria-autocomplete="list" :aria-expanded="open && shown.length > 0" :aria-controls="listId"
      :aria-activedescendant="active >= 0 ? `${listId}-${active}` : undefined"
      @input="onInput" @focus="open = true" @click="open = true" @blur="open = false; active = -1" @keydown="onKey" />
    <div v-if="open && shown.length" class="recent-list" :id="listId" role="listbox" aria-label="最近使用的探测目标" @mousedown.prevent>
      <div class="recent-head">
        <span>最近使用 · ↑↓ 选择，回车填入，Delete 删除</span>
        <button type="button" class="btn link sm" title="清空这一类型的记录（Alt+Delete）" @click="emit('clear')">清空</button>
      </div>
      <div v-for="(t, i) in shown" :key="t" class="recent-item" :class="{ active: i === active }" :id="`${listId}-${i}`" role="option" :aria-selected="i === active" @mousemove="active = i" @click="pick(t)">
        <span class="mono">{{ t }}</span>
        <button type="button" class="recent-del" :aria-label="`删除历史目标 ${t}`" title="删除这条记录" @click.stop="emit('remove', t)">×</button>
      </div>
    </div>
  </div>
</template>
