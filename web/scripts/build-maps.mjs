#!/usr/bin/env node
// Generates the map modules in src/maps/ from boundary datasets. The output
// is committed; this script is run by hand when a dataset is updated, never
// in CI or at build time, so building the dashboard needs no network and no
// GIS tooling.
//
//   node scripts/build-maps.mjs --world <ne_10m_admin_0_countries_chn.shp>
//   node scripts/build-maps.mjs --china <天地图省级行政区划.geojson>
//
// Either flag may be given alone. See src/maps/README.md for where the
// datasets come from and the terms they are used under.
//
// mapshaper (run through npx, pinned below) does the GIS work: dissolving,
// topology-preserving simplification and projection. What is left for this
// file is turning projected rings into SVG path strings.
import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, writeFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const MAPSHAPER = 'mapshaper@0.8.7'
const OUT_DIR = resolve(dirname(fileURLToPath(import.meta.url)), '../src/maps')
const WIDTH = 1000 // viewBox width; heights follow from the projection

function arg(name) {
  const i = process.argv.indexOf('--' + name)
  return i > 0 ? process.argv[i + 1] : ''
}

// mapshaper runs the commands on input (a file path, or a GeoJSON object) and
// returns the resulting GeoJSON.
function mapshaper(input, commands) {
  const dir = mkdtempSync(join(tmpdir(), 'maps-'))
  const out = join(dir, 'out.geojson')
  try {
    let src = input
    if (typeof input !== 'string') { src = join(dir, 'in.geojson'); writeFileSync(src, JSON.stringify(input)) }
    execFileSync('npx', ['-y', MAPSHAPER, resolve(src), ...commands, '-o', 'format=geojson', 'precision=0.01', out], { stdio: ['ignore', 'inherit', 'inherit'], cwd: dir })
    return JSON.parse(readFileSync(out, 'utf8'))
  } finally { rmSync(dir, { recursive: true, force: true }) }
}

const ringsOf = (g) => (!g ? [] : g.type === 'Polygon' ? g.coordinates : g.type === 'MultiPolygon' ? g.coordinates.flat() : [])
function ringArea(ring) {
  let a = 0
  for (let i = 0, j = ring.length - 1; i < ring.length; j = i++) a += ring[j][0] * ring[i][1] - ring[i][0] * ring[j][1]
  return a / 2
}
function ringCentroid(ring) {
  let a = 0, cx = 0, cy = 0
  for (let i = 0, j = ring.length - 1; i < ring.length; j = i++) {
    const f = ring[j][0] * ring[i][1] - ring[i][0] * ring[j][1]
    a += f; cx += (ring[j][0] + ring[i][0]) * f; cy += (ring[j][1] + ring[i][1]) * f
  }
  return a ? [cx / (3 * a), cy / (3 * a)] : ring[0]
}

const PAD = 4
const n1 = (v) => String(Math.round(v * 10) / 10)
function boundsOf(features) {
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity
  const see = ([x, y]) => { x0 = Math.min(x0, x); x1 = Math.max(x1, x); y0 = Math.min(y0, y); y1 = Math.max(y1, y) }
  for (const f of features) {
    const g = f.geometry
    if (!g) continue
    if (g.type === 'LineString') g.coordinates.forEach(see)
    else if (g.type === 'MultiLineString') g.coordinates.flat().forEach(see)
    else ringsOf(g).flat().forEach(see)
  }
  return { x0, y0, x1, y1 }
}
// fitter maps projected coordinates into a WIDTH-wide viewBox that spans the
// bounds' x range, north at the top (SVG's y axis points down).
function fitter({ x0, x1, y1 }) {
  const k = (WIDTH - 2 * PAD) / (x1 - x0)
  return ([x, y]) => [(x - x0) * k + PAD, (y1 - y) * k + PAD]
}

