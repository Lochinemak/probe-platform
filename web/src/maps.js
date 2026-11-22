// The generated map modules (src/maps/*.js, written by scripts/build-maps.mjs)
// load on demand, each as its own chunk, so a page without a map does not pay
// for one. A map that has not been generated is simply not offered.
const loaders = import.meta.glob('./maps/*.js', { import: 'default' })

export const MAP_SCOPES = [
  { id: 'china', label: '中国', by: 'province' }, // region ids are province short names
  { id: 'world', label: '全球', by: 'cc' }, // region ids are ISO country codes
].filter((s) => loaders[`./maps/${s.id}.js`])

const cache = new Map()
export function loadMap(id) {
  if (!cache.has(id)) cache.set(id, loaders[`./maps/${id}.js`]())
  return cache.get(id)
}

// regionId is where a node's geography puts it on a map of the given scope,
// '' when it has no place there.
export function regionId(scope, geo) {
  if (!geo?.cc) return ''
  if (scope === 'world') return geo.cc
  return geo.cc === 'CN' ? geo.province || '' : ''
}
