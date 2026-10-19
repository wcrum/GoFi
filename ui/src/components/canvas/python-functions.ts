import { createContext, useContext, useEffect, useState } from 'react'
import { listLibraryFunctions, type LibraryFunction, type PythonFunctionKind } from '../../sandbox-api'
import type { PythonFunctionRef } from '../../types'
import { refLibrary, storedRef } from '../../lib/python-ref'
import { shared } from '../../lib/stream'

type FunctionsState = { functions: LibraryFunction[]; error: string | null }
const EMPTY: FunctionsState = { functions: [], error: null }
// One module-level poller for every picker on the page (the useClusterInfo
// pattern): N stage inspectors cost one ListFunctions call per interval.
const libraryFunctions = shared<FunctionsState>((publish) => {
  let state = EMPTY
  // Inline Python belongs to its own flow: never offer it to other nodes.
  const refresh = () => listLibraryFunctions()
    .then(value => { state = { functions: value.filter(fn => !fn.managedBy), error: null }; publish(state) })
    .catch(reason => { state = { ...state, error: String(reason) }; publish(state) })
  void refresh()
  const timer = window.setInterval(refresh, 5000)
  return () => window.clearInterval(timer)
}, 5000)
export function useLibraryFunctions(): FunctionsState {
  const [state, setState] = useState<FunctionsState>(EMPTY)
  useEffect(() => libraryFunctions.subscribe(setState), [])
  return state
}

/** The flow's default Python library (FlowDefinition.python.library), for
 * pickers inside the flow editor. Empty when the flow sets none. */
export const PythonDefaultContext = createContext<string>('')
export const usePythonDefault = () => useContext(PythonDefaultContext)

/** Suffix naming the flow default, e.g. "default (hooks)". */
export const defaultTag = (library: string) => `default (${library})`

export function pythonValue(ref?: PythonFunctionRef, defaultLibrary?: string): string { return ref ? `python:${JSON.stringify([refLibrary(ref, defaultLibrary), ref.function])}` : 'python' }
/** The picked ref; the library is omitted when it is the flow default. */
export function parsePythonValue(value: string, defaultLibrary?: string): PythonFunctionRef | undefined {
  if (!value.startsWith('python:')) return undefined
  const [library, name] = JSON.parse(value.slice(7)) as [string,string]
  return storedRef({ library, function: name }, defaultLibrary)
}
/** Label of a library function in a picker; the flow default is named. */
export function functionLabel(library: string, name: string, defaultLibrary?: string) {
  return !library ? name : library === defaultLibrary ? `${name} · ${defaultTag(library)}` : `${library}.${name}`
}
export function functionOptions(functions: LibraryFunction[], kind: PythonFunctionKind, current?: PythonFunctionRef, defaultLibrary?: string) {
  const options = functions.filter(fn => fn.kind === kind).map(fn => ({ value: pythonValue({ library: fn.library, function: fn.name }), label: `Python · ${functionLabel(fn.library, fn.name, defaultLibrary)}` }))
  if (current && !options.some(option => option.value === pythonValue(current, defaultLibrary))) options.push({ value: pythonValue(current, defaultLibrary), label: `Python · ${functionLabel(refLibrary(current, defaultLibrary), current.function, defaultLibrary)} (unavailable)` })
  return options
}
