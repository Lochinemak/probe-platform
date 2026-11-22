import { describe, it, expect } from 'vitest'
import { regionName, isMainland, placeKey, placeLabel, placeOrder, UNKNOWN_PLACE } from '../geo.js'

const gd = { cc: 'CN', country: '中国', province: '广东', city: '深圳', line: '电信' }
const hk = { cc: 'CN', country: '中国', province: '香港' }
const jp = { cc: 'JP', country: '日本', province: '东京都', city: '东京' }
const us = { cc: 'US', country: '美国', province: 'California' }

describe('geo helpers', () => {
  it('names the result region', () => {
    expect(regionName(gd)).toBe('华南')
    expect(regionName(hk)).toBe('港澳台')
    expect(regionName(jp)).toBe('海外')
    expect(regionName({ cc: 'CN', country: '中国' })).toBe('中国')
    expect(regionName(null)).toBe(UNKNOWN_PLACE)
    expect(regionName({ line: '其他' })).toBe(UNKNOWN_PLACE)
  })
  it('tells the mainland apart', () => {
    expect(isMainland(gd)).toBe(true)
    expect(isMainland(hk)).toBe(false)
    expect(isMainland(jp)).toBe(false)
    expect(isMainland(undefined)).toBe(false)
  })
  it('files a node under a province or a country', () => {
    expect(placeKey(gd)).toBe('广东')
    expect(placeKey(jp)).toBe('日本')
    expect(placeKey({ cc: 'CN' })).toBe('中国')
    expect(placeKey({})).toBe(UNKNOWN_PLACE)
  })
  it('labels a place', () => {
    expect(placeLabel(gd)).toBe('广东 深圳')
    expect(placeLabel(hk)).toBe('香港')
    expect(placeLabel({ cc: 'CN', province: '上海', city: '上海' })).toBe('上海')
    expect(placeLabel(jp)).toBe('日本 东京')
    expect(placeLabel(us)).toBe('美国 California')
    expect(placeLabel(null)).toBe('')
  })
  it('orders places: provinces by region, then countries, then unknown', () => {
    expect(['日本', UNKNOWN_PLACE, '广东', '北京', '美国', '香港', '中国'].sort(placeOrder)).toEqual(['北京', '广东', '香港', '中国', '美国', '日本', UNKNOWN_PLACE]) // countries by pinyin
  })
})
