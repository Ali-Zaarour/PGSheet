import { create } from 'zustand'
import type {
  ColumnMapping,
  PreviewCell,
  MappingStatus,
  PKChoice,
  ProgressEvent,
  Report,
  ServerInfo,
  SheetInfo,
  TableDetail,
  WorkbookInfo,
} from '../lib/api'

/**
 * The session store: one store, one import. Nothing is persisted, no
 * localStorage, no credentials, no history. Closing the app forgets everything.
 */

export const STEPS = [
  'connect',
  'table',
  'file',
  'mapping',
  'primaryKey',
  'validation',
  'generate',
] as const

export type Step = (typeof STEPS)[number]

export const STEP_LABELS: Record<Step, string> = {
  connect: 'Connect',
  table: 'Table',
  file: 'File',
  mapping: 'Mapping',
  primaryKey: 'Primary key',
  validation: 'Validation',
  generate: 'Generate',
}

interface SessionState {
  step: Step
  /** The furthest step reached. A step unlocks once the previous one produced
   *  valid state. */
  unlocked: Step
  progress: ProgressEvent | null
  busy: string | null

  server: ServerInfo | null
  table: TableDetail | null
  workbook: WorkbookInfo | null
  sheet: SheetInfo | null
  /** The first rows, so the mapping screen can show sample values: the
   *  cheapest way to catch a correctly-named column with the wrong content. */
  preview: PreviewCell[][]
  mappings: ColumnMapping[]
  mappingStatus: MappingStatus | null
  pk: PKChoice
  report: Report | null
  dryRunReport: Report | null

  goTo: (step: Step) => void
  unlock: (step: Step) => void
  setProgress: (p: ProgressEvent | null) => void
  setBusy: (label: string | null) => void

  setServer: (s: ServerInfo | null) => void
  setTable: (t: TableDetail | null) => void
  setWorkbook: (w: WorkbookInfo | null) => void
  setSheet: (s: SheetInfo | null) => void
  setPreview: (rows: PreviewCell[][]) => void
  setMappings: (m: ColumnMapping[]) => void
  setMappingStatus: (s: MappingStatus | null) => void
  setPK: (pk: PKChoice) => void
  setReport: (r: Report | null) => void
  setDryRunReport: (r: Report | null) => void
  reset: () => void
}

const initialPK: PKChoice = { strategy: 'none', columns: [], emitSetval: false }

export const useSession = create<SessionState>((set, get) => ({
  step: 'connect',
  unlocked: 'connect',
  progress: null,
  busy: null,

  server: null,
  table: null,
  workbook: null,
  sheet: null,
  preview: [],
  mappings: [],
  mappingStatus: null,
  pk: initialPK,
  report: null,
  dryRunReport: null,

  goTo: (step) => {
    if (STEPS.indexOf(step) <= STEPS.indexOf(get().unlocked)) set({ step })
  },

  unlock: (step) => {
    if (STEPS.indexOf(step) > STEPS.indexOf(get().unlocked)) set({ unlocked: step })
  },

  setProgress: (progress) => set({ progress }),
  setBusy: (busy) => set({ busy }),

  setServer: (server) => set({ server }),
  setTable: (table) =>
    // Changing the table invalidates everything downstream of it: a mapping to
    // a different table's columns is not a mapping.
    set({ table, mappings: [], mappingStatus: null, report: null, dryRunReport: null }),
  setWorkbook: (workbook) =>
    set({ workbook, sheet: null, preview: [], report: null, dryRunReport: null }),
  setSheet: (sheet) => set({ sheet, report: null, dryRunReport: null }),
  setPreview: (preview) => set({ preview }),
  setMappings: (mappings) => set({ mappings, report: null, dryRunReport: null }),
  setMappingStatus: (mappingStatus) => set({ mappingStatus }),
  setPK: (pk) => set({ pk, report: null, dryRunReport: null }),
  setReport: (report) => set({ report, dryRunReport: null }),
  setDryRunReport: (dryRunReport) => set({ dryRunReport }),

  reset: () =>
    set({
      step: 'connect',
      unlocked: 'connect',
      progress: null,
      busy: null,
      server: null,
      table: null,
      workbook: null,
      sheet: null,
      preview: [],
      mappings: [],
      mappingStatus: null,
      pk: initialPK,
      report: null,
      dryRunReport: null,
    }),
}))
