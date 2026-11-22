import { describe, it, expect } from 'vitest'
import { derive, percentile } from '../derive.js'
import { errorCategory, bucketOf, dur } from '../fmt.js'
import { sortRows } from '../useSort.js'
import { toMarkdown, toCSV, toJSON, exportFilename } from '../export.js'

const reply = (seq, rtt) => ({ seq, ok: rtt !== null, rtt_ms: rtt ?? 0 })
const gd = { cc: 'CN', country: '中国', province: '广东', city: '深圳', line: '电信' }

describe('percentile', () => {
  it('uses the nearest rank', () => {
    const v = Array.from({ length: 100 }, (_, i) => i + 1)
    expect(percentile(v, 95)).toBe(95)
    expect(percentile([10, 20, 30, 40], 95)).toBe(40)
    expect(percentile([7], 95)).toBe(7)
    expect(percentile([], 95)).toBeUndefined()
  })
})

describe('derive', () => {
  const ping = { id: 't1', type: 'ping', target: '223.5.5.5', params: { count: 4 } }
  it('reads a finished ping', () => {
    const replies = [reply(0, 10), reply(1, 30), reply(2, null), reply(3, 20)]
    const r = derive(ping, { agent_id: 'a', agent_name: 'sz', status: 'done', geo: gd, target_geo: '中国 浙江省 杭州市 阿里',
      data: { ip: '223.5.5.5', replies, stats: { sent: 4, received: 3, loss_pct: 25, min_ms: 10, avg_ms: 20, max_ms: 30, stddev_ms: 8 } } })
    expect(r).toMatchObject({ metric: 20, p95: 30, latest: 20, failed: false, finished: true, statusText: '完成', line: '电信', place: '广东 深圳', region: '华南', placeKey: '广东', ip: '223.5.5.5', ipGeo: '中国 浙江省 杭州市 阿里' })
    expect(r.ips).toEqual([{ ip: '223.5.5.5', geo: '中国 浙江省 杭州市 阿里' }])
  })
  it('reads a ping still running from its progress, with the same shape', () => {
    const r = derive(ping, { agent_id: 'a', agent_name: 'sz', status: 'running', progress: [
      { kind: 'resolved', data: '223.5.5.5', geo: '中国 浙江省 杭州市' }, { kind: 'reply', data: reply(1, 30) }, { kind: 'reply', data: reply(0, 10) },
    ] })
    expect(r).toMatchObject({ live: true, metric: 20, ip: '223.5.5.5', ipGeo: '中国 浙江省 杭州市', failed: false, finished: false, statusText: '进行中' })
    expect(r.replies.map((x) => x.seq)).toEqual([0, 1])
    expect(r.stats).toMatchObject({ sent: 2, received: 2, loss_pct: 0 })
  })
  it('counts total loss as a timeout and a stopped run as stopped', () => {
    const lost = derive(ping, { status: 'done', data: { ip: '1.1.1.1', replies: [reply(0, null)], stats: { sent: 1, received: 0, loss_pct: 100 } } })
    expect(lost).toMatchObject({ failed: true, timedOut: true, statusText: '全部丢包', metric: undefined })
    const stopped = derive(ping, { status: 'done', error: 'stopped', data: { ip: '1.1.1.1', replies: [reply(0, 5)], stats: { sent: 1, received: 1, avg_ms: 5 } } })
    expect(stopped).toMatchObject({ failed: false, stopped: true, statusText: '已停止' })
  })
  it('classifies failures', () => {
    expect(derive(ping, { status: 'error', error: 'agent offline' })).toMatchObject({ failed: true, timedOut: false, statusText: '节点离线' })
    expect(derive(ping, { status: 'error', error: 'timeout' })).toMatchObject({ timedOut: true, statusText: '超时' })
    const http = { type: 'http', target: 'https://x', params: {} }
    const refused = derive(http, { status: 'done', data: { attempts: [{ seq: 0, ok: false, error: 'dial tcp 1.2.3.4:443: connect: connection refused' }] } })
    expect(refused).toMatchObject({ failed: true, statusText: '端口拒绝' })
    const assert = derive(http, { status: 'done', data: { attempts: [{ seq: 0, ok: true, assert_ok: false, ip: '1.2.3.4', status_code: 500, timing: { total_ms: 80 }, assertions: [{ name: '状态码', pass: false, detail: '500' }] }] } })
    expect(assert).toMatchObject({ failed: true, statusText: '断言未通过', metric: 80, ip: '1.2.3.4' })
    const dns = { type: 'dns', target: 'x.example', params: {} }
    const nx = derive(dns, { status: 'done', data: { attempts: [{ seq: 0, ok: false, rcode: 'NXDomain' }] } })
    expect(nx).toMatchObject({ failed: true, statusText: 'DNS 失败' })
    const ok = derive(dns, { status: 'done', data: { attempts: [{ seq: 0, ok: true, rtt_ms: 12, answers: [{ type: 'A', value: '1.1.1.1', geo: '澳大利亚' }, { type: 'CNAME', value: 'a.b.' }, { type: 'AAAA', value: '2606::1' }] }] } })
    expect(ok.metric).toBe(12)
    expect(ok.ips).toEqual([{ ip: '1.1.1.1', geo: '澳大利亚' }, { ip: '2606::1', geo: '' }])
  })
  it('summarises a trace by its last hop', () => {
    const mtr = { type: 'mtr', target: '1.1.1.1', params: {} }
    const hops = [{ ttl: 1, hosts: ['10.0.0.1'], sent: 3, received: 3, avg_ms: 1 }, { ttl: 2, hosts: [], sent: 3, received: 0 }, { ttl: 3, hosts: ['1.1.1.1'], sent: 3, received: 2, loss_pct: 33.3, avg_ms: 9, reached: true }]
    expect(derive(mtr, { status: 'done', data: { ip: '1.1.1.1', hops, reached: true, rounds: 3 } })).toMatchObject({ hopCount: 3, metric: 9, sampled: 3, failed: false })
    expect(derive(mtr, { status: 'done', data: { ip: '1.1.1.1', hops: hops.slice(0, 2), reached: false } })).toMatchObject({ hopCount: 1, failed: true, statusText: '未到达' })
  })
})

