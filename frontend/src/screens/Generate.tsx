import { useState } from 'react'
import {
  api,
  errorText,
  painted,
  type ExecResult,
  type GenerateOptions,
  type GenerateResult,
} from '../lib/api'
import { useSession } from '../store/session'
import { Button, Card, Checkbox, Field, Input, Notice, Screen, Select } from '../components/ui'

const defaults: GenerateOptions = {
  mode: 'insert',
  batchSize: 500,
  wrapInTransaction: true,
  includeSummaryHeader: true,
  skipBlankRows: true,
  statementTimeout: '',
  path: '',
}

export default function Generate() {
  const { report, table, sheet, busy, setBusy } = useSession()
  const [options, setOptions] = useState<GenerateOptions>(defaults)
  const [result, setResult] = useState<GenerateResult | null>(null)
  const [configPath, setConfigPath] = useState('')
  const [error, setError] = useState('')
  const [confirmName, setConfirmName] = useState('')
  const [execResult, setExecResult] = useState<ExecResult | null>(null)

  const largeFile = (sheet?.totalRows ?? 0) > 50000

  const set = <K extends keyof GenerateOptions>(key: K, value: GenerateOptions[K]) =>
    setOptions((o) => ({ ...o, [key]: value }))

  async function generate() {
    setError('')
    setResult(null)
    setBusy('Generating')
    await painted()

    try {
      setResult(await api.generate(options))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  async function executeDirect() {
    setError('')
    setExecResult(null)
    setBusy('Inserting')
    await painted()

    try {
      setExecResult(await api.executeDirect(options, confirmName))
      setConfirmName('')
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  async function exportConfig() {
    setError('')
    try {
      const path = await api.exportConfig()
      if (path) setConfigPath(path)
    } catch (err) {
      setError(errorText(err))
    }
  }

  if (!report || report.errorCount > 0) {
    return (
      <Screen
        title="Generate the file"
        actions={<Button onClick={() => void exportConfig()}>Export configuration…</Button>}
      >
        <Notice
          tone="info"
          title="Validation has to pass before a file can be written"
          detail={
            report
              ? `${report.errorCount.toLocaleString()} problems still block generation. Fix them on the validation step and run it again.`
              : 'Run validation first.'
          }
        />

        {/* The mapping is worth saving whether or not the data is clean — it
            is the work being reused next month, and a file that failed
            validation is exactly when an operator wants to keep the setup and
            come back to it. */}
        <div className="mt-4">
          <Card>
            <p className="text-sm font-medium text-slate-800">Save the setup for next time</p>
            <p className="mt-0.5 text-sm text-slate-600">
              The configuration holds the sheet settings, the mapping, the adjustments and the
              primary key decision, everything except the connection. Loading it next month
              rebuilds all of it in one action.
            </p>
            {configPath && (
              <p className="mt-2 text-sm text-emerald-700 selectable">Saved to {configPath}</p>
            )}
            {error && <p className="mt-2 text-sm text-severity-error selectable">{error}</p>}
          </Card>
        </div>
      </Screen>
    )
  }

  return (
    <Screen
      title="Generate the file"
      subtitle={`${report.rowsValid.toLocaleString()} rows into ${table?.schema.schema}.${table?.schema.table}`}
      actions={
        <>
          <Button onClick={() => void exportConfig()}>Export configuration…</Button>
          <Button variant="primary" onClick={() => void generate()} disabled={busy !== null}>
            {busy ? 'Generating…' : 'Save .sql…'}
          </Button>
        </>
      }
    >
      {error && <Notice tone="error" title="Could not generate the file" detail={error} />}

      <div className="grid grid-cols-2 gap-4">
        <Card>
          <h2 className="mb-3 font-medium text-slate-900">Output</h2>
          <div className="space-y-3">
            <Field
              label="Format"
              hint={
                options.mode === 'copy'
                  ? 'COPY loads roughly ten times faster and is much harder to read.'
                  : 'One INSERT per batch: readable, reviewable, and diffable against the sheet.'
              }
            >
              <Select
                value={options.mode}
                onChange={(e) => set('mode', e.target.value as 'insert' | 'copy')}
              >
                <option value="insert">INSERT statements</option>
                <option value="copy">COPY from stdin</option>
              </Select>
            </Field>

            {largeFile && options.mode === 'insert' && (
              <Notice
                tone="info"
                title="This is a large file"
                detail="Above about 50,000 rows, COPY is dramatically faster to load."
              />
            )}

            {options.mode === 'insert' && (
              <Field label="Rows per statement" hint="Between 100 and 1000.">
                <Input
                  type="number"
                  min={100}
                  max={1000}
                  value={options.batchSize}
                  onChange={(e) => set('batchSize', Number(e.target.value))}
                />
              </Field>
            )}

            <Checkbox
              label="Wrap in a transaction"
              checked={options.wrapInTransaction}
              onChange={(v) => set('wrapInTransaction', v)}
              hint="All or nothing: a failure part-way leaves the table exactly as it was."
            />
            <Checkbox
              label="Include the summary header"
              checked={options.includeSummaryHeader}
              onChange={(v) => set('includeSummaryHeader', v)}
              hint="Records what was imported, from where, and what was checked."
            />
            <Checkbox
              label="Skip blank rows"
              checked={options.skipBlankRows}
              onChange={(v) => set('skipBlankRows', v)}
            />
          </div>
        </Card>

        <Card>
          <h2 className="mb-3 font-medium text-slate-900">What will be written</h2>
          <dl className="grid grid-cols-[9rem_1fr] gap-y-2 text-sm">
            <dt className="text-slate-500">Rows</dt>
            <dd>{report.rowsValid.toLocaleString()}</dd>
            <dt className="text-slate-500">Blank rows skipped</dt>
            <dd>{report.rowsSkipped.toLocaleString()}</dd>
            <dt className="text-slate-500">Warnings recorded</dt>
            <dd>{report.warningCount.toLocaleString()}</dd>
            <dt className="text-slate-500">Source</dt>
            <dd className="selectable">{sheet?.name}</dd>
          </dl>

          {!options.wrapInTransaction && (
            <div className="mt-3">
              <Notice
                tone="warning"
                title="Without a transaction there is no all-or-nothing"
                detail="A failure part-way through will leave some rows inserted and some not."
              />
            </div>
          )}

          {configPath && (
            <p className="mt-3 text-sm text-emerald-700 selectable">
              Configuration saved to {configPath}
            </p>
          )}
        </Card>
      </div>

      <div className="mt-4">
        <DirectExecute
          tableName={table?.schema.table ?? ''}
          qualified={`${table?.schema.schema}.${table?.schema.table}`}
          canInsert={table?.privileges.canInsert ?? false}
          rows={report.rowsValid}
          confirmName={confirmName}
          setConfirmName={setConfirmName}
          onExecute={() => void executeDirect()}
          busy={busy !== null}
          result={execResult}
        />
      </div>

      {result && (
        <div className="mt-4 space-y-3">
          <Notice
            tone="ok"
            title={`Wrote ${result.rowsWritten.toLocaleString()} rows to ${result.path}`}
            detail={`${result.statements} statement(s), ${formatBytes(result.bytesWritten)}, in ${result.duration}.`}
          />

          {result.personalData && result.personalData.length > 0 && (
            <Notice
              tone="warning"
              title="This file contains personal data"
              detail={`Columns: ${result.personalData.join(', ')}. Treat the file as you would the database itself.`}
            />
          )}

          {result.preview && result.preview.length > 0 && (
            <Card className="p-0">
              <div className="border-b border-slate-200 px-3 py-2 text-xs uppercase tracking-wide text-slate-500">
                First lines of the file
              </div>
              <pre className="selectable max-h-80 overflow-auto p-3 font-mono text-xs leading-relaxed text-slate-800">
                {result.preview.join('\n')}
              </pre>
            </Card>
          )}
        </div>
      )}
    </Screen>
  )
}

/**
 * Direct execution, kept deliberately awkward: it sits below the normal path,
 * says plainly what it will do, and needs the table name typed first. The
 * tool's safety property is that it writes a file, not data.
 */
function DirectExecute({
  tableName,
  qualified,
  canInsert,
  rows,
  confirmName,
  setConfirmName,
  onExecute,
  busy,
  result,
}: {
  tableName: string
  qualified: string
  canInsert: boolean
  rows: number
  confirmName: string
  setConfirmName: (v: string) => void
  onExecute: () => void
  busy: boolean
  result: ExecResult | null
}) {
  const [open, setOpen] = useState(false)

  if (result?.committed) {
    return (
      <Notice
        tone="ok"
        title={`Inserted ${result.rowsInserted.toLocaleString()} rows into ${qualified}`}
        detail={`${result.statements} statement(s) in ${result.duration}. The transaction committed.`}
      />
    )
  }

  if (!open) {
    return (
      <button
        onClick={() => setOpen(true)}
        className="text-xs text-slate-500 underline decoration-dotted hover:text-slate-700"
      >
        Or execute against the database directly…
      </button>
    )
  }

  return (
    <Card className="border-amber-200">
      <h2 className="font-medium text-slate-900">Execute directly</h2>
      <p className="mt-1 text-sm text-slate-600">
        This writes {rows.toLocaleString()} rows into <strong>{qualified}</strong> now, in one
        transaction. Nothing is written if any row fails. There is no undo, generating a file and
        reviewing it first is the safer path, and the reason this is not the default.
      </p>

      {!canInsert ? (
        <div className="mt-3">
          <Notice
            tone="info"
            title="This user cannot insert into the table"
            detail="Generate the file and have someone with the privilege run it."
          />
        </div>
      ) : (
        <div className="mt-3 flex items-end gap-2">
          <div className="w-64">
            {/* Shown and compared in lower case, so what is asked for is
                exactly what is accepted. preserveCase keeps the label styling
                from upper-casing the name it is asking to be typed. */}
            <Field label={`Type "${tableName.toLowerCase()}" to confirm`} preserveCase>
              <Input
                value={confirmName}
                onChange={(e) => setConfirmName(e.target.value)}
                placeholder={tableName.toLowerCase()}
              />
            </Field>
          </div>
          <Button
            variant="danger"
            disabled={busy || confirmName.trim().toLowerCase() !== tableName.toLowerCase()}
            onClick={onExecute}
          >
            {busy ? 'Inserting…' : 'Insert now'}
          </Button>
          <Button variant="ghost" onClick={() => setOpen(false)}>
            Cancel
          </Button>
        </div>
      )}
    </Card>
  )
}

function formatBytes(n: number): string {
  if (n > 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)}MB`
  if (n > 1024) return `${(n / 1024).toFixed(1)}KB`
  return `${n}B`
}