// toRegions writes each projected feature as one path in a viewBox fitted to
// all of them (or by the given `fit`). Rings smaller than minRing square units
// are dropped (islets that would be sub-pixel) unless keepAll says the region
// keeps every ring, and every region keeps its largest ring; a region whose
// whole outline is smaller than smallBelow is flagged `small` so the map can
// draw a dot the eye and the pointer can find.
function toRegions(features, { idOf, nameOf, minRing = 1.5, smallBelow = 30, keepAll = () => false, fit }) {
  const b = boundsOf(features)
  fit = fit || fitter(b)
  const height = Math.ceil(fit([b.x0, b.y0])[1] + PAD)
  const n = n1
  const regions = []
  for (const f of features) {
    const rings = ringsOf(f.geometry).map((r) => r.map(fit)).map((r) => ({ r, area: Math.abs(ringArea(r)) })).sort((a, b) => b.area - a.area)
    if (!rings.length) continue
    const kept = rings.filter((x, i) => i === 0 || x.area >= minRing || keepAll(f))
    const d = kept.map(({ r }) => 'M' + r.slice(0, -1).map(([x, y]) => `${n(x)},${n(y)}`).join('L') + 'Z').join('')
    const [cx, cy] = ringCentroid(rings[0].r)
    const total = rings.reduce((s, x) => s + x.area, 0)
    const region = { id: idOf(f), name: nameOf(f), d, cx: Math.round(cx * 10) / 10, cy: Math.round(cy * 10) / 10 }
    if (total < smallBelow) region.small = true
    regions.push(region)
  }
  return { viewBox: [0, 0, WIDTH, height], regions }
}

function write(name, map, header) {
  const file = join(OUT_DIR, name + '.js')
  const body = `// Generated by scripts/build-maps.mjs. Do not edit: regenerate it instead.\n${header.map((l) => '// ' + l).join('\n')}\nexport default ${JSON.stringify(map)}\n`
  writeFileSync(file, body)
  console.log(`wrote ${file}: ${map.regions.length} regions, ${(body.length / 1024).toFixed(1)} KB`)
}

// --- world: one region per country, keyed by ISO 3166-1 alpha-2 -------------
// Natural Earth's "China point of view" edition: boundaries as China draws
// them, with Taiwan part of China. Hong Kong and Macau are separate features
// there; they are merged into CN here because the dashboard files them (and
// Taiwan) as provinces of China. Antarctica is left out. Features without a
// country code (a few dependencies and unclaimed specks) are merged into one
// shape that is drawn but cannot be selected.
function buildWorld(input) {
  const gj = mapshaper(input, [
    '-filter', 'ADM0_A3 != "ATA"',
    '-each', 'cc = SOVEREIGNT == "China" ? "CN" : (ISO_A2_EH == "-99" ? "" : ISO_A2_EH), zh = cc == "CN" ? "中国" : NAME_ZH, size = this.area',
    '-sort', 'size', 'descending',
    '-dissolve', 'cc', 'copy-fields=zh',
    '-simplify', 'weighted', '1%', 'keep-shapes',
    '-proj', '+proj=robin +lon_0=150',
    '-clean',
  ])
  const map = toRegions(gj.features, { idOf: (f) => f.properties.cc, nameOf: (f) => f.properties.zh })
  const other = map.regions.find((r) => !r.id)
  map.regions = map.regions.filter((r) => r.id).sort((a, b) => a.id.localeCompare(b.id))
  if (other) map.other = other.d
  write('world', { id: 'world', credit: '世界地图数据：Natural Earth（公有领域），中国视角版本', ...map }, [
    'Source: Natural Earth 1:10m Admin 0 – Countries, China point-of-view edition (public domain).',
    'Robinson projection centred on 150°E. Region ids are ISO 3166-1 alpha-2 codes; CN includes Hong Kong, Macau and Taiwan.',
  ])
}

// --- china: one region per province-level division, keyed by short name -----
// 天地图's province dataset: 34 polygons (name, gb code) plus the 境界线 line
// features that belong on any map of China, the South China Sea dashes among
// them. Nothing is moved or dropped: the provinces are simplified together so
// shared borders stay shared, the lines are drawn as given, and the southern
// part of the sea, which would otherwise take a third of the picture, is shown
// the conventional way, in an inset.
const CHINA_PROJ = '+proj=aea +lat_1=25 +lat_2=47 +lat_0=36 +lon_0=105 +ellps=GRS80' // Albers equal-area, the usual choice for China
const MAIN_SOUTH = 15.8 // the main frame ends south of Hainan island, far enough for the inset to fit below Taiwan
const INSET = { west: 106.3, east: 123.2, south: 2.6, north: 24.2, width: 128, label: '南海诸岛' }
const shortName = (name) => name.replace(/(壮族|回族|维吾尔)?自治区$|特别行政区$|省$|市$/, '')

