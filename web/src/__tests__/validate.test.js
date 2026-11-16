import { describe, it, expect } from 'vitest'
import { validateTarget, isIPv4, isIPv6, isHostname, splitHostPort } from '../validate.js'

const ok = (type, raw, opts) => { const v = validateTarget(type, raw, opts); expect(v.ok, `${type} ${raw}: ${v.error}`).toBe(true); return v }
const bad = (type, raw, part) => { const v = validateTarget(type, raw); expect(v.ok, `${type} ${raw}`).toBe(false); if (part) expect(v.error).toContain(part) }

describe('address helpers', () => {
  it('recognises IPv4', () => {
    expect(isIPv4('223.5.5.5')).toBe(true)
    expect(isIPv4('256.1.1.1')).toBe(false)
    expect(isIPv4('1.1.1')).toBe(false)
  })
  it('recognises IPv6', () => {
    for (const a of ['::1', '2400:3200::1', 'fe80::1%eth0', '::ffff:1.2.3.4', '2001:db8:0:0:0:0:0:1']) expect(isIPv6(a), a).toBe(true)
    for (const a of ['1.1.1.1', 'example.com', '1::2::3', '2001:db8', '12345::1', 'host:443']) expect(isIPv6(a), a).toBe(false)
  })
  it('recognises hostnames', () => {
    for (const h of ['example.com', 'a.b-c.example.', '_dmarc.example.com', '中文.cn', 'localhost']) expect(isHostname(h), h).toBe(true)
    for (const h of ['-a.com', 'a..b', 'a b', 'exa$mple.com', '']) expect(isHostname(h), h).toBe(false)
  })
  it('splits host and port', () => {
    expect(splitHostPort('example.com:443')).toMatchObject({ host: 'example.com', port: '443', ok: true })
    expect(splitHostPort('[2400:3200::1]:53')).toMatchObject({ host: '2400:3200::1', port: '53', ok: true })
    expect(splitHostPort('2400:3200::1')).toMatchObject({ host: '2400:3200::1', port: '' })
    expect(splitHostPort('example.com:99999').ok).toBe(false)
  })
})

describe('validateTarget', () => {
  it('rejects empty and whitespace', () => {
    bad('ping', '  ', '请输入')
    bad('ping', 'a b.com', '空白')
  })
  it('ping / mtr / dns take a bare host', () => {
    expect(ok('ping', ' www.qq.com ').target).toBe('www.qq.com')
    expect(ok('mtr', '1.1.1.1').target).toBe('1.1.1.1')
    expect(ok('ping', '[2400:3200::1]').target).toBe('2400:3200::1')
    expect(ok('dns', '_dmarc.example.com').target).toBe('_dmarc.example.com')
    bad('ping', 'https://example.com', '不要包含协议')
    bad('dns', 'example.com:53', '不能包含端口')
    bad('dns', 'example.com/path', '路径')
    bad('mtr', 'user@example.com', '用户信息')
    bad('ping', 'exa$mple', '格式不正确')
  })
  it('tcping takes host or host:port', () => {
    expect(ok('tcping', 'example.com')).toMatchObject({ target: 'example.com', port: 0 })
    expect(ok('tcping', 'example.com:443')).toMatchObject({ target: 'example.com:443', port: 443 })
    expect(ok('tcping', '[::1]:22')).toMatchObject({ port: 22 })
    bad('tcping', 'example.com:0', '1-65535')
    bad('tcping', 'example.com:http', '1-65535')
    bad('tcping', 'tcp://example.com:80', '不要包含协议')
  })
  it('http builds a full URL', () => {
    expect(ok('http', 'example.com/path?q=1').target).toBe('https://example.com/path?q=1')
    expect(ok('http', 'example.com', { scheme: 'http' }).target).toBe('http://example.com')
    expect(ok('http', 'http://example.com:8080/')).toMatchObject({ target: 'http://example.com:8080/', scheme: 'http' })
    expect(ok('http', 'HTTPS://Example.com').scheme).toBe('https')
    bad('http', 'ftp://example.com', 'http 或 https')
    bad('http', 'https://user:pw@example.com', '用户名或密码')
    bad('http', 'https://example.com/#frag', '片段')
    bad('http', 'https://example.com:99999/', '')
    bad('http', 'https://', '')
  })
})
