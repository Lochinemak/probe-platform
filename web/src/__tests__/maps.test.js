import { describe, it, expect } from 'vitest'
import { MAP_SCOPES, loadMap, regionId } from '../maps.js'

describe('maps', () => {
  it('places a node on each map', () => {
    const gd = { cc: 'CN', province: '广东' }, hk = { cc: 'CN', province: '香港' }, jp = { cc: 'JP', country: '日本' }
    expect(regionId('china', gd)).toBe('广东')
    expect(regionId('china', hk)).toBe('香港')
    expect(regionId('china', jp)).toBe('')
    expect(regionId('china', { cc: 'CN' })).toBe('')
    expect(regionId('world', gd)).toBe('CN')
    expect(regionId('world', hk)).toBe('CN')
    expect(regionId('world', jp)).toBe('JP')
    expect(regionId('world', null)).toBe('')
    expect(regionId('world', { line: '其他' })).toBe('')
  })
  it('offers only the maps that were generated, and they are well formed', async () => {
    expect(MAP_SCOPES.map((s) => s.id)).toContain('world')
    for (const s of MAP_SCOPES) {
      const m = await loadMap(s.id)
      expect(m.viewBox).toHaveLength(4)
      expect(m.regions.length).toBeGreaterThan(30)
      const ids = m.regions.map((r) => r.id)
      expect(new Set(ids).size).toBe(ids.length)
      for (const r of m.regions) {
        expect(r.id && r.name && r.d.startsWith('M')).toBeTruthy()
        expect(Number.isFinite(r.cx) && Number.isFinite(r.cy)).toBe(true)
      }
    }
    const world = await loadMap('world')
    const ids = new Set(world.regions.map((r) => r.id))
    // China is one region: Hong Kong, Macau and Taiwan are not listed apart.
    for (const cc of ['CN', 'US', 'JP', 'SG', 'DE', 'RU', 'BR', 'AU']) expect(ids.has(cc), cc).toBe(true)
    for (const cc of ['HK', 'MO', 'TW', 'AQ']) expect(ids.has(cc), cc).toBe(false)
  })
})
