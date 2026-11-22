// Click-to-sort for the result tables. `columns` maps a sort key to a getter
// (row) => value. Rows without a value always go last, whichever direction.
import { ref, computed, unref } from 'vue'

export function compareValues(a, b) {
  if (typeof a === 'string' || typeof b === 'string') return String(a).localeCompare(String(b), 'zh-CN', { numeric: true })
  return a - b
}
const missing = (v) => v === undefined || v === null || v === '' || (typeof v === 'number' && Number.isNaN(v))

export function sortRows(rows, getter, dir = 1) {
  return [...rows].sort((x, y) => {
    const a = getter(x), b = getter(y)
    if (missing(a) && missing(b)) return 0
    if (missing(a)) return 1
    if (missing(b)) return -1
    return compareValues(a, b) * dir
  })
}

export function useSort(rows, columns) {
  const sortKey = ref('')
  const sortDir = ref(1)
  function sortBy(k) {
    if (sortKey.value !== k) { sortKey.value = k; sortDir.value = 1 } else if (sortDir.value === 1) sortDir.value = -1
    else { sortKey.value = ''; sortDir.value = 1 } // third click: back to the original order
  }
  const sorted = computed(() => {
    const list = (typeof rows === 'function' ? rows() : unref(rows)) || []
    const get = columns[sortKey.value]
    return get ? sortRows(list, get, sortDir.value) : list
  })
  const arrow = (k) => (sortKey.value === k ? (sortDir.value > 0 ? ' ↑' : ' ↓') : '')
  const aria = (k) => (sortKey.value === k ? (sortDir.value > 0 ? 'ascending' : 'descending') : 'none')
  return { sorted, sortKey, sortDir, sortBy, arrow, aria }
}
