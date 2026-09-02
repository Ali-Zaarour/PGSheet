/**
 * Typed access to the Go side.
 *
 * Wails exposes the bound methods on `window.go` at runtime. Declaring the
 * surface here rather than importing the generated bindings means the frontend
 * type-checks without them, and the contract lives in one file.
 *
 * These shapes mirror the JSON tags on the Go structs.
 */

// ---------- schema ----------

export interface Column {
  name: string
  ordinalPosition: number
  dataType: string
  formattedType: string
  nullable: boolean
  hasDefault: boolean
  defaultExpr: string
  maxLength: number | null
  numericPrecision: number | null
  numericScale: number | null
  isIdentity: boolean
  identityKind: string
  isGenerated: boolean
  enumValues: string[] | null
  arrayElemType: string
  comment: string
}

export interface Constraint {
  name: string
  type: 'p' | 'u' | 'c' | 'f' | 'x'
  columns: string[]
  definition: string
  refTable: string
  refColumns: string[] | null
}

export interface Sequence {
  name: string
  lastValue: number
  isCalled: boolean
  nextValue: number
  increment: number
  ownedBy: string
}

export interface TableSchema {
  schema: string
  table: string
  columns: Column[]
  constraints: Constraint[]
  primaryKey: Constraint | null
  pkSequence: Sequence | null
  rowCount: number
}

export interface TableRef {
  schema: string
  table: string
  estRows: number
  comment: string
}

export interface UniqueIndex {
  name: string
  columns: string[]
  definition: string
  primary: boolean
  partial: boolean
}

export interface UniquenessRule {
  name: string
  columns: string[]
  primary: boolean
  partial: boolean
  definition: string
}

export interface Privileges {
  table: string
  canSelect: boolean
  canInsert: boolean
}

export interface Unverifiable {
  constraint: string
  definition: string
  reason: string
}

export type PKStrategy = 'sequence' | 'mapped' | 'none'

export interface PKOption {
  strategy: PKStrategy
  label: string
  description: string
  recommended: boolean
  available: boolean
  reason: string
}

export interface TableDetail {
  schema: TableSchema
  uniqueIndexes: UniqueIndex[] | null
  rules: UniquenessRule[] | null
  privileges: Privileges
  pkOptions: PKOption[]
  unverifiable: Unverifiable[] | null
}

// ---------- connection ----------

export interface ConnectInput {
  host: string
  port: number
  database: string
  user: string
  password: string
  sslMode: string
  caCertPath: string
}

export interface ServerInfo {
  database: string
  user: string
  version: string
  versionNum: number
  standardConformingStrings: boolean
  serverTimezone: string
}

export interface ConnectResult {
  server: ServerInfo
  sslModes: string[]
}

// ---------- workbook ----------

export interface HeaderGuess {
  row: number
  score: number
  margin: number
  confident: boolean
}

export interface WorkbookInfo {
  path: string
  fileName: string
  sheets: string[]
  headerGuess: HeaderGuess
  merged: string[] | null
}

export interface SheetInfo {
  name: string
  headers: string[]
  headerRow: number
  dataStart: number
  totalRows: number
  fingerprint: string
}

export interface PreviewCell {
  text: string
  kind: 'empty' | 'text' | 'number' | 'boolean' | 'date' | 'error' | 'unknown'
}

export interface SheetSelection {
  info: SheetInfo
  preview: PreviewCell[][]
  warnings: string[] | null
  /** The mapping re-checked against this sheet, when one was already loaded. */
  status: MappingStatus | null
}

// ---------- mapping ----------

export interface Transform {
  trim: boolean
  blankAsNull: boolean
  upperCase: boolean
  lowerCase: boolean
  dateFormat: string
  boolMap: Record<string, boolean> | null
  valueMap: Record<string, string> | null
  defaultOnBlank: string
  stripNonDigits: boolean
}

export interface ColumnMapping {
  excelColumn: string
  excelIndex: number
  dbColumn: string
  transform: Transform
  enabled: boolean
}

