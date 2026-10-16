import { useCallback, useEffect, useState } from 'react'
import { listExpressions, type CelExpression } from '../../expression-api'

/** Library entries for pickers and linked fields, refreshed on demand. */
export function useExpressions(enabled = true): { expressions: CelExpression[]; error: string | null; loading: boolean; refresh: () => Promise<void> } {
  const [expressions, setExpressions] = useState<CelExpression[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const refresh = useCallback(async () => {
    try { setExpressions(await listExpressions()); setError(null) }
    catch (reason) { setError(String(reason)) }
    finally { setLoading(false) }
  }, [])
  useEffect(() => { if (enabled) void refresh() }, [enabled, refresh])
  return { expressions, error, loading, refresh }
}
