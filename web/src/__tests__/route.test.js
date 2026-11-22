import { describe, it, expect } from 'vitest'
import { parse, taskPath } from '../route.js'
import { parseSSE } from '../api.js'

describe('parse', () => {
  it('maps probe type paths', () => {
    expect(parse('/ping')).toMatchObject({ page: 'probe', type: 'ping', taskId: '' })
    expect(parse('/tcping/')).toMatchObject({ page: 'probe', type: 'tcping' })
    expect(parse('/http', '?target=example.com')).toMatchObject({ page: 'probe', type: 'http', query: { target: 'example.com' } })
    expect(parse('/http3', '?target=example.com')).toMatchObject({ page: 'probe', type: 'http', query: { target: 'example.com', http3: '1' } })
  })
  it('maps task links', () => {
    expect(parse('/t/mabc123')).toMatchObject({ page: 'probe', type: '', taskId: 'mabc123' })
    expect(parse(taskPath('a b'))).toMatchObject({ taskId: 'a b' })
  })
  it('maps share links', () => {
    expect(parse('/s/abc123')).toMatchObject({ page: 'share', token: 'abc123' })
    expect(parse('/s')).toMatchObject({ page: 'probe' })
  })
  it('maps the other pages', () => {
    for (const p of ['ip', 'coverage', 'dns-servers', 'dns-global', 'monitors', 'agents', 'history', 'notify', 'settings']) expect(parse('/' + p).page).toBe(p)
    expect(parse('/monitors', '?id=m1').query.id).toBe('m1')
  })
  it('falls back to the probe page', () => {
    for (const p of ['/', '', '/nope', '/t', '/t/a/b', '/ping/extra', '/history/x']) {
      expect(parse(p)).toMatchObject({ page: 'probe', type: '', taskId: '' })
    }
  })
})

describe('parseSSE', () => {
  it('reads an event block', () => {
    expect(parseSSE('event: row\ndata: {"row":{"id":"rec:1.1.1.1"}}')).toEqual({ type: 'row', data: { row: { id: 'rec:1.1.1.1' } } })
    expect(parseSSE('data: {"a":1}')).toEqual({ type: 'message', data: { a: 1 } })
  })
  it('ignores comments and broken blocks', () => {
    expect(parseSSE(': keep-alive')).toBeNull()
    expect(parseSSE('event: row\ndata: {not json')).toBeNull()
  })
})
