// Text exports of a result table: Markdown (to paste into a chat or a doc),
// CSV and JSON. They follow whatever rows they are given, so the caller
// passes the filtered and sorted list it is showing.
import { TYPE_INFO } from './validate.js'

const num = (v, digits = 1) => (v === undefined || v === null || Number.isNaN(v) ? '' : Number(v.toFixed(digits)).toString())
const base = (r) => [r.agent_name, r.place, r.line]

const TABLES = {
  ping: {
    headers: ['节点', '地域', '线路', '响应 IP', 'IP 归属地', '发送', '接收', '丢包 %', '最快 ms', '平均 ms', '最慢 ms', '抖动 ms', 'P95 ms', '状态'],
    row: (r) => [...base(r), r.ip, r.ipGeo, r.stats?.sent ?? '', r.stats?.received ?? '', num(r.stats?.loss_pct), num(r.stats?.min_ms, 2), num(r.stats?.avg_ms, 2), num(r.stats?.max_ms, 2), num(r.stats?.stddev_ms, 2), num(r.p95, 2), r.statusText],
  },
  http: {
    headers: ['节点', '地域', '线路', '响应 IP', 'IP 归属地', '状态码', '协议', 'DNS ms', '连接 ms', 'TLS ms', '首字节 ms', '下载 ms', '总耗时 ms', '大小 B', '状态'],
    row: (r) => [...base(r), r.ip, r.ipGeo, r.last?.status_code || '', r.last?.proto || '', num(r.avg?.dns_ms), num(r.avg?.connect_ms), num(r.avg?.tls_ms), num(r.avg?.ttfb_ms), num(r.avg?.transfer_ms), num(r.avg?.total_ms), r.last?.ok ? r.last.body_bytes : '', r.statusText],
  },
  dns: {
    headers: ['节点', '地域', '线路', 'DNS 服务器', '耗时 ms', 'RCode', '记录', '状态'],
    row: (r) => [...base(r), r.last?.server || '', r.last?.ok ? num(r.last.rtt_ms, 2) : '', r.last?.rcode === 'Success' ? 'NOERROR' : r.last?.rcode || '', (r.last?.answers || []).map((a) => `${a.type} ${a.value}`).join('; '), r.statusText],
  },
  mtr: {
    headers: ['节点', '地域', '线路', '目标 IP', '跳数', '最后一跳', '是否到达', '末跳丢包 %', '末跳平均 ms', '状态'],
    row: (r) => [...base(r), r.ip, r.hopCount || '', r.lastHop?.hosts?.[0] || '', r.reached ? '是' : '否', num(r.lastHop?.loss_pct), r.lastHop?.received ? num(r.lastHop.avg_ms, 2) : '', r.statusText],
  },
}
TABLES.tcping = TABLES.ping

export function tableOf(task, rows) {
  const t = TABLES[task.type] || TABLES.ping
  return { headers: t.headers, rows: rows.map((r) => t.row(r).map((c) => (c === undefined || c === null ? '' : String(c)))) }
}

export function exportTitle(task) {
  return `${TYPE_INFO[task.type]?.title || task.type} · ${task.target}`
}

export function toMarkdown(task, rows) {
  const { headers, rows: body } = tableOf(task, rows)
  const cell = (c) => c.replace(/\|/g, '\\|').replace(/\n/g, ' ') || '-'
  const line = (cells) => `| ${cells.map(cell).join(' | ')} |`
  return [`**${exportTitle(task)}**`, '', line(headers), `| ${headers.map(() => '---').join(' | ')} |`, ...body.map(line), ''].join('\n')
}

export function toCSV(task, rows) {
  const { headers, rows: body } = tableOf(task, rows)
  // A leading = + - @ would be run as a formula by a spreadsheet.
  const cell = (c) => { const v = /^[=+\-@]/.test(c) && Number.isNaN(Number(c)) ? "'" + c : c; return /[",\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v }
  return '﻿' + [headers, ...body].map((r) => r.map(cell).join(',')).join('\r\n') + '\r\n'
}

// The raw task and results, minus the streamed progress (the final data holds
// the same) and who started it.
export function toJSON(task, results) {
  const { owner, ...t } = task
  return JSON.stringify({ task: t, results: results.map(({ progress, ...r }) => r) }, null, 2)
}

export function download(filename, text, mime) {
  const url = URL.createObjectURL(new Blob([text], { type: mime }))
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

export function exportFilename(task, ext) {
  const safe = String(task.target).replace(/^https?:\/\//, '').replace(/[^\w.-]+/g, '_').replace(/^_+|_+$/g, '').slice(0, 60) || 'result'
  return `${task.type}-${safe}-${task.id}.${ext}`
}
