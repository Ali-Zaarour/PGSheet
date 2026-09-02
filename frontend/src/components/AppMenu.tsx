import { useEffect, useRef, useState } from 'react'
import { api, errorText, type AboutInfo } from '../lib/api'
import { Button, Card } from './ui'
import Logo from './Logo'

/** The window menu: About and Exit. */
export default function AppMenu() {
  const [open, setOpen] = useState(false)
  const [about, setAbout] = useState<AboutInfo | null>(null)
  const [error, setError] = useState('')
  const menuRef = useRef<HTMLDivElement>(null)

  // Clicking anywhere else closes the menu, and so does Escape. A dropdown
  // that can only be dismissed by clicking the button again feels stuck.
  useEffect(() => {
    if (!open) return

    function onPointerDown(e: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setOpen(false)
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false)
    }

    document.addEventListener('mousedown', onPointerDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onPointerDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  async function showAbout() {
    setOpen(false)
    setError('')
    try {
      setAbout(await api.about())
    } catch (err) {
      setError(errorText(err))
    }
  }

  async function quit() {
    setOpen(false)
    try {
      await api.quit()
    } catch (err) {
      setError(errorText(err))
    }
  }

  return (
    <>
      <div className="relative" ref={menuRef}>
        <button
          onClick={() => setOpen((v) => !v)}
          aria-haspopup="menu"
          aria-expanded={open}
          className="flex items-center gap-1.5 rounded px-2 py-1.5 text-slate-600 transition-colors hover:bg-slate-100"
        >
          <Logo size={18} />
          <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
            <path d="M1 3 L5 7 L9 3" fill="none" stroke="currentColor" strokeWidth="1.5" />
          </svg>
        </button>

        {open && (
          <div
            role="menu"
            className="absolute right-0 z-20 mt-1 w-48 overflow-hidden rounded-lg border border-slate-200 bg-white py-1 shadow-lg"
          >
            <MenuItem onClick={() => void showAbout()}>About PGSheet</MenuItem>
            <div className="my-1 border-t border-slate-100" />
            <MenuItem onClick={() => void quit()} danger>
              Exit
            </MenuItem>
          </div>
        )}
      </div>

      {about && <AboutDialog info={about} onClose={() => setAbout(null)} />}
      {error && (
        <div className="absolute right-4 top-12 z-30">
          <Card className="border-red-200">
            <p className="text-sm text-severity-error">{error}</p>
          </Card>
        </div>
      )}
    </>
  )
}

function MenuItem({
  children,
  onClick,
  danger,
}: {
  children: React.ReactNode
  onClick: () => void
  danger?: boolean
}) {
  return (
    <button
      role="menuitem"
      onClick={onClick}
      className={`block w-full px-3 py-1.5 text-left text-sm transition-colors hover:bg-slate-50 ${
        danger ? 'text-severity-error' : 'text-slate-700'
      }`}
    >
      {children}
    </button>
  )
}

function AboutDialog({ info, onClose }: { info: AboutInfo; onClose: () => void }) {
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div
      className="fixed inset-0 z-40 flex items-center justify-center bg-slate-900/40 p-6"
      onClick={onClose}
    >
      <div
        className="w-full max-w-md rounded-lg border border-slate-200 bg-white p-5 shadow-xl"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label="About PGSheet"
      >
        <div className="flex items-start gap-3">
          <Logo size={44} />
          <div>
            <h2 className="text-base font-semibold text-slate-900">{info.name}</h2>
            <p className="font-mono text-xs text-slate-500 selectable">{info.version}</p>
            <p className="mt-1 text-sm text-slate-600">{info.description}</p>
          </div>
        </div>

        <div className="mt-4 border-t border-slate-100 pt-3">
          <p className="mb-2 text-xs font-medium uppercase tracking-wide text-slate-500">
            Developer
          </p>
          <dl className="grid grid-cols-[5rem_1fr] gap-y-1.5 text-sm">
            <dt className="text-slate-500">Name</dt>
            <dd className="selectable text-slate-800">{info.developer}</dd>
            <dt className="text-slate-500">Email</dt>
            <dd className="selectable text-slate-800">{info.email}</dd>
            <dt className="text-slate-500">Phone</dt>
            <dd className="selectable text-slate-800">{info.phone}</dd>
          </dl>
        </div>

        <div className="mt-4 border-t border-slate-100 pt-3">
          <dl className="grid grid-cols-[5rem_1fr] gap-y-1.5 text-xs text-slate-500">
            <dt>Platform</dt>
            <dd className="selectable">{info.platform}</dd>
            <dt>Runtime</dt>
            <dd className="selectable">{info.goVersion}</dd>
            <dt>Logs</dt>
            {/* Where to look when something goes wrong, without asking. */}
            <dd className="selectable break-all">{info.logDir}</dd>
          </dl>
        </div>

        <div className="mt-5 flex justify-end">
          <Button variant="primary" onClick={onClose}>
            Close
          </Button>
        </div>
      </div>
    </div>
  )
}