describe('derive for the newer probes', () => {
  it('reads a udp result like a ping, and says why nothing came back', () => {
    const udp = { type: 'udp', target: '1.1.1.1:53', params: {} }
    const okr = derive(udp, { status: 'done', data: { ip: '1.1.1.1', port: 53, proto: 'udp', replies: [{ seq: 0, ok: true, state: 'reply', rtt_ms: 8 }], stats: { sent: 1, received: 1, avg_ms: 8 } } })
    expect(okr).toMatchObject({ metric: 8, failed: false, ip: '1.1.1.1', port: 53 })
    const closed = derive(udp, { status: 'done', data: { ip: '1.1.1.1', replies: [{ seq: 0, ok: false, state: 'closed', error: 'port unreachable' }], stats: { sent: 1, received: 0 } } })
    expect(closed).toMatchObject({ failed: true, statusText: '端口关闭', timedOut: false })
    const silent = derive(udp, { status: 'done', data: { ip: '1.1.1.1', replies: [{ seq: 0, ok: false, state: 'no_reply', error: 'no reply' }], stats: { sent: 1, received: 0 } } })
    expect(silent).toMatchObject({ failed: true, statusText: '无响应', timedOut: true })
  })
  it('reads a TLS result: handshake, certificate, verification', () => {
    const tls = { type: 'tls', target: 'example.com', params: { expect_cert_days: 30 } }
    const chain = [{ subject: 'CN=example.com', issuer: 'CN=R3', not_after: '2027-01-01T00:00:00Z' }]
    const base = { ip: '93.184.216.34', port: 443, attempts: [{ seq: 0, ok: true, handshake_ms: 40, connect_ms: 10 }], stats: { sent: 1, received: 1, avg_ms: 40 }, chain, version: 'TLS 1.3' }
    expect(derive(tls, { status: 'done', data: { ...base, verified: true, cert_ok: true, days_left: 90 } })).toMatchObject({ metric: 40, failed: false, statusText: '完成', ip: '93.184.216.34' })
    expect(derive(tls, { status: 'done', data: { ...base, verified: false, verify_error: 'certificate has expired', cert_ok: true, days_left: -3 } })).toMatchObject({ failed: true, statusText: '证书无效', errorKind: 'tls' })
    expect(derive(tls, { status: 'done', data: { ...base, verified: true, cert_ok: false, days_left: 12 } })).toMatchObject({ failed: true, statusText: '即将到期' })
    expect(derive(tls, { status: 'done', data: { ip: '1.2.3.4', attempts: [{ seq: 0, ok: false, error: 'timeout' }], stats: { sent: 1, received: 0 } } })).toMatchObject({ failed: true, statusText: '超时', metric: undefined })
    // Still running: the attempts come from progress and there is no certificate yet.
    expect(derive(tls, { status: 'running', progress: [{ kind: 'resolved', data: '1.2.3.4' }, { kind: 'attempt', data: { seq: 0, ok: true, handshake_ms: 30 } }] })).toMatchObject({ live: true, metric: 30, ip: '1.2.3.4', leaf: null, failed: false })
  })
  it('groups a DNS comparison by answer, the majority first', () => {
    const cmp = { type: 'dns_compare', target: 'example.com', params: {} }
    const entry = (server, key, extra = {}) => ({ server, key, attempt: { ok: true, rtt_ms: 10, answers: key.split(',').map((v) => ({ type: 'A', value: v, geo: 'x' })), ...extra } })
    const entries = [entry('system', '1.1.1.1'), entry('223.5.5.5', '2.2.2.2', { rtt_ms: 25 }), entry('8.8.8.8', '1.1.1.1'), { server: '9.9.9.9', key: '', attempt: { ok: false, error: 'timeout', answers: [] } }]
    const r = derive(cmp, { status: 'done', data: { entries, verdict: 'inconsistent', groups: 2 } })
    expect(r).toMatchObject({ verdict: 'inconsistent', groups: 2, metric: 25, failed: false })
    expect(r.groupOf).toEqual({ '1.1.1.1': 1, '2.2.2.2': 2 })
    expect(r.ips.map((x) => x.ip)).toEqual(['1.1.1.1', '2.2.2.2'])
    // From progress alone the verdict is worked out here.
    const live = derive(cmp, { status: 'done', progress: entries.slice(0, 1).concat(entries.slice(2, 3)).map((data) => ({ kind: 'entry', data })) })
    expect(live).toMatchObject({ verdict: 'consistent', groups: 1 })
    expect(derive(cmp, { status: 'done', data: { entries: [entries[3]], verdict: 'failed', groups: 0 } })).toMatchObject({ failed: true, statusText: '超时' })
  })
  it('passes a node status through', () => {
    const r = derive({ type: 'status', target: 'self', params: {} }, { status: 'done', data: { os: 'linux', cpus: 4, load: [0.5, 0.4, 0.3] } })
    expect(r).toMatchObject({ failed: false, statusText: '完成', metric: undefined })
    expect(r.sys.cpus).toBe(4)
    expect(r.ips).toEqual([])
  })
})