function buildChina(input) {
  const src = JSON.parse(readFileSync(resolve(input), 'utf8'))
  const polys = src.features.filter((f) => /Polygon$/.test(f.geometry?.type) && f.properties?.gb)
  const lines = src.features.filter((f) => /LineString$/.test(f.geometry?.type))
  if (polys.length !== 34) throw new Error(`expected 34 province-level divisions, found ${polys.length}`)

  const provinces = mapshaper({ type: 'FeatureCollection', features: polys }, ['-simplify', 'weighted', '6%', 'keep-shapes', '-proj', CHINA_PROJ, '-clean'])
  // Helper lines projected along with the boundary lines: the inset's frame
  // and the parallel where the main frame ends, both drawn with enough points
  // to follow the projection's curves.
  const steps = (a, b, fixed, lonFirst) => Array.from({ length: 41 }, (_, i) => { const v = a + ((b - a) * i) / 40; return lonFirst ? [v, fixed] : [fixed, v] })
  const frame = [...steps(INSET.west, INSET.east, INSET.south, true), ...steps(INSET.south, INSET.north, INSET.east, false), ...steps(INSET.east, INSET.west, INSET.north, true), ...steps(INSET.north, INSET.south, INSET.west, false)]
  const helper = (kind, coordinates) => ({ type: 'Feature', properties: { kind }, geometry: { type: 'LineString', coordinates } })
  const drawn = mapshaper({ type: 'FeatureCollection', features: [
    ...lines.map((f) => ({ ...f, properties: { kind: 'line' } })),
    helper('inset', frame), helper('south', steps(107, 112, MAIN_SOUTH, true)),
  ] }, ['-simplify', 'weighted', '25%', '-proj', CHINA_PROJ])
  const of = (kind) => drawn.features.filter((f) => f.properties.kind === kind)

  const fit = fitter(boundsOf(provinces.features))
  const map = toRegions(provinces.features, {
    fit, idOf: (f) => shortName(f.properties.name), nameOf: (f) => shortName(f.properties.name),
    minRing: 0.3, smallBelow: 60,
    // Hainan's rings include the South China Sea islands and Taiwan's the
    // Diaoyu Islands: specks at this scale, but they stay.
    keepAll: (f) => ['海南省', '台湾省'].includes(f.properties.name),
  })
  map.regions.sort((a, b) => a.id.localeCompare(b.id, 'zh-CN'))
  map.overlays = of('line').flatMap((f) => (f.geometry.type === 'LineString' ? [f.geometry.coordinates] : f.geometry.coordinates))
    .map((line) => ({ d: 'M' + line.map(fit).map(([x, y]) => `${n1(x)},${n1(y)}`).join('L') }))

  // The main frame stops at MAIN_SOUTH; what lies beyond is in the inset.
  const south = boundsOf(of('south'))
  const height = Math.ceil(fit([0, south.y0])[1])
  map.viewBox = [0, 0, WIDTH, height]
  const ib = boundsOf(of('inset'))
  const [ix0, iy0] = fit([ib.x0, ib.y1]), [ix1, iy1] = fit([ib.x1, ib.y0])
  const iw = INSET.width, ih = Math.round((iw * (iy1 - iy0)) / (ix1 - ix0))
  map.inset = { label: INSET.label, from: [ix0, iy0, ix1 - ix0, iy1 - iy0].map((v) => Math.round(v * 10) / 10), at: [WIDTH - PAD - iw, height - PAD - ih, iw, ih] }
  // The inset must sit on open sea: it may not cover any province's mainland.
  const [ax, ay] = map.inset.at
  for (const f of provinces.features) {
    const main = ringsOf(f.geometry).map((r) => r.map(fit)).sort((a, b) => Math.abs(ringArea(b)) - Math.abs(ringArea(a)))[0]
    if (main.some(([x, y]) => x > ax - 3 && y > ay - 3 && y < height)) throw new Error(`the inset covers ${f.properties.name}: move or shrink it`)
  }

  write('china', { id: 'china', credit: '中国地图数据：天地图 · 审图号 GS（2024）0650号', ...map }, [
    'Source: 天地图 行政区划可视化, province level (cloudcenter.tianditu.gov.cn/administrativeDivision), 审图号 GS（2024）0650号.',
    'Terms: "该数据仅供地图可视化使用". Albers equal-area projection. Region ids are province short names (Geo.province).',
  ])
}

const world = arg('world'), china = arg('china')
if (!world && !china) {
  console.error('usage: node scripts/build-maps.mjs [--world <countries.shp>] [--china <provinces.geojson>]')
  process.exit(2)
}
if (world) buildWorld(world)
if (china) buildChina(china)
