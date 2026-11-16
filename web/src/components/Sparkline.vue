<script setup>
import { computed } from 'vue'
const props = defineProps({ replies: { type: Array, default: () => [] }, total: { type: Number, default: 0 } })
// A continuous run has up to 100 samples; the row shows the latest MAX_BARS so
// the table does not grow sideways.
const MAX_BARS = 40
const shown = computed(() => props.replies.slice(-MAX_BARS))
const max = computed(() => Math.max(1, ...shown.value.filter((r) => r.ok).map((r) => r.rtt_ms)))
const pendingCount = computed(() => Math.max(0, Math.min(props.total, MAX_BARS) - shown.value.length))
</script>
<template>
  <span class="spark" :title="replies.map((r) => (r.ok ? r.rtt_ms.toFixed(1) + 'ms' : '×')).join(' ')">
    <i v-for="r in shown" :key="r.seq" :class="{ lost: !r.ok }" :style="{ height: r.ok ? Math.max(8, (r.rtt_ms / max) * 100) + '%' : '100%' }"></i>
    <i v-for="n in pendingCount" :key="'p' + n" class="pending" style="height: 30%"></i>
  </span>
</template>
