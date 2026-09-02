import { Fragment, useEffect, useState } from 'react'
import {
  api,
  errorText,
  type Column,
  type ColumnMapping,
  type PreviewCell,
  type Transform,
} from '../lib/api'
import { useSession } from '../store/session'
import {
  Badge,
  Button,
  Card,
  Checkbox,
  Field,
  Input,
  Notice,
  Screen,
  Select,
} from '../components/ui'

const emptyTransform: Transform = {
  trim: true,
  blankAsNull: true,
  upperCase: false,
  lowerCase: false,
  dateFormat: '',
  boolMap: null,
  valueMap: null,
  defaultOnBlank: '',
  stripNonDigits: false,
}

export default function Mapping() {
  const { table, sheet, preview, mappings, mappingStatus, setMappings, setMappingStatus, setPK, unlock, goTo } =
    useSession()
  const [error, setError] = useState('')
  const [editing, setEditing] = useState<string | null>(null)

  useEffect(() => {
    // A configuration can be loaded before the workbook, in which case the
    // status was taken with nothing to compare against. Re-check it now.
    if (mappings.length > 0 && sheet && mappingStatus?.sheetPending) {
      api
        .setMappings(mappings)
        .then((status) => {
          setMappingStatus(status)
          if (!status.blocking) unlock('primaryKey')
        })
        .catch((err) => setError(errorText(err)))
      return
    }

    // Auto-match on first entry only: re-running it would discard the
    // operator's corrections every time they come back to this screen.
    if (mappings.length > 0 || !table || !sheet) return
    api
      .autoMatch()
      .then((res) => {
        setMappings(res.mappings ?? [])
        setMappingStatus(res.status)
        if (!res.status.blocking) unlock('primaryKey')
      })
      .catch((err) => setError(errorText(err)))
  }, [table, sheet, mappings, mappingStatus?.sheetPending, setMappings, setMappingStatus, unlock])

  if (!table || !sheet) {
    return (
      <Screen title="Map the columns">
        <Notice tone="info" title="Choose a table and a sheet first" />
      </Screen>
    )
  }

  async function push(next: ColumnMapping[]) {
    setMappings(next)
    try {
      const status = await api.setMappings(next)
      setMappingStatus(status)
      if (!status.blocking) unlock('primaryKey')
    } catch (err) {
      setError(errorText(err))
    }
  }

  function mappingFor(header: string): ColumnMapping | undefined {
    return mappings.find((m) => m.excelColumn === header)
  }

  async function setTarget(header: string, index: number, dbColumn: string) {
    const without = mappings.filter((m) => m.excelColumn !== header && m.dbColumn !== dbColumn)
    if (!dbColumn) {
      await push(without)
      return
    }

    // The engine decides the adjustments from the column's own constraints,
    // so the rule lives in one place. Whether a blank may become NULL depends
    // on nullability, and a default is not permission to write NULL.
    let transform = emptyTransform
    try {
      transform = await api.transformFor(dbColumn)
    } catch {
      // Fall back to the neutral set rather than blocking the mapping.
    }

    await push([
      ...without,
      { excelColumn: header, excelIndex: index, dbColumn, enabled: true, transform },
    ])
  }

  function updateTransform(header: string, transform: Transform) {
    void push(mappings.map((m) => (m.excelColumn === header ? { ...m, transform } : m)))
  }

  async function loadConfig() {
    setError('')
    try {
      const res = await api.importConfig()
      if (!res.config) return
      // The backend has applied the configuration to the session already; the
      // screen shows what it loaded rather than re-deriving anything. The
      // primary key decision is part of the saved setup and has to come back
      // with the mapping, or a reused configuration quietly changes how the
      // key is handled.
      setMappings(res.config.mappings ?? [])
      setMappingStatus(res.status)
      setPK({
        strategy: res.config.primaryKey.strategy,
        columns: res.config.primaryKey.columns,
        emitSetval: res.config.primaryKey.emitSetval,
      })
      if (!res.status.blocking) unlock('primaryKey')
      if (res.warnings && res.warnings.length > 0) {
        setError(res.warnings.map((w) => `${w.message}, ${w.detail}`).join('\n'))
      }
    } catch (err) {
      setError(errorText(err))
    }
  }

  const taken = new Set(mappings.map((m) => m.dbColumn))
  const blocking = mappingStatus?.blocking ?? false

  return (
    <Screen
      title="Map the columns"
      subtitle="Spreadsheet on the left, table on the right. Unmapped columns are simply left out."
      actions={
        <>
          <Button onClick={() => void loadConfig()}>Load configuration…</Button>
          <Button variant="primary" disabled={blocking} onClick={() => goTo('primaryKey')}>
            Primary key
          </Button>
        </>
      }
    >
      {error && <Notice tone="warning" title="Configuration" detail={error} />}

      {mappingStatus && (
        <div className="mb-4 flex flex-wrap gap-2 text-sm">
          <Badge tone="ok">{mappingStatus.mapped} mapped</Badge>
          <Badge>{mappingStatus.unmappedSheet} sheet columns unmapped</Badge>
          <Badge>{mappingStatus.defaultedCols} left to database defaults</Badge>
        </div>
      )}

      {mappingStatus?.problems && mappingStatus.problems.length > 0 && (
        <div className="mb-4 space-y-2">
          {mappingStatus.problems.map((p, i) => (
            <Notice
              key={`${p.code}-${i}`}
              tone={p.severity === 'error' ? 'error' : 'warning'}
              title={`${p.code}, ${p.message}`}
              detail={p.hint}
            />
          ))}
        </div>
      )}

      <Card className="p-0">
        <table className="w-full border-separate border-spacing-0 text-left text-sm">
          {/* The header stays in view: a sheet with eighty columns is eighty
              rows here, and by row forty the column meanings are gone. */}
          <thead className="text-xs uppercase tracking-wide text-slate-500">
            <tr>
              <th className="sticky top-0 z-10 w-[24%] border-b border-slate-200 bg-slate-50 px-3 py-2 font-medium">
                Sheet column
              </th>
              <th className="sticky top-0 z-10 w-[24%] border-b border-slate-200 bg-slate-50 px-3 py-2 font-medium">
                Table column
              </th>
              <th className="sticky top-0 z-10 w-[22%] border-b border-slate-200 bg-slate-50 px-3 py-2 font-medium">
                Type
              </th>
              <th className="sticky top-0 z-10 border-b border-slate-200 bg-slate-50 px-3 py-2 font-medium">
                Adjustments
              </th>
            </tr>
          </thead>
          <tbody>
            {sheet.headers.map((header, index) => {
              const mapping = mappingFor(header)
              const column = table.schema.columns.find((c) => c.name === mapping?.dbColumn)
              const sample = sampleFor(preview, index)
              return (
                <Fragment key={header}>
                  <tr className="border-b border-slate-100">
                    <td className="px-3 py-2 align-top">
                      <div className="font-mono text-xs text-slate-900">{header}</div>
                      {/* One sample value: what catches the failure a name
                          check cannot, a column called "Phone" full of dates. */}
                      {sample && (
                        <div
                          className="mt-0.5 truncate font-mono text-[11px] text-slate-400"
                          title={sample}
                        >
                          {sample}
                        </div>
                      )}
                    </td>
                    <td className="px-3 py-2">
                      <Select
                        value={mapping?.dbColumn ?? ''}
                        onChange={(e) => void setTarget(header, index, e.target.value)}
                      >
                        <option value=""> not mapped </option>
                        {table.schema.columns.map((c) => (
                          <option
                            key={c.name}
                            value={c.name}
                            disabled={!acceptsValue(c) || (taken.has(c.name) && c.name !== mapping?.dbColumn)}
                          >
                            {c.name}
                            {!acceptsValue(c) ? ' (database supplies this)' : ''}
                          </option>
                        ))}
                      </Select>
                    </td>
                    <td className="px-3 py-2 font-mono text-xs text-slate-600">
                      {column?.formattedType ?? ''}
                      {column && isRequired(column) && (
                        <span className="ml-1">
                          <Badge tone="warning">required</Badge>
                        </span>
                      )}
                    </td>
                    <td className="px-3 py-2">
                      {mapping && (
                        <button
                          onClick={() => setEditing(editing === header ? null : header)}
                          className="text-xs text-slate-600 underline decoration-dotted hover:text-slate-900"
                        >
                          {describeTransform(mapping.transform)}
                        </button>
                      )}
                    </td>
                  </tr>
                  {mapping && editing === header && (
                    <tr className="border-b border-slate-100 bg-slate-50">
                      <td colSpan={4} className="px-3 py-3">
                        <TransformEditor
                          column={column}
                          transform={mapping.transform}
                          onChange={(t) => updateTransform(header, t)}
                        />
                      </td>
                    </tr>
                  )}
                </Fragment>
              )
            })}
          </tbody>
        </table>
      </Card>
    </Screen>
  )
}

