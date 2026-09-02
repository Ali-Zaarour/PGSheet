import type { ReactNode, InputHTMLAttributes, SelectHTMLAttributes } from 'react'

/** The small set of pieces every screen is built from. */

/**
 * One step, filling the window: a header that stays put and a body that
 * scrolls. The body owns the scrolling rather than the page, so a long table
 * never pushes the actions out of reach.
 */
export function Screen({
  title,
  subtitle,
  children,
  actions,
}: {
  title: string
  subtitle?: string
  children: ReactNode
  actions?: ReactNode
}) {
  return (
    <section className="flex h-full w-full flex-col">
      <header className="mb-4 flex shrink-0 items-start justify-between gap-6">
        <div>
          <h1 className="text-lg font-semibold text-slate-900">{title}</h1>
          {subtitle && <p className="mt-1 text-slate-600">{subtitle}</p>}
        </div>
        {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
      </header>

      <div className="min-h-0 flex-1 overflow-auto pr-0.5">{children}</div>
    </section>
  )
}

export function Card({ children, className = '' }: { children: ReactNode; className?: string }) {
  return (
    <div className={`rounded-lg border border-slate-200 bg-white p-4 shadow-sm ${className}`}>
      {children}
    </div>
  )
}

export function Button({
  children,
  onClick,
  variant = 'secondary',
  disabled,
  type = 'button',
}: {
  children: ReactNode
  onClick?: () => void
  variant?: 'primary' | 'secondary' | 'danger' | 'ghost'
  disabled?: boolean
  type?: 'button' | 'submit'
}) {
  const styles = {
    primary: 'bg-slate-900 text-white hover:bg-slate-800 disabled:bg-slate-300',
    secondary:
      'border border-slate-300 bg-white text-slate-800 hover:bg-slate-50 disabled:text-slate-400',
    danger: 'bg-severity-error text-white hover:opacity-90 disabled:opacity-50',
    ghost: 'text-slate-600 hover:bg-slate-100 disabled:text-slate-300',
  }[variant]

  return (
    <button
      type={type}
      onClick={onClick}
      disabled={disabled}
      className={`rounded px-3 py-1.5 text-sm font-medium transition-colors disabled:cursor-not-allowed ${styles}`}
    >
      {children}
    </button>
  )
}

// preserveCase leaves the label exactly as written. Labels are styled in upper
// case, which is fine for a name but wrong when the label quotes something the
// operator has to type back: the field would ask for CUSTOMERS and accept only
// customers.
export function Field({
  label,
  hint,
  preserveCase,
  children,
}: {
  label: string
  hint?: string
  preserveCase?: boolean
  children: ReactNode
}) {
  return (
    <label className="block">
      <span
        className={`mb-1 block text-xs font-medium tracking-wide text-slate-500 ${
          preserveCase ? '' : 'uppercase'
        }`}
      >
        {label}
      </span>
      {children}
      {hint && <span className="mt-1 block text-xs text-slate-500">{hint}</span>}
    </label>
  )
}

export function Input(props: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      {...props}
      className="w-full rounded border border-slate-300 px-2.5 py-1.5 text-sm text-slate-900 outline-none selectable focus:border-slate-500 focus:ring-1 focus:ring-slate-500"
    />
  )
}

export function Select(props: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      {...props}
      className="w-full rounded border border-slate-300 bg-white px-2.5 py-1.5 text-sm text-slate-900 outline-none focus:border-slate-500 focus:ring-1 focus:ring-slate-500"
    />
  )
}

export function Checkbox({
  label,
  checked,
  onChange,
  hint,
  disabled,
}: {
  label: string
  checked: boolean
  onChange: (v: boolean) => void
  hint?: string
  disabled?: boolean
}) {
  return (
    <label className="flex cursor-pointer items-start gap-2">
      <input
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.target.checked)}
        className="mt-0.5 h-4 w-4 rounded border-slate-300"
      />
      <span>
        <span className="text-sm text-slate-800">{label}</span>
        {hint && <span className="block text-xs text-slate-500">{hint}</span>}
      </span>
    </label>
  )
}

export function Badge({
  children,
  tone = 'neutral',
}: {
  children: ReactNode
  tone?: 'neutral' | 'error' | 'warning' | 'ok' | 'info'
}) {
  const styles = {
    neutral: 'bg-slate-100 text-slate-700',
    error: 'bg-red-50 text-severity-error',
    warning: 'bg-amber-50 text-severity-warning',
    ok: 'bg-emerald-50 text-emerald-700',
    info: 'bg-blue-50 text-severity-info',
  }[tone]

  return (
    <span className={`rounded px-1.5 py-0.5 font-mono text-xs ${styles}`}>{children}</span>
  )
}

/**
 * A problem the operator has to act on.
 *
 * Every one of these names a specific thing and says what to do about it —
 * "validation failed" with no location is not an acceptable message (spec §5).
 */
export function Notice({
  tone,
  title,
  detail,
  children,
}: {
  tone: 'error' | 'warning' | 'info' | 'ok'
  title: string
  detail?: string
  children?: ReactNode
}) {
  const styles = {
    error: 'border-red-200 bg-red-50 text-red-900',
    warning: 'border-amber-200 bg-amber-50 text-amber-900',
    info: 'border-blue-200 bg-blue-50 text-blue-900',
    ok: 'border-emerald-200 bg-emerald-50 text-emerald-900',
  }[tone]

  return (
    <div className={`rounded border px-3 py-2 text-sm ${styles}`}>
      <p className="font-medium">{title}</p>
      {detail && <p className="mt-0.5 opacity-90 selectable">{detail}</p>}
      {children}
    </div>
  )
}

export function Stat({ label, value, tone }: { label: string; value: string | number; tone?: 'error' | 'warning' | 'ok' }) {
  const valueTone = {
    error: 'text-severity-error',
    warning: 'text-severity-warning',
    ok: 'text-emerald-700',
    undefined: 'text-slate-900',
  }[tone ?? 'undefined']

  return (
    <div className="rounded-lg border border-slate-200 bg-white px-4 py-3">
      <div className="text-xs uppercase tracking-wide text-slate-500">{label}</div>
      <div className={`mt-1 text-2xl font-semibold tabular-nums ${valueTone}`}>{value}</div>
    </div>
  )
}

/**
 * What the app is doing, while it is doing it. Long reads look like a hang
 * without one, and a cancel button is what makes a long one bearable.
 */
export function Busy({
  label,
  detail,
  onCancel,
}: {
  label: string
  detail?: string
  onCancel?: () => void
}) {
  return (
    <div className="flex items-center gap-3 rounded-lg border border-slate-200 bg-white px-4 py-3">
      <Spinner />
      <div className="flex-1">
        <p className="text-sm font-medium text-slate-800">{label}…</p>
        {detail && <p className="text-xs text-slate-500 tabular-nums">{detail}</p>}
      </div>
      {onCancel && (
        <Button variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
      )}
    </div>
  )
}

export function Spinner({ size = 16 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      className="shrink-0 animate-spin text-slate-400"
      aria-hidden="true"
    >
      <circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" strokeWidth="3" opacity="0.25" />
      <path d="M21 12a9 9 0 0 0-9-9" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
    </svg>
  )
}

export function Empty({ children }: { children: ReactNode }) {
  return (
    <div className="rounded border border-dashed border-slate-300 px-4 py-8 text-center text-sm text-slate-500">
      {children}
    </div>
  )
}