describe('errorCategory', () => {
  it('prefers the specific cause', () => {
    const k = (m) => errorCategory(m).key
    expect(k('lookup example.com: i/o timeout')).toBe('dns')
    expect(k('dial tcp 1.2.3.4:443: i/o timeout')).toBe('connect_timeout')
    expect(k('dial tcp 1.2.3.4:443: connect: connection refused')).toBe('refused')
    expect(k('context deadline exceeded')).toBe('timeout')
    expect(k('tls: failed to verify certificate: x509: certificate has expired')).toBe('tls')
    expect(k('read: connection reset by peer')).toBe('reset')
    expect(k('connect: network is unreachable')).toBe('unreachable')
    expect(k('www.x.com 解析为 fake-IP 198.18.0.1')).toBe('fakeip')
    expect(k('10.0.0.1 不是公网地址：未登录的访客只能探测公网目标')).toBe('policy')
    expect(k('unknown task type "tls"')).toBe('unsupported')
    expect(k('something odd')).toBe('error')
  })
})

describe('latency scale', () => {
  it('grades link RTT in ms and HTTP in seconds', () => {
    expect([10, 50, 51, 299, 301].map((v) => bucketOf('ping', v))).toEqual([0, 0, 1, 5, 6])
    expect([400, 900, 12000].map((v) => bucketOf('http', v))).toEqual([0, 1, 6])
    expect(bucketOf('ping', undefined)).toBe(-1)
    expect(dur(1500)).toBe('1.50 s')
    expect(dur(15.2)).toBe('15.2 ms')
  })
})

