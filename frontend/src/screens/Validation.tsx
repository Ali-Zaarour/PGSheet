import { useState } from 'react'
import { api, errorText, painted, type ColumnVerdict } from '../lib/api'
import { useSession } from '../store/session'
import { Button, Card, Checkbox, Empty, Notice, Screen, Stat } from '../components/ui'
import IssueTable from '../components/IssueTable'

/**
 * One line per mapped column: did this one come through clean?
 *
 * The issue list answers "what is wrong"; it does not answer "what did you
 * check, and what passed". Without that, an operator reading a report of forty
 * problems cannot tell whether the other twenty columns were examined at all.
 */
function ColumnResults({
  verdicts,
  byColumn,
}: {
  verdicts: ColumnVerdict[]
  byColumn: Record<string, number> | null
}) {
  const clean = verdicts.filter((v) => !v.blocked && !(byColumn?.[v.excelColumn] ?? 0)).length

  return (
    <Card className="p-0">
      <div className="flex items-baseline justify-between border-b border-slate-200 px-3 py-2">
        <h2 className="text-sm font-medium text-slate-800">Columns</h2>
        <span className="text-xs text-slate-500">
          {clean} of {verdicts.length} with no problems
        </span>
      </div>

      <ul className="max-h-64 overflow-auto">
        {verdicts.map((v) => {
          const issues = byColumn?.[v.excelColumn] ?? 0
          const state = v.blocked ? 'blocked' : issues > 0 ? 'issues' : 'clean'

          return (
            <li
              key={`${v.excelColumn}-${v.dbColumn}`}
              className="flex items-start gap-2 border-b border-slate-100 px-3 py-1.5 last:border-0"
            >
              <Mark state={state} />
              <div className="min-w-0 flex-1">
                <div className="flex items-baseline gap-2">
                  <span className="truncate font-mono text-xs text-slate-900">{v.excelColumn}</span>
                  <span className="truncate text-xs text-slate-400">→ {v.dbColumn}</span>
                </div>
                {v.blocked ? (
                  <p className="text-xs text-severity-error">{v.reason}</p>
                ) : issues > 0 ? (
                  <p className="text-xs text-severity-warning">
                    {issues.toLocaleString()} {issues === 1 ? 'problem' : 'problems'} in this column
                  </p>
                ) : (
                  <p className="text-xs text-slate-500">
                    {v.sampled.toLocaleString()} values checked, all accepted
                  </p>
                )}
              </div>
            </li>
          )
        })}
      </ul>
    </Card>
  )
}

function Mark({ state }: { state: 'clean' | 'issues' | 'blocked' }) {
  const style = {
    clean: 'border-emerald-300 bg-emerald-50 text-emerald-700',
    issues: 'border-amber-300 bg-amber-50 text-severity-warning',
    blocked: 'border-red-300 bg-red-50 text-severity-error',
  }[state]

  return (
    <span
      className={`mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded border text-[10px] leading-none ${style}`}
      aria-label={state}
    >
      {state === 'clean' ? '✓' : state === 'issues' ? '!' : '×'}
    </span>
  )
}