export interface Suggestion {
  excelIndex: number
  excelColumn: string
  dbColumn: string
  score: number
  reason: string
  accepted: boolean
}

export interface MappingProblem {
  code: string
  severity: 'error' | 'warning'
  message: string
  hint: string
  dbColumn: string
  excelColumn: string
}

export interface MappingStatus {
  /** No workbook is open yet, so the checks that need the sheet have not run. */
  sheetPending: boolean
  mapped: number
  unmappedSheet: number
  defaultedCols: number
  problems: MappingProblem[] | null
  blocking: boolean
  mappedDbColumns: string[] | null
}

export interface AutoMatchResult {
  suggestions: Suggestion[] | null
  mappings: ColumnMapping[] | null
  status: MappingStatus
}

export interface PKChoice {
  strategy: PKStrategy
  columns: string[] | null
  emitSetval: boolean
}

// ---------- validation ----------

export type Severity = 'error' | 'warning' | 'info'

export interface Issue {
  code: string
  severity: Severity
  scope: 'row' | 'column' | 'file'
  excelRow: number
  excelColumn: string
  excelRef: string
  dbColumn: string
  value: string
  message: string
  hint: string
}

export interface ColumnVerdict {
  excelColumn: string
  dbColumn: string
  sampled: number
  failures: number
  failureRate: number
  blocked: boolean
  reason: string
}

export interface Report {
  issues: Issue[] | null
  errorCount: number
  warningCount: number
  rowsTotal: number
  rowsValid: number
  rowsSkipped: number
  byColumn: Record<string, number> | null
  byCode: Record<string, number> | null
  truncated: boolean
  duration: string
  columnVerdicts: ColumnVerdict[] | null
  unverifiable: Unverifiable[] | null
}

export interface ValidationSettings {
  MaxIssues: number
  ColumnMisalignThreshold: number
  CheckUniqueAgainstDB: boolean
  CheckForeignKeys: boolean
  SkipBlankRows: boolean
}

// ---------- generation ----------

export interface GenerateOptions {
  mode: 'insert' | 'copy'
  batchSize: number
  wrapInTransaction: boolean
  includeSummaryHeader: boolean
  skipBlankRows: boolean
  statementTimeout: string
  path: string
}

export interface GenerateResult {
  path: string
  rowsWritten: number
  rowsSkipped: number
  statements: number
  bytesWritten: number
  duration: string
  personalData: string[] | null
  preview: string[] | null
}

export interface ExecResult {
  rowsInserted: number
  rowsSkipped: number
  statements: number
  duration: string
  committed: boolean
}

export interface ConfigWarning {
  kind: string
  message: string
  detail: string
}

export interface SavedConfig {
  configVersion: number
  name: string
  target: { schema: string; table: string }
  source: {
    sheetName: string
    headerRow: number
    dataStartRow: number
    headerFingerprint: string
    headers: string[] | null
  }
  mappings: ColumnMapping[] | null
  primaryKey: { strategy: PKStrategy; columns: string[] | null; emitSetval: boolean }
}

export interface ConfigLoadResult {
  config: SavedConfig
  warnings: ConfigWarning[] | null
  status: MappingStatus
  /** The target table, introspected and selected as part of loading. */
  table: TableDetail | null
}

export interface AboutInfo {
  name: string
  version: string
  description: string
  developer: string
  email: string
  phone: string
  platform: string
  goVersion: string
  logDir: string
}

export interface ProgressEvent {
  phase: string
  current: number
  total: number
  message: string
}

// ---------- the bridge ----------