function TransformEditor({
  column,
  transform,
  onChange,
}: {
  column?: Column
  transform: Transform
  onChange: (t: Transform) => void
}) {
  const set = <K extends keyof Transform>(key: K, value: Transform[K]) =>
    onChange({ ...transform, [key]: value })

  const isDate = column?.dataType === 'date' || column?.dataType?.startsWith('timestamp')
  const isEnum = (column?.enumValues?.length ?? 0) > 0

  return (
    <div className="grid grid-cols-3 gap-4">
      <div className="space-y-2">
        <Checkbox label="Trim whitespace" checked={transform.trim} onChange={(v) => set('trim', v)} />
        <Checkbox
          label="Treat blanks as NULL"
          checked={transform.blankAsNull}
          onChange={(v) => set('blankAsNull', v)}
          hint={column && isRequired(column) ? 'This column is required, a NULL will fail.' : undefined}
        />
        <Checkbox
          label="Keep digits only"
          checked={transform.stripNonDigits}
          onChange={(v) => set('stripNonDigits', v)}
          hint="For phone numbers written with spaces and dashes."
        />
      </div>

      <div className="space-y-2">
        <Checkbox
          label="Uppercase"
          checked={transform.upperCase}
          onChange={(v) => onChange({ ...transform, upperCase: v, lowerCase: v ? false : transform.lowerCase })}
        />
        <Checkbox
          label="Lowercase"
          checked={transform.lowerCase}
          onChange={(v) => onChange({ ...transform, lowerCase: v, upperCase: v ? false : transform.upperCase })}
        />
        <Field label="Value for blank cells">
          <Input
            value={transform.defaultOnBlank}
            onChange={(e) => set('defaultOnBlank', e.target.value)}
            placeholder="leave empty for none"
          />
        </Field>
      </div>

      <div className="space-y-2">
        {isDate && (
          <Field
            label="Date format"
            hint="Go layout, e.g. 02/01/2006 for day/month/year. Only used when the cell holds text."
          >
            <Input
              value={transform.dateFormat}
              onChange={(e) => set('dateFormat', e.target.value)}
              placeholder="02/01/2006"
            />
          </Field>
        )}
        {isEnum && (
          <Field label="Accepted values">
            <p className="font-mono text-xs text-slate-600 selectable">
              {column?.enumValues?.join(', ')}
            </p>
          </Field>
        )}
        {!isDate && !isEnum && (
          <p className="text-xs text-slate-500">
            Adjustments run in a fixed order: digits, trim, blanks, defaults, case, value mapping.
          </p>
        )}
      </div>
    </div>
  )
}

/** The first non-empty value from the preview rows, for one column. */
function sampleFor(preview: PreviewCell[][], index: number): string {
  for (const row of preview) {
    const cell = row[index]
    if (cell?.text) return cell.text
  }
  return ''
}

function acceptsValue(c: Column): boolean {
  return !c.isGenerated && c.identityKind !== 'ALWAYS'
}

function isRequired(c: Column): boolean {
  return !c.nullable && !c.hasDefault && !c.isIdentity && !c.isGenerated
}

function describeTransform(t: Transform): string {
  const parts: string[] = []
  if (t.stripNonDigits) parts.push('digits only')
  if (t.trim) parts.push('trim')
  if (t.blankAsNull) parts.push('blank → NULL')
  if (t.defaultOnBlank) parts.push(`blank → "${t.defaultOnBlank}"`)
  if (t.upperCase) parts.push('uppercase')
  if (t.lowerCase) parts.push('lowercase')
  if (t.dateFormat) parts.push(t.dateFormat)
  if (t.valueMap && Object.keys(t.valueMap).length > 0) parts.push('value map')
  return parts.length > 0 ? parts.join(', ') : 'no adjustments'
}
