<script setup>
// A sortable column header: a real button inside the th, so it is reachable
// by keyboard and announces its sort state.
defineProps({ sort: { type: Object, required: true }, k: { type: String, required: true }, num: Boolean, title: String })
</script>
<template>
  <th class="sortable" :class="{ num }" :aria-sort="sort.aria(k)" :title="title">
    <button type="button" class="th-btn" @click="sort.sortBy(k)"><slot></slot>{{ sort.arrow(k) }}</button>
  </th>
</template>