describe('sortRows', () => {
  it('keeps rows without a value last in both directions', () => {
    const rows = [{ v: 3 }, { v: undefined }, { v: 1 }, { v: NaN }, { v: 2 }]
    expect(sortRows(rows, (r) => r.v, 1).map((r) => r.v).slice(0, 3)).toEqual([1, 2, 3])
    expect(sortRows(rows, (r) => r.v, -1).map((r) => r.v).slice(0, 3)).toEqual([3, 2, 1])
    expect(sortRows([{ s: '广东' }, { s: '' }, { s: '北京' }], (r) => r.s).map((r) => r.s)).toEqual(['北京', '广东', ''])
  })
})

describe('exports', () => {
  const task = { id: 't1', type: 'ping', target: 'a|b.example', params: {}, owner: 'guest:secret' }
  const raw = [{ agent_id: 'a', agent_name: 'sz', status: 'done', geo: gd, progress: [{ kind: 'reply' }], data: { ip: '1.1.1.1', replies: [reply(0, 12.345)], stats: { sent: 1, received: 1, loss_pct: 0, min_ms: 12.345, avg_ms: 12.345, max_ms: 12.345, stddev_ms: 0 } } }]
  const rows = raw.map((r) => derive(task, r))
  it('writes a Markdown table', () => {
    const md = toMarkdown(task, rows)
    expect(md).toContain('| 节点 | 地域 | 线路 |')
    expect(md).toContain('| sz | 广东 深圳 | 电信 | 1.1.1.1 |')
    // A pipe inside a cell would split the column.
    expect(toMarkdown(task, [{ ...rows[0], agent_name: 'a|b' }])).toContain('| a\\|b | 广东 深圳 |')
  })
  it('writes CSV a spreadsheet opens safely', () => {
    const csv = toCSV(task, [{ ...rows[0], agent_name: '=cmd()', place: 'a,b' }])
    expect(csv.startsWith('﻿节点,')).toBe(true)
    expect(csv).toContain("'=cmd(),\"a,b\"")
    expect(csv).toContain(',12.35,')
  })
  it('writes JSON without the owner or the streamed progress', () => {
    const j = JSON.parse(toJSON(task, raw))
    expect(j.task.owner).toBeUndefined()
    expect(j.results[0].progress).toBeUndefined()
    expect(j.results[0].data.ip).toBe('1.1.1.1')
  })
  it('names the file after the task', () => {
    expect(exportFilename({ id: 't1', type: 'http', target: 'https://www.example.com/a?b=1' }, 'csv')).toBe('http-www.example.com_a_b_1-t1.csv')
    // A shared result has neither the target nor the task id.
    expect(exportFilename({ id: '', type: 'ping', target: '' }, 'png')).toBe('ping-result.png')
  })
})
