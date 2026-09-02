import { useEffect, useState } from 'react'
import { api, onProgress } from './lib/api'
import { STEPS, STEP_LABELS, useSession } from './store/session'
import Connect from './screens/Connect'
import TableSelect from './screens/TableSelect'
import FileSelect from './screens/FileSelect'
import Mapping from './screens/Mapping'
import PrimaryKey from './screens/PrimaryKey'
import Validation from './screens/Validation'
import Generate from './screens/Generate'
import AppMenu from './components/AppMenu'

const SCREENS = {
  connect: Connect,
  table: TableSelect,
  file: FileSelect,
  mapping: Mapping,
  primaryKey: PrimaryKey,
  validation: Validation,
  generate: Generate,
} as const

export default function App() {
  const { step, unlocked, goTo, setProgress, server, table, sheet } = useSession()
  const [version, setVersion] = useState('')

  useEffect(() => {
    // The bridge is injected by the Wails runtime, which is not guaranteed to
    // have run by the time React mounts. Swallowing the failure once left the
    // version blank for the whole session, so it is retried briefly.
    let cancelled = false
    let attempts = 0

    const read = () => {
      api
        .version()
        .then((v) => {
          if (!cancelled) setVersion(v)
        })
        .catch(() => {
          if (cancelled || attempts >= 5) return
          attempts++
          setTimeout(read, 200)
        })
    }
    read()

    const off = onProgress(setProgress)
    return () => {
      cancelled = true
      off()
    }
  }, [setProgress])

  const Screen = SCREENS[step]
  const unlockedIndex = STEPS.indexOf(unlocked)

  return (
    <div className="flex h-full flex-col">
      <nav className="flex items-center gap-1 border-b border-slate-200 bg-white px-3 py-2">
        {STEPS.map((s, i) => {
          const reachable = i <= unlockedIndex
          const active = s === step
          return (
            <button
              key={s}
              onClick={() => goTo(s)}
              disabled={!reachable}
              className={[
                'rounded px-3 py-1.5 text-sm transition-colors',
                active ? 'bg-slate-900 text-white' : 'text-slate-600',
                reachable && !active ? 'hover:bg-slate-100' : '',
                !reachable ? 'cursor-not-allowed text-slate-300' : '',
              ].join(' ')}
            >
              <span className="mr-1.5 text-xs opacity-60">{i + 1}</span>
              {STEP_LABELS[s]}
            </button>
          )
        })}

        <div className="ml-auto flex items-center gap-3 text-xs text-slate-500">
          {/* The context bar answers "what am I about to write, and where?" at
              every step, the question the operator should never have to leave
              the screen to check. */}
          {server && <span className="selectable">{server.database}</span>}
          {table && (
            <span className="selectable">
              {table.schema.schema}.{table.schema.table}
            </span>
          )}
          {sheet && <span className="selectable">{sheet.name}</span>}
          {version && (
            <span className="text-slate-400" title={`PGSheet ${version}`}>
              v{version}
            </span>
          )}
          <AppMenu />
        </div>
      </nav>

      <main className="min-h-0 flex-1 overflow-hidden p-5">
        <Screen />
      </main>
    </div>
  )
}
