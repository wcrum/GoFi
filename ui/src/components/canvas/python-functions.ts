import { useEffect, useState } from 'react'
import { listLibraryFunctions, type LibraryFunction, type PythonFunctionKind } from '../../sandbox-api'
import type { PythonFunctionRef } from '../../types'
export function useLibraryFunctions(): { functions: LibraryFunction[]; error: string | null } {
  const [functions, setFunctions] = useState<LibraryFunction[]>([])
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    let active = true
    // Inline Python belongs to its own flow: never offer it to other nodes.
    const refresh = () => listLibraryFunctions().then(value => { if (active) { setFunctions(value.filter(fn => !fn.managedBy)); setError(null) } }).catch(reason => { if (active) setError(String(reason)) })
    void refresh()
    const timer = window.setInterval(refresh, 5000)
    return () => { active = false; window.clearInterval(timer) }
  }, [])
  return { functions, error }
}
export function pythonValue(ref?: PythonFunctionRef): string { return ref ? `python:${JSON.stringify([ref.library,ref.function])}` : 'python' }
export function parsePythonValue(value: string): PythonFunctionRef | undefined {
  if (!value.startsWith('python:')) return undefined
  const [library, name] = JSON.parse(value.slice(7)) as [string,string]
  return { library, function: name }
}
export function functionOptions(functions: LibraryFunction[], kind: PythonFunctionKind, current?: PythonFunctionRef) {
  const options = functions.filter(fn => fn.kind === kind).map(fn => ({ value: pythonValue({ library: fn.library, function: fn.name }), label: `Python · ${fn.library}.${fn.name}` }))
  if (current && !options.some(option => option.value === pythonValue(current))) options.push({value: pythonValue(current),label:`Python · ${current.library}.${current.function} (unavailable)`})
  return options
}
