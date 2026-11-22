<script setup>
// The state of one node's result, the same in every result table: running,
// failed (click for the full reason), stopped by the user, or done.
import ErrorBadge from './ErrorBadge.vue'
defineProps({ r: { type: Object, required: true } })
</script>
<template>
  <span v-if="r.live" class="badge running"><span class="pulse"></span>{{ r.statusText }}</span>
  <ErrorBadge v-else-if="r.failed" :message="r.errorText || r.statusText" :label="r.statusText" />
  <span v-else class="badge" :class="r.stopped ? 'warn' : 'done'">{{ r.statusText }}</span>
</template>
