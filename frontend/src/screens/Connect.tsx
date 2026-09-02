import { useState } from 'react'
import { api, errorText, painted, type ConnectInput } from '../lib/api'
import { useSession } from '../store/session'
import { Button, Card, Field, Input, Notice, Screen, Select } from '../components/ui'

const SSL_MODES = ['disable', 'prefer', 'require', 'verify-ca', 'verify-full']

const empty: ConnectInput = {
  host: 'localhost',
  port: 5432,
  database: '',
  user: '',
  password: '',
  sslMode: 'prefer',
  caCertPath: '',
}

export default function Connect() {
  const [form, setForm] = useState<ConnectInput>(empty)
  const [error, setError] = useState('')
  const [connecting, setConnecting] = useState(false)

  const { server, setServer, setTable, setMappings, setMappingStatus, setPK, unlock, goTo, reset } =
    useSession()
  const [configNote, setConfigNote] = useState('')

  const set = <K extends keyof ConnectInput>(key: K, value: ConnectInput[K]) =>
    setForm((f) => ({ ...f, [key]: value }))

  async function connect() {
    setError('')
    setConnecting(true)
    await painted()

    try {
      const result = await api.connect(form)
      setServer(result.server)
      // Unlock the next step but stay here. Connecting is not a decision about
      // what to do next, and the two ways forward — load a saved layout, or
      // pick a table by hand — are both on this screen.
      unlock('table')
    } catch (err) {
      setError(errorText(err))
    } finally {
      setConnecting(false)
    }
  }

  async function disconnect() {
    try {
      await api.disconnect()
    } finally {
      reset()
      setForm({ ...empty, host: form.host, port: form.port, database: form.database, user: form.user })
    }
  }

  // Loading a configuration here is the whole point of the reuse workflow: the
  // file names its own table, so the operator goes straight from connecting to
  // opening this month's spreadsheet, skipping the table and mapping screens
  // (spec §15).
  async function loadConfiguration() {
    setError('')
    setConfigNote('')
    try {
      const res = await api.importConfig()
      if (!res.config) return // dialog cancelled

      if (res.table) {
        setTable(res.table)
        unlock('file')
      }
      setMappings(res.config.mappings ?? [])
      setMappingStatus(res.status)
      setPK({
        strategy: res.config.primaryKey.strategy,
        columns: res.config.primaryKey.columns,
        emitSetval: res.config.primaryKey.emitSetval,
      })

      if (res.warnings && res.warnings.length > 0) {
        setError(res.warnings.map((w) => `${w.message}, ${w.detail}`).join('; '))
        return
      }

      const mapped = res.status?.mapped ?? res.config.mappings?.length ?? 0
      setConfigNote(
        `Loaded "${res.config.name || res.config.target.table}" for ` +
          `${res.config.target.schema}.${res.config.target.table}: ${mapped} column mappings.`,
      )
      if (res.table) goTo('file')
    } catch (err) {
      setError(errorText(err))
    }
  }

  if (server) {
    return (
      <Screen
        title="Connected"
        subtitle={`${server.database} as ${server.user}`}
        actions={<Button onClick={disconnect}>Disconnect</Button>}
      >
        <Card>
          <dl className="grid grid-cols-[10rem_1fr] gap-y-2 text-sm">
            <dt className="text-slate-500">Server</dt>
            <dd className="selectable">{server.version}</dd>
            <dt className="text-slate-500">Database</dt>
            <dd className="selectable">{server.database}</dd>
            <dt className="text-slate-500">User</dt>
            <dd className="selectable">{server.user}</dd>
            <dt className="text-slate-500">Server timezone</dt>
            <dd className="selectable">{server.serverTimezone}</dd>
          </dl>
        </Card>

        <p className="mt-4 text-sm text-slate-600">
          Credentials are held in memory for this session only. Nothing is written to disk, and the
          connection is closed when you disconnect or close the application.
        </p>

        {configNote && (
          <div className="mt-4">
            <Notice
              tone="ok"
              title={configNote}
              detail="Open this month's spreadsheet next. The mapping is checked against it then, including whether the layout still matches."
            />
          </div>
        )}
        {error && (
          <div className="mt-4">
            <Notice tone="warning" title="The configuration loaded with warnings" detail={error} />
          </div>
        )}

        <div className="mt-6 flex gap-2">
          <Button variant="primary" onClick={() => goTo('table')}>
            Choose a table
          </Button>
          <Button onClick={() => void loadConfiguration()}>Load a saved configuration…</Button>
        </div>
      </Screen>
    )
  }

  return (
    <Screen
      title="Connect to the database"
      subtitle="Connection details are entered fresh each session and are never saved."
    >
      <Card className="max-w-4xl">
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault()
            void connect()
          }}
        >
          <div className="grid grid-cols-[1fr_8rem] gap-3">
            <Field label="Host">
              <Input value={form.host} onChange={(e) => set('host', e.target.value)} autoFocus />
            </Field>
            <Field label="Port">
              <Input
                type="number"
                value={form.port}
                onChange={(e) => set('port', Number(e.target.value))}
              />
            </Field>
          </div>

          <Field label="Database">
            <Input value={form.database} onChange={(e) => set('database', e.target.value)} />
          </Field>

          <div className="grid grid-cols-2 gap-3">
            <Field label="User">
              <Input value={form.user} onChange={(e) => set('user', e.target.value)} />
            </Field>
            <Field label="Password">
              <Input
                type="password"
                value={form.password}
                onChange={(e) => set('password', e.target.value)}
              />
            </Field>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <Field
              label="SSL mode"
              hint={
                form.sslMode === 'disable'
                  ? 'The connection will not be encrypted.'
                  : undefined
              }
            >
              <Select value={form.sslMode} onChange={(e) => set('sslMode', e.target.value)}>
                {SSL_MODES.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </Select>
            </Field>

            {(form.sslMode === 'verify-ca' || form.sslMode === 'verify-full') && (
              <Field label="CA certificate" hint="PEM file used to verify the server">
                <Input
                  value={form.caCertPath}
                  onChange={(e) => set('caCertPath', e.target.value)}
                  placeholder="C:\\certs\\server-ca.pem"
                />
              </Field>
            )}
          </div>

          {error && <Notice tone="error" title="Could not connect" detail={error} />}

          <div className="flex items-center gap-3 pt-1">
            <Button type="submit" variant="primary" disabled={connecting || !form.database}>
              {connecting ? 'Connecting…' : 'Connect'}
            </Button>
            <span className="text-xs text-slate-500">
              The tool reads the schema; nothing is written until you choose to.
            </span>
          </div>
        </form>
      </Card>

      <div className="mt-4 max-w-4xl">
        <LoadConfigurationHint />
      </div>
    </Screen>
  )
}

function LoadConfigurationHint() {
  return (
    <Card>
      <p className="text-sm font-medium text-slate-800">Reusing a saved layout?</p>
      <p className="mt-0.5 text-sm text-slate-600">
        A configuration file holds the mapping and settings for one spreadsheet layout, and names
        the table it was built for. It contains no connection details by design, so connect first,
        then load it here and go straight to this month's spreadsheet.
      </p>
    </Card>
  )
}