interface GoApp {
  Connect(input: ConnectInput): Promise<ConnectResult>
  Disconnect(): Promise<void>
  ListTables(): Promise<TableRef[] | null>
  DescribeTable(schema: string, table: string): Promise<TableDetail>
  ChooseWorkbook(): Promise<string>
  OpenWorkbookAt(path: string): Promise<WorkbookInfo>
  SelectSheet(name: string, headerRow: number): Promise<SheetSelection>
  AutoMatch(): Promise<AutoMatchResult>
  SetMappings(mappings: ColumnMapping[]): Promise<MappingStatus>
  TransformFor(dbColumn: string): Promise<Transform>
  SetPKStrategy(choice: PKChoice): Promise<MappingStatus>
  SetValidationOptions(
    settings: ValidationSettings,
    sourceTimezone: string,
    allowRounding: boolean,
    enumCaseInsensitive: boolean,
  ): Promise<void>
  Validate(): Promise<Report>
  DryRun(): Promise<Report>
  ExportIssues(): Promise<string>
  Generate(options: GenerateOptions): Promise<GenerateResult>
  ExecuteDirect(options: GenerateOptions, confirmTableName: string): Promise<ExecResult>
  ExportConfig(): Promise<string>
  ImportConfig(): Promise<ConfigLoadResult>
  Cancel(): Promise<void>
  Version(): Promise<string>
  About(): Promise<AboutInfo>
  Quit(): Promise<void>
}

declare global {
  interface Window {
    go?: { app?: { App?: GoApp } }
    runtime?: {
      EventsOn(event: string, callback: (data: ProgressEvent) => void): () => void
      EventsOff(event: string): void
    }
  }
}

/**
 * The bound Go object, or a message a person can act on. A missing bridge
 * means the dev server was opened in a browser, which is worth saying.
 */
function backend(): GoApp {
  const bound = window.go?.app?.App
  if (!bound) {
    throw new Error(
      'The application backend is not available. Run the desktop app with `wails dev` rather than opening the dev server in a browser.',
    )
  }
  return bound
}

export const api = {
  connect: (input: ConnectInput) => backend().Connect(input),
  disconnect: () => backend().Disconnect(),
  listTables: () => backend().ListTables().then((t) => t ?? []),
  describeTable: (schema: string, table: string) => backend().DescribeTable(schema, table),
  chooseWorkbook: () => backend().ChooseWorkbook(),
  openWorkbookAt: (path: string) => backend().OpenWorkbookAt(path),
  selectSheet: (name: string, headerRow: number) => backend().SelectSheet(name, headerRow),
  autoMatch: () => backend().AutoMatch(),
  setMappings: (mappings: ColumnMapping[]) => backend().SetMappings(mappings),
  transformFor: (dbColumn: string) => backend().TransformFor(dbColumn),
  setPKStrategy: (choice: PKChoice) => backend().SetPKStrategy(choice),
  setValidationOptions: (
    settings: ValidationSettings,
    sourceTimezone: string,
    allowRounding: boolean,
    enumCaseInsensitive: boolean,
  ) => backend().SetValidationOptions(settings, sourceTimezone, allowRounding, enumCaseInsensitive),
  validate: () => backend().Validate(),
  dryRun: () => backend().DryRun(),
  exportIssues: () => backend().ExportIssues(),
  generate: (options: GenerateOptions) => backend().Generate(options),
  executeDirect: (options: GenerateOptions, confirmTableName: string) =>
    backend().ExecuteDirect(options, confirmTableName),
  exportConfig: () => backend().ExportConfig(),
  importConfig: () => backend().ImportConfig(),
  cancel: () => backend().Cancel(),
  version: () => backend().Version(),
  about: () => backend().About(),
  quit: () => backend().Quit(),
}

/**
 * Waits until the browser has painted.
 *
 * Setting a busy state and calling straight into the backend can leave the
 * operator looking at the old screen while the work runs: React has queued the
 * render, but nothing has been drawn. Two frames is the reliable point at
 * which the update is on screen.
 */
export function painted(): Promise<void> {
  return new Promise((resolve) =>
    requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
  )
}

/** Subscribes to progress events. Returns an unsubscribe function. */
export function onProgress(callback: (e: ProgressEvent) => void): () => void {
  if (!window.runtime) return () => {}
  return window.runtime.EventsOn('progress', callback)
}

/** Renders an error from the Go side. Wails rejects with a string, not an
 *  Error, and the Go side has already redacted anything sensitive. */
export function errorText(err: unknown): string {
  if (typeof err === 'string') return err
  if (err instanceof Error) return err.message
  return String(err)
}
