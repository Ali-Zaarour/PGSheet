import { useEffect, useMemo, useState } from 'react'
import { api, errorText, painted, type Column, type TableRef } from '../lib/api'
import { useSession } from '../store/session'
import { Badge, Busy, Button, Card, Empty, Input, Notice, Screen, Select } from '../components/ui'

export default function TableSelect() {
  const [tables, setTables] = useState<TableRef[]>([])
  const [filter, setFilter] = useState('')
  const [schema, setSchema] = useState('all')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  // Which table is being read, so the click has visible consequences.
  const [reading, setReading] = useState('')

  const { table, setTable, unlock, goTo } = useSession()

  useEffect(() => {
    let cancelled = false
    api
      .listTables()
      .then((t) => {
        if (!cancelled) setTables(t)
      })
      .catch((err) => {
        if (!cancelled) setError(errorText(err))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  const schemas = useMemo(
    () => Array.from(new Set(tables.map((t) => t.schema))).sort(),
    [tables],
  )

  const filtered = useMemo(() => {
    const q = filter.trim().toLowerCase()
    return tables.filter((t) => {
      if (schema !== 'all' && t.schema !== schema) return false
      if (q && !`${t.schema}.${t.table}`.toLowerCase().includes(q)) return false
      return true
    })
  }, [tables, filter, schema])

  async function choose(ref: TableRef) {
    setError('')
    // Show it, let it paint, then run the introspection queries. Otherwise the
    // window sits still while a wide table is read.
    setReading(`${ref.schema}.${ref.table}`)
    await painted()

    try {
      const detail = await api.describeTable(ref.schema, ref.table)
      setTable(detail)
      unlock('file')
    } catch (err) {
      setError(errorText(err))
    } finally {
      setReading('')
    }
  }

  return (
    <Screen
      title="Choose the target table"
      subtitle="The structure below is read live from the database, every session."
      actions={
        <>
          <Count label="Schemas" value={schemas.length} />
          <Count label="Tables" value={tables.length} />
          {table && (
            <Button variant="primary" onClick={() => goTo('file')}>
              Open a spreadsheet
            </Button>
          )}
        </>
      }
    >
      {error && <Notice tone="error" title="Could not read the schema" detail={error} />}

      <div className="grid h-full grid-cols-[22rem_1fr] gap-4">
        <div className="flex min-h-0 flex-col">
          <div className="space-y-2">
            <Select value={schema} onChange={(e) => setSchema(e.target.value)}>
              <option value="all">All schemas ({schemas.length})</option>
              {schemas.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </Select>
            <Input
              placeholder="Filter tables…"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
          </div>
          <div className="mt-2 min-h-0 flex-1 overflow-auto rounded border border-slate-200 bg-white">
            {loading && (
              <p className="px-3 py-2 text-sm text-slate-500">Reading the table list…</p>
            )}
            {!loading && filtered.length === 0 && (
              <p className="px-3 py-2 text-sm text-slate-500">No tables match.</p>
            )}
            {!loading && filtered.length > 0 && filtered.length !== tables.length && (
              <p className="border-b border-slate-100 px-3 py-1.5 text-xs text-slate-500">
                {filtered.length} of {tables.length}
              </p>
            )}
            {filtered.map((t) => {
              const selected = table?.schema.schema === t.schema && table?.schema.table === t.table
              return (
                <button
                  key={`${t.schema}.${t.table}`}
                  onClick={() => void choose(t)}
                  className={`block w-full border-b border-slate-100 px-3 py-2 text-left last:border-0 ${
                    selected ? 'bg-slate-900 text-white' : 'hover:bg-slate-50'
                  }`}
                >
                  <div className="text-sm">
                    <span className={selected ? 'opacity-70' : 'text-slate-500'}>{t.schema}.</span>
                    {t.table}
                  </div>
                  {t.estRows > 0 && (
                    <div className={`text-xs ${selected ? 'opacity-70' : 'text-slate-500'}`}>
                      ~{t.estRows.toLocaleString()} rows
                    </div>
                  )}
                </button>
              )
            })}
          </div>
        </div>

        <div className="min-h-0 overflow-auto">
          {reading ? (
            <Busy label={`Reading ${reading}`} detail="Columns, constraints and sequences." />
          ) : table ? (
            <TableDetailPanel />
          ) : (
            <Empty>Select a table to see its structure.</Empty>
          )}
        </div>
      </div>
    </Screen>
  )
}

/** A count in the header: how much there is to choose from. */
function Count({ label, value }: { label: string; value: number }) {
  return (
    <div className="flex items-baseline gap-2 whitespace-nowrap rounded border border-slate-200 bg-white px-3 py-1.5">
      <span className="text-xs uppercase tracking-wide text-slate-500">{label}</span>
      <span className="text-sm font-semibold tabular-nums text-slate-900">
        {value.toLocaleString()}
      </span>
    </div>
  )
}

function TableDetailPanel() {
  const table = useSession((s) => s.table)!
  const { schema, privileges, unverifiable } = table

  return (
    <div className="space-y-4">
      <Card>
        <div className="mb-3 flex items-baseline justify-between">
          <h2 className="font-semibold text-slate-900">
            {schema.schema}.{schema.table}
          </h2>
          <span className="text-xs text-slate-500">
            {schema.columns.length} columns · ~{schema.rowCount.toLocaleString()} rows
          </span>
        </div>

        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead className="border-b border-slate-200 text-xs uppercase tracking-wide text-slate-500">
              <tr>
                <th className="py-1.5 pr-3 font-medium">Column</th>
                <th className="py-1.5 pr-3 font-medium">Type</th>
                <th className="py-1.5 pr-3 font-medium">Required</th>
                <th className="py-1.5 font-medium">Notes</th>
              </tr>
            </thead>
            <tbody>
              {schema.columns.map((c) => (
                <tr key={c.name} className="border-b border-slate-100 last:border-0">
                  <td className="py-1.5 pr-3 font-mono text-xs text-slate-900">{c.name}</td>
                  <td className="py-1.5 pr-3 font-mono text-xs text-slate-600">{c.formattedType}</td>
                  <td className="py-1.5 pr-3">
                    {required(c) ? <Badge tone="warning">required</Badge> : <span className="text-slate-400">no</span>}
                  </td>
                  <td className="py-1.5 text-xs text-slate-600">{columnNotes(c)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>

      {schema.constraints.length > 0 && (
        <Card>
          <h3 className="mb-2 font-semibold text-slate-900">Constraints</h3>
          <ul className="space-y-1.5">
            {schema.constraints.map((c) => (
              <li key={c.name} className="text-sm">
                <Badge tone={c.type === 'p' ? 'info' : 'neutral'}>{constraintKind(c.type)}</Badge>{' '}
                <span className="font-mono text-xs text-slate-700">{c.name}</span>
                <div className="ml-1 font-mono text-xs text-slate-500 selectable">{c.definition}</div>
              </li>
            ))}
          </ul>
        </Card>
      )}

      {schema.pkSequence && (
        <Card>
          <h3 className="mb-1 font-semibold text-slate-900">Primary key sequence</h3>
          <p className="text-sm text-slate-600">
            <span className="font-mono text-xs">{schema.pkSequence.name}</span>, the next value is
            around <strong>{schema.pkSequence.nextValue.toLocaleString()}</strong>. That is an
            estimate, not a reservation: the database assigns the real values when the generated file
            runs.
          </p>
        </Card>
      )}

      {!privileges.canInsert && (
        <Notice
          tone="info"
          title="This user cannot insert into the table"
          detail="Validation and file generation work normally. Live verification and direct execution are unavailable."
        />
      )}

      {unverifiable && unverifiable.length > 0 && (
        <Notice
          tone="warning"
          title={`${unverifiable.length} constraint(s) cannot be checked offline`}
          detail="These are evaluated by the database only. Use live verification before generating."
        >
          <ul className="mt-2 space-y-1">
            {unverifiable.map((u) => (
              <li key={u.constraint} className="font-mono text-xs selectable">
                {u.constraint}: {u.definition}
              </li>
            ))}
          </ul>
        </Notice>
      )}
    </div>
  )
}

function required(c: Column): boolean {
  return !c.nullable && !c.hasDefault && !c.isIdentity && !c.isGenerated
}

function columnNotes(c: Column): string {
  const notes: string[] = []
  if (c.identityKind === 'ALWAYS') notes.push('generated always, cannot be mapped')
  else if (c.isIdentity) notes.push('identity')
  if (c.isGenerated) notes.push('computed, cannot be mapped')
  if (c.hasDefault && c.defaultExpr) notes.push(`default ${c.defaultExpr}`)
  if (c.enumValues && c.enumValues.length > 0) notes.push(`one of: ${c.enumValues.join(', ')}`)
  if (c.comment) notes.push(c.comment)
  return notes.join(' · ')
}

function constraintKind(type: string): string {
  switch (type) {
    case 'p':
      return 'primary key'
    case 'u':
      return 'unique'
    case 'c':
      return 'check'
    case 'f':
      return 'foreign key'
    case 'x':
      return 'exclusion'
    default:
      return type
  }
}
