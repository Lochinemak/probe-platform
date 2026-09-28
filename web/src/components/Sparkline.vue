<script setup>
import { computed } from 'vue'
const props = defineProps({ replies: { type: Array, default: () => [] }, total: { type: Number, default: 0 } })
const max = computed(() => Math.max(1, ...props.replies.filter((r) => r.ok).map((r) => r.rtt_ms)))
const pendingCount = computed(() => Math.max(0, props.total - props.replies.length))
</script>
<template>
  <span class="spark" :title="replies.map((r) => (r.ok ? r.rtt_ms.toFixed(1) + 'ms' : '×')).join(' ')">
    <i v-for="r in replies" :key="r.seq" :class="{ lost: !r.ok }" :style="{ height: r.ok ? Math.max(8, (r.rtt_ms / max) * 100) + '%' : '100%' }"></i>
    <i v-for="n in pendingCount" :key="'p' + n" class="pending" style="height: 30%"></i>
  </span>
</template>
