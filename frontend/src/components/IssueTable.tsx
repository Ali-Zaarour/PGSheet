import { useMemo, useRef, useState } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import type { Issue, Severity } from '../lib/api'
import { Badge, Input, Select } from './ui'

/**
 * The issue list. A report can hold ten thousand rows, so it is virtualized and
 * filtered here over the report already transferred, rather than round-tripping
 * to Go on every keystroke.
 */
export default function IssueTable({ issues }: { issues: Issue[] }) {
  const [severity, setSeverity] = useState<'all' | Severity>('all')
  const [code, setCode] = useState('all')
  const [column, setColumn] = useState('all')
  const [text, setText] = useState('')

  const codes = useMemo(
    () => Array.from(new Set(issues.map((i) => i.code))).sort(),
    [issues],
  )
  const columns = useMemo(
    () => Array.from(new Set(issues.map((i) => i.excelColumn).filter(Boolean))).sort(),
    [issues],
  )

  const filtered = useMemo(() => {
    const q = text.trim().toLowerCase()
    return issues.filter((i) => {
      if (severity !== 'all' && i.severity !== severity) return false
      if (code !== 'all' && i.code !== code) return false
      if (column !== 'all' && i.excelColumn !== column) return false
      if (q && !`${i.message} ${i.value} ${i.dbColumn} ${i.excelRef}`.toLowerCase().includes(q))
        return false
      return true
    })
  }, [issues, severity, code, column, text])

  const parentRef = useRef<HTMLDivElement>(null)
  const virtualizer = useVirtualizer({
    count: filtered.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 56,
    overscan: 12,
  })

  return (
    <div>
      <div className="mb-2 flex flex-wrap items-end gap-2">
        <div className="w-36">
          <Select value={severity} onChange={(e) => setSeverity(e.target.value as 'all' | Severity)}>
            <option value="all">All severities</option>
            <option value="error">Errors</option>
            <option value="warning">Warnings</option>
          </Select>
        </div>
        <div className="w-32">
          <Select value={code} onChange={(e) => setCode(e.target.value)}>
            <option value="all">All codes</option>
            {codes.map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </Select>
        </div>
        <div className="w-52">
          <Select value={column} onChange={(e) => setColumn(e.target.value)}>
            <option value="all">All columns</option>
            {columns.map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </Select>
        </div>
        <div className="flex-1 min-w-[12rem]">
          <Input
            placeholder="Search messages and values…"
            value={text}
            onChange={(e) => setText(e.target.value)}
          />
        </div>
        <span className="pb-1.5 text-xs text-slate-500">
          {filtered.length.toLocaleString()} of {issues.length.toLocaleString()}
        </span>
      </div>

      <div
        ref={parentRef}
        className="h-[26rem] overflow-auto rounded border border-slate-200 bg-white"
      >
        <div style={{ height: virtualizer.getTotalSize(), position: 'relative' }}>
          {virtualizer.getVirtualItems().map((item) => {
            const issue = filtered[item.index]
            return (
              <div
                key={item.key}
                className="absolute left-0 w-full border-b border-slate-100 px-3 py-2"
                style={{ height: item.size, transform: `translateY(${item.start}px)` }}
              >
                <div className="flex items-baseline gap-2">
                  <Badge tone={issue.severity === 'error' ? 'error' : 'warning'}>{issue.code}</Badge>
                  {issue.excelRef ? (
                    // A1 notation, so the operator can paste it into Excel's
                    // name box and land on the cell.
                    <span className="selectable font-mono text-xs text-slate-900">
                      {issue.excelRef}
                    </span>
                  ) : (
                    <span className="text-xs uppercase tracking-wide text-slate-400">
                      {issue.scope}
                    </span>
                  )}
                  {issue.excelColumn && (
                    <span className="text-xs text-slate-500">{issue.excelColumn}</span>
                  )}
                  {issue.dbColumn && (
                    <span className="text-xs text-slate-400">→ {issue.dbColumn}</span>
                  )}
                </div>
                <div className="mt-0.5 truncate text-sm text-slate-800" title={issue.message}>
                  {issue.message}
                  {issue.hint && <span className="ml-2 text-slate-500">{issue.hint}</span>}
                </div>
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}