export default function Validation() {
  const {
    table,
    report,
    dryRunReport,
    setReport,
    setDryRunReport,
    progress,
    busy,
    setBusy,
    unlock,
    goTo,
  } = useSession()

  const [error, setError] = useState('')
  const [checkExisting, setCheckExisting] = useState(true)
  const [checkReferences, setCheckReferences] = useState(false)
  const [exported, setExported] = useState('')

  const canInsert = table?.privileges.canInsert ?? false

  async function validate() {
    setError('')
    setExported('')
    setBusy('Validating')
    await painted()

    try {
      await api.setValidationOptions(
        {
          MaxIssues: 10000,
          ColumnMisalignThreshold: 0.3,
          CheckUniqueAgainstDB: checkExisting,
          CheckForeignKeys: checkReferences,
          SkipBlankRows: true,
        },
        '',
        false,
        false,
      )
      const rep = await api.validate()
      setReport(rep)
      if (rep.errorCount === 0) unlock('generate')
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  async function dryRun() {
    setError('')
    setBusy('Verifying against the database')
    await painted()

    try {
      setDryRunReport(await api.dryRun())
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  async function exportIssues() {
    setError('')
    try {
      const path = await api.exportIssues()
      if (path) setExported(path)
    } catch (err) {
      setError(errorText(err))
    }
  }

  return (
    <Screen
      title="Validate"
      subtitle="Every cell is checked against the table's real rules before anything is written."
      actions={
        <>
          <Button onClick={() => void validate()} disabled={busy !== null}>
            {report ? 'Re-run validation' : 'Run validation'}
          </Button>
          {report && report.errorCount === 0 && (
            <Button variant="primary" onClick={() => goTo('generate')}>
              Generate the file
            </Button>
          )}
        </>
      }
    >
      {error && <Notice tone="error" title="Validation could not finish" detail={error} />}

      <Card className="mb-4">
        <div className="grid grid-cols-2 gap-3">
          <Checkbox
            label="Check unique values against the table"
            checked={checkExisting}
            onChange={setCheckExisting}
            hint="Finds rows that are already in the database. Costs one query per thousand keys."
          />
          <Checkbox
            label="Check foreign key values"
            checked={checkReferences}
            onChange={setCheckReferences}
            hint="Confirms every referenced row exists before the import runs."
          />
        </div>
      </Card>

      {busy && (
        <Card className="mb-4">
          <p className="text-sm text-slate-700">
            {busy}
            {progress && progress.total > 0
              ? `: ${progress.phase}, row ${progress.current.toLocaleString()} of ${progress.total.toLocaleString()}`
              : progress && progress.current > 0
                ? `: ${progress.phase}, ${progress.current.toLocaleString()} rows`
                : '…'}
          </p>
          <div className="mt-2 h-1.5 w-full overflow-hidden rounded bg-slate-100">
            <div
              className="h-full bg-slate-900 transition-all"
              style={{
                width:
                  progress && progress.total > 0
                    ? `${Math.min(100, (progress.current / progress.total) * 100)}%`
                    : '30%',
              }}
            />
          </div>
          <div className="mt-2">
            <Button variant="ghost" onClick={() => void api.cancel()}>
              Cancel
            </Button>
          </div>
        </Card>
      )}

      {!report && !busy && (
        <Empty>Run validation to check the file against {table?.schema.table ?? 'the table'}.</Empty>
      )}

      {report && (
        <>
          <div className="mb-4 grid grid-cols-4 gap-3">
            <Stat label="Rows" value={report.rowsTotal.toLocaleString()} />
            <Stat label="Valid" value={report.rowsValid.toLocaleString()} tone="ok" />
            <Stat
              label="Errors"
              value={report.errorCount.toLocaleString()}
              tone={report.errorCount > 0 ? 'error' : undefined}
            />
            <Stat
              label="Warnings"
              value={report.warningCount.toLocaleString()}
              tone={report.warningCount > 0 ? 'warning' : undefined}
            />
          </div>

          {report.errorCount === 0 ? (
            <div className="mb-4">
              <Notice
                tone="ok"
                title="No blocking problems"
                detail={`Checked in ${report.duration}. ${report.rowsSkipped} blank rows were skipped.`}
              />
            </div>
          ) : (
            <div className="mb-4">
              <Notice
                tone="error"
                title={`${report.errorCount.toLocaleString()} problems block generation`}
                detail={
                  report.truncated
                    ? 'The list below is truncated. Fix the column-level problems first, they usually explain the rest.'
                    : undefined
                }
              />
            </div>
          )}

          {report.columnVerdicts && report.columnVerdicts.length > 0 && (
            <div className="mb-4">
              <ColumnResults verdicts={report.columnVerdicts} byColumn={report.byColumn} />
            </div>
          )}

          {report.unverifiable && report.unverifiable.length > 0 && (
            <div className="mb-4">
              <Notice
                tone="info"
                title={`${report.unverifiable.length} constraint(s) can only be checked by the database`}
                detail="Run the live verification below to test them exactly as the database would."
              />
            </div>
          )}

          <div className="mb-2 flex items-center gap-2">
            <Button onClick={() => void exportIssues()} disabled={(report.issues?.length ?? 0) === 0}>
              Export issues…
            </Button>
            <Button onClick={() => void dryRun()} disabled={!canInsert || busy !== null}>
              Run live verification
            </Button>
            {!canInsert && (
              <span className="text-xs text-slate-500">
                Live verification needs INSERT privilege on the table.
              </span>
            )}
            {exported && <span className="text-xs text-emerald-700">Written to {exported}</span>}
          </div>

          {report.issues && report.issues.length > 0 ? (
            <IssueTable issues={report.issues} />
          ) : (
            <Empty>Nothing to report.</Empty>
          )}

          {dryRunReport && (
            <div className="mt-4">
              <Notice
                tone={dryRunReport.errorCount === 0 ? 'ok' : 'error'}
                title={
                  dryRunReport.errorCount === 0
                    ? 'Live verification passed, the transaction was rolled back'
                    : `Live verification found ${dryRunReport.errorCount} problems`
                }
                detail="Nothing was written. Note that triggers fired, and any sequence values consumed inside the transaction are not returned."
              />
              {dryRunReport.issues && dryRunReport.issues.length > 0 && (
                <div className="mt-2">
                  <IssueTable issues={dryRunReport.issues} />
                </div>
              )}
            </div>
          )}
        </>
      )}
    </Screen>
  )
}
