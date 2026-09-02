import { useState } from 'react'
import { api, errorText, painted, type PreviewCell, type SheetSelection } from '../lib/api'
import { useSession } from '../store/session'
import {
  Badge,
  Busy,
  Button,
  Card,
  Empty,
  Field,
  Input,
  Notice,
  Screen,
  Select,
} from '../components/ui'

export default function FileSelect() {
  const [error, setError] = useState('')
  const [selection, setSelection] = useState<SheetSelection | null>(null)
  const [sheetName, setSheetName] = useState('')
  const [headerRow, setHeaderRow] = useState(1)
  // What the app is doing, in the operator's words. Null when idle.
  const [busy, setBusy] = useState<string | null>(null)

  const { workbook, setWorkbook, setSheet, setPreview, setMappingStatus, progress, unlock, goTo } =
    useSession()

  async function open() {
    setError('')
    try {
      // The dialog first, on its own. Until it closes the operator is looking
      // at it, not at us.
      const path = await api.chooseWorkbook()
      if (!path) return // cancelled

      // Show what is happening, wait for it to be on screen, and only then
      // start the slow part: opening a workbook parses the whole file.
      setBusy('Opening the workbook')
      await painted()

      const wb = await api.openWorkbookAt(path)
      setWorkbook(wb)
      setSelection(null)
      setSheetName(wb.sheets[0] ?? '')
      setHeaderRow(wb.headerGuess.row)
      await load(wb.sheets[0] ?? '', wb.headerGuess.row)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  async function load(name: string, row: number) {
    if (!name) return
    setError('')
    setBusy('Reading the sheet')
    await painted()

    try {
      const sel = await api.selectSheet(name, row)
      setSelection(sel)
      setSheet(sel.info)
      setPreview(sel.preview)
      // A configuration loaded before the workbook is only really checked now.
      if (sel.status) setMappingStatus(sel.status)
      unlock('mapping')
    } catch (err) {
      setError(errorText(err))
      setSelection(null)
    } finally {
      setBusy(null)
    }
  }

  return (
    <Screen
      title="Open the spreadsheet"
      subtitle="Confirm the sheet and which row holds the headers."
      actions={
        <>
          <Button onClick={() => void open()} disabled={busy !== null}>
            {workbook ? 'Open another file' : 'Open file…'}
          </Button>
          {selection && (
            <Button variant="primary" disabled={busy !== null} onClick={() => goTo('mapping')}>
              Map the columns
            </Button>
          )}
        </>
      }
    >
      {error && <Notice tone="error" title="Could not read the workbook" detail={error} />}

      {!workbook && !busy && (
        <Empty>
          No file open. The native file dialog is used so the tool has a real path to stream from.
        </Empty>
      )}

      {busy && (
        <div className="mb-4">
          <Busy
            label={busy}
            // Counting rows has no total until it finishes, so the count as it
            // climbs is the reassurance.
            detail={
              progress && progress.current > 0
                ? `${progress.current.toLocaleString()} rows so far`
                : 'This can take a moment on a large file.'
            }
            onCancel={() => void api.cancel()}
          />
        </div>
      )}

      {workbook && (
        <>
          <Card className="mb-4">
            <div className="mb-3 flex items-baseline justify-between">
              <h2 className="font-medium text-slate-900 selectable">{workbook.fileName}</h2>
              {!workbook.headerGuess.confident && !busy && (
                <Badge tone="warning">header row is a guess, please confirm</Badge>
              )}
            </div>

            <div className="grid grid-cols-[1fr_10rem] gap-3">
              <Field label="Sheet">
                <Select
                  value={sheetName}
                  disabled={busy !== null}
                  onChange={(e) => {
                    setSheetName(e.target.value)
                    void load(e.target.value, headerRow)
                  }}
                >
                  {workbook.sheets.map((s) => (
                    <option key={s} value={s}>
                      {s}
                    </option>
                  ))}
                </Select>
              </Field>

              <Field label="Header row">
                <Input
                  type="number"
                  min={1}
                  value={headerRow}
                  disabled={busy !== null}
                  onChange={(e) => setHeaderRow(Number(e.target.value))}
                  onBlur={() => void load(sheetName, headerRow)}
                />
              </Field>
            </div>

            {selection && !busy && (
              <p className="mt-3 text-sm text-slate-600">
                {selection.info.headers.length} columns, {selection.info.totalRows.toLocaleString()}{' '}
                data rows starting at row {selection.info.dataStart}.
              </p>
            )}
          </Card>

          {selection?.warnings?.map((w) => (
            <div key={w} className="mb-3">
              <Notice tone="warning" title="Worth checking" detail={w} />
            </div>
          ))}

          {selection && !busy && (
            <PreviewGrid headers={selection.info.headers} rows={selection.preview} />
          )}
        </>
      )}
    </Screen>
  )
}

/**
 * The preview shows the kind each cell was read as, not only its text: that is
 * what catches a date column read as numbers, or lost leading zeros.
 *
 * A client sheet can be sixty columns wide, so the grid scrolls in both
 * directions inside its own box rather than stretching the page. The row
 * number and the header stay put while it does.
 */
function PreviewGrid({ headers, rows }: { headers: string[]; rows: PreviewCell[][] }) {
  return (
    <Card className="p-0">
      <div className="max-h-[62vh] overflow-auto">
        <table className="w-full border-separate border-spacing-0 text-left text-sm">
          <thead>
            <tr>
              <th className="sticky left-0 top-0 z-20 w-12 border-b border-r border-slate-200 bg-slate-50 px-2 py-2 text-xs font-normal text-slate-400">
                row
              </th>
              {headers.map((h) => (
                <th
                  key={h}
                  className="sticky top-0 z-10 whitespace-nowrap border-b border-slate-200 bg-slate-50 px-2 py-2 text-xs font-medium text-slate-700"
                  title={h}
                >
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row, i) => (
              <tr key={i}>
                <td className="sticky left-0 z-10 border-b border-r border-slate-100 bg-white px-2 py-1.5 text-xs tabular-nums text-slate-400">
                  {i + 1}
                </td>
                {row.map((cell, j) => (
                  <td
                    key={j}
                    className="max-w-[18rem] truncate border-b border-slate-100 px-2 py-1.5"
                    title={cell.text}
                  >
                    <span className="selectable font-mono text-xs text-slate-800">
                      {cell.text || <span className="text-slate-300">empty</span>}
                    </span>
                    {cell.kind !== 'empty' && cell.kind !== 'text' && (
                      <span className="ml-1 text-[10px] uppercase text-slate-400">{cell.kind}</span>
                    )}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  )
}
