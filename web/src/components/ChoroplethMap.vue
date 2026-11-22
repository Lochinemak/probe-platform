<script setup>
// An SVG map whose regions are filled by value. `map` is one of the generated
// modules in src/maps (viewBox, regions with an id, a name, a path and a
// centre). `values` maps a region id to { fill, title, lines }: the CSS colour
// to paint it, and what the tooltip says. Regions with a value can be picked
// (click, Enter or Space); picking the selected one again clears it.
import { ref, computed } from 'vue'

const props = defineProps({
  map: { type: Object, required: true },
  values: { type: Object, default: () => ({}) },
  selected: { type: String, default: '' },
  label: { type: String, default: '地图' },
})
const emit = defineEmits(['select'])

const box = ref(null)
// The inset (the South China Sea on the map of China) redraws the same shapes
// through <use>, so they need an id that is unique on the page.
const gid = 'map-' + Math.random().toString(36).slice(2, 8)
const tip = ref(null) // { id, x, y }
const viewBox = computed(() => props.map.viewBox.join(' '))
const withValue = computed(() => props.map.regions.filter((r) => props.values[r.id]))
// Painted last so a selected or hovered outline is not covered by neighbours.
const ordered = computed(() => [...props.map.regions].sort((a, b) => rank(a) - rank(b)))
function rank(r) { return (r.id === props.selected ? 2 : 0) + (props.values[r.id] ? 1 : 0) }

function pick(r) { if (props.values[r.id]) emit('select', r.id === props.selected ? '' : r.id) }
function move(e, r) {
  if (!props.values[r.id]) { tip.value = null; return }
  const b = box.value.getBoundingClientRect()
  tip.value = { id: r.id, x: e.clientX - b.left, y: e.clientY - b.top, flip: e.clientX - b.left > b.width * 0.6 }
}
function focus(r) {
  const b = box.value.getBoundingClientRect()
  const [, , w, h] = props.map.viewBox
  tip.value = { id: r.id, x: (r.cx / w) * b.width, y: (r.cy / h) * b.height, flip: r.cx > w * 0.6 }
}
const tipRegion = computed(() => (tip.value ? props.map.regions.find((r) => r.id === tip.value.id) : null))
const tipValue = computed(() => (tip.value ? props.values[tip.value.id] : null))
</script>

<template>
  <div class="map-box" :class="map.id" ref="box" @mouseleave="tip = null">
    <svg :viewBox="viewBox" role="group" :aria-label="label" preserveAspectRatio="xMidYMid meet">
      <g :id="gid">
      <path v-if="map.other" class="map-other" :d="map.other" />
      <template v-for="r in ordered" :key="r.id">
        <path class="map-region" :class="{ has: values[r.id], on: r.id === selected }" :d="r.d" :style="values[r.id] ? { fill: values[r.id].fill } : null"
          :tabindex="values[r.id] ? 0 : undefined" :role="values[r.id] ? 'button' : undefined" :aria-pressed="values[r.id] ? r.id === selected : undefined"
          :aria-label="values[r.id] ? `${r.name}：${values[r.id].title}` : undefined"
          @click="pick(r)" @keydown.enter.prevent="pick(r)" @keydown.space.prevent="pick(r)" @mousemove="move($event, r)" @focus="focus(r)" @blur="tip = null" />
      </template>
      <!-- A region too small to see at this scale gets a dot when it has something to show. -->
      <circle v-for="r in withValue.filter((x) => x.small)" :key="'dot-' + r.id" class="map-dot" :class="{ on: r.id === selected }" :cx="r.cx" :cy="r.cy" r="5" :style="{ fill: values[r.id].fill }"
        @click="pick(r)" @mousemove="move($event, r)" />
      <path v-for="(o, i) in map.overlays || []" :key="'o' + i" class="map-overlay" :d="o.d" />
      </g>
      <template v-if="map.inset">
        <svg class="map-inset" :x="map.inset.at[0]" :y="map.inset.at[1]" :width="map.inset.at[2]" :height="map.inset.at[3]" :viewBox="map.inset.from.join(' ')" aria-hidden="true">
          <rect class="map-inset-bg" :x="map.inset.from[0]" :y="map.inset.from[1]" :width="map.inset.from[2]" :height="map.inset.from[3]" />
          <use :href="'#' + gid" />
        </svg>
        <rect class="map-inset-frame" :x="map.inset.at[0]" :y="map.inset.at[1]" :width="map.inset.at[2]" :height="map.inset.at[3]" />
        <text class="map-inset-label" :x="map.inset.at[0] + 5" :y="map.inset.at[1] + map.inset.at[3] - 6">{{ map.inset.label }}</text>
      </template>
    </svg>
    <div v-if="tip && tipValue" class="map-tip" :class="{ flip: tip.flip }" :style="{ left: tip.x + 'px', top: tip.y + 'px' }" role="tooltip">
      <b>{{ tipRegion?.name }}</b> <span class="sub">{{ tipValue.title }}</span>
      <div v-for="(l, i) in tipValue.lines || []" :key="i" class="map-tip-line"><span>{{ l[0] }}</span><span class="mono" :class="l[2]">{{ l[1] }}</span></div>
    </div>
  </div>
</template>
