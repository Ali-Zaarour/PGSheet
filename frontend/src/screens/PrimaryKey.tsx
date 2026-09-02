import { useState } from 'react'
import { api, errorText, type PKStrategy } from '../lib/api'
import { useSession } from '../store/session'
import { Badge, Button, Card, Checkbox, Notice, Screen } from '../components/ui'

export default function PrimaryKey() {
  const { table, pk, setPK, setMappingStatus, unlock, goTo } = useSession()
  const [error, setError] = useState('')

  if (!table) {
    return (
      <Screen title="Decide on the primary key">
        <Notice tone="info" title="Choose a table first" />
      </Screen>
    )
  }

  const schema = table.schema
  const keyColumns = schema.primaryKey?.columns ?? []

  async function choose(strategy: PKStrategy) {
    const next = {
      strategy,
      columns: keyColumns,
      // A sequence that the file supplies values for must be moved past them,
      // or the live application's next insert collides (spec §10).
      emitSetval: strategy === 'mapped' && schema.pkSequence !== null,
    }
    setPK(next)
    setError('')
    try {
      const status = await api.setPKStrategy(next)
      setMappingStatus(status)
      if (!status.blocking) unlock('validation')
    } catch (err) {
      setError(errorText(err))
    }
  }

  return (
    <Screen
      title="Decide on the primary key"
      subtitle={
        schema.primaryKey
          ? `${schema.primaryKey.name} on ${keyColumns.join(', ')}`
          : 'This table has no primary key.'
      }
      actions={
        <Button variant="primary" onClick={() => goTo('validation')}>
          Validate
        </Button>
      }
    >
      {error && <Notice tone="error" title="Could not apply the strategy" detail={error} />}

      {schema.pkSequence && (
        <Card className="mb-4">
          <p className="text-sm text-slate-700">
            The key is backed by{' '}
            <span className="font-mono text-xs">{schema.pkSequence.name}</span>. The next value is
            around <strong>{schema.pkSequence.nextValue.toLocaleString()}</strong>.
          </p>
          <p className="mt-1 text-sm text-slate-500">
            That number is an estimate read from the sequence, not a reservation, reading it does
            not consume a value, and other rows may be added before this file runs.
          </p>
        </Card>
      )}

      <div className="space-y-3">
        {table.pkOptions.map((opt) => {
          const selected = pk.strategy === opt.strategy
          return (
            <button
              key={opt.strategy}
              disabled={!opt.available}
              onClick={() => void choose(opt.strategy)}
              className={`block w-full rounded-lg border p-4 text-left transition-colors ${
                selected
                  ? 'border-slate-900 bg-slate-50'
                  : opt.available
                    ? 'border-slate-200 bg-white hover:border-slate-300'
                    : 'cursor-not-allowed border-slate-200 bg-slate-50 opacity-60'
              }`}
            >
              <div className="flex items-center gap-2">
                <span className="font-medium text-slate-900">{opt.label}</span>
                {opt.recommended && <Badge tone="ok">recommended</Badge>}
                {selected && <Badge tone="info">selected</Badge>}
              </div>
              <p className="mt-1 text-sm text-slate-600">{opt.description}</p>
              {!opt.available && opt.reason && (
                <p className="mt-1 text-sm text-severity-warning">{opt.reason}</p>
              )}
            </button>
          )
        })}
      </div>

      {pk.strategy === 'mapped' && schema.pkSequence && (
        <Card className="mt-4">
          <Checkbox
            label="Resynchronise the sequence at the end of the file"
            checked={pk.emitSetval}
            onChange={(v) => setPK({ ...pk, emitSetval: v })}
            hint="Without this, the application that owns the database fails on its next insert. Turn it off only if you will run setval yourself."
          />
        </Card>
      )}
    </Screen>
  )
}
