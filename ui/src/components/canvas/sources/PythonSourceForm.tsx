import { useEffect, useState } from 'react'
import { Alert, Field, Input, Select, Text, TextArea, TextLink } from '@grafana/ui'
import { Trans, t } from '../../../i18n'
import type { PythonSource } from '../../../types'
import { paths } from '../../../router'
import { useLibraryFunctions } from '../python-functions'
import { everyProblem, parseConfigText, pythonSourceProblems, sourceFunctionOf, sourceFunctionOptions, sourceLibraries } from './pythonSource'

/** `source.python`: a @gofi.fetcher or @gofi.streamer of a saved library. The
 * function runs in the library's isolated worker; the flow runtime only calls
 * that worker, so network access is the library's egress allowlist. */
export function PythonSourceForm({ python, readOnly, onChange }: { python: PythonSource; readOnly?: boolean; onChange: (python: PythonSource) => void }) {
  const { functions, error } = useLibraryFunctions()
  const [configText, setConfigText] = useState(() => configTextOf(python.config))
  const [configError, setConfigError] = useState<string>()
  // Follow outside edits (YAML tab, undo) without clobbering what is typed.
  useEffect(() => {
    const parsed = parseConfigText(configText)
    if (!parsed.error && JSON.stringify(parsed.config ?? {}) !== JSON.stringify(python.config ?? {})) setConfigText(configTextOf(python.config))
  }, [python.config]) // eslint-disable-line react-hooks/exhaustive-deps

  const selected = sourceFunctionOf(functions, python)
  const libraries = sourceLibraries(functions, python.library || undefined)
  const functionOptions = python.library ? sourceFunctionOptions(functions, python.library, python.function || undefined) : []
  // Only the registry kind check is shown here; the other problems are the fields' own validation.
  const problems = pythonSourceProblems(python, functions.length ? functions : undefined).filter(problem => problem.kind === 'wrong-kind').map(problem => problem.message)
  const isStreamer = selected?.kind === 'streamer'
  const params = selected?.params

  return <>
    {error && <Alert severity="warning" title={t('gofi.canvas.python-source-form.libraries-unavailable', 'Functions unavailable')} bottomSpacing={0}>{error}</Alert>}
    <Field noMargin label={t('gofi.canvas.python-source-form.library', 'Library')} description={libraries.length ? t('gofi.canvas.python-source-form.library-description', 'Saved libraries that register a @gofi.fetcher or @gofi.streamer.') : undefined} invalid={!python.library} error={python.library ? undefined : t('gofi.common.required', 'Required.')}>
      <Select disabled={readOnly} value={python.library || null} placeholder={t('gofi.canvas.python-source-form.choose-library', 'Choose a library')}
        options={libraries.map(name => ({ value: name, label: name }))}
        noOptionsMessage={t('gofi.canvas.python-source-form.no-source-library', 'No ready library registers a fetcher or streamer')}
        onChange={option => option?.value && option.value !== python.library && onChange({ ...python, library: option.value, function: '' })} />
    </Field>
    {!libraries.length && !error && <Text variant="bodySmall" color="secondary">
      <Trans i18nKey="gofi.canvas.python-source-form.add-library-hint">
        Add one in <TextLink href={paths.libraries()} inline>Functions</TextLink>: <b>New function</b> offers Fetcher and Streamer templates. Save and launch the library so its worker registers them.
      </Trans>
    </Text>}
    <Field noMargin label={t('gofi.canvas.python-source-form.function', 'Function')} description={selected ? `${selected.kind === 'fetcher' ? t('gofi.canvas.python-source-form.fetcher-description', 'Fetcher: called every interval; calls never overlap.') : t('gofi.canvas.python-source-form.streamer-description', 'Streamer: kept running; restarted with backoff if it fails.')}${selected.description ? ` ${selected.description.split('\n')[0]}` : ''}` : undefined}
      invalid={!!python.library && (!python.function || problems.length > 0)} error={!python.function ? t('gofi.common.required', 'Required.') : problems[0]}>
      <Select disabled={readOnly || !python.library} value={python.function || null} placeholder={t('gofi.canvas.python-source-form.choose-function', 'Choose a fetcher or streamer')}
        options={functionOptions}
        onChange={option => option?.value && onChange({ ...python, function: option.value })} />
    </Field>
    {!isStreamer && <Field noMargin label={t('gofi.canvas.python-source-form.every', 'Every')} description={selected?.every ? t('gofi.canvas.python-source-form.every-description-with-default', "Poll interval for a fetcher. Empty uses the decorator's every ({{every}}), else 30s. Each call runs under the library's timeout.", { every: selected.every }) : t('gofi.canvas.python-source-form.every-description', "Poll interval for a fetcher. Empty uses the decorator's every, else 30s. Each call runs under the library's timeout.")}
      invalid={!!everyProblem(python.every)} error={everyProblem(python.every)}>
      <Input disabled={readOnly} value={python.every ?? ''} placeholder={selected?.every || '30s'} onChange={e => onChange({ ...python, every: e.currentTarget.value || undefined })} />
    </Field>}
    <Field noMargin label={t('gofi.canvas.python-source-form.config', 'Config (JSON object)')} description={params?.length ? t('gofi.canvas.python-source-form.config-keyword-arguments', 'Keyword arguments: {{arguments}}.', { arguments: params.map(p => `${p.name}${p.type && p.type !== 'any' ? `: ${p.type}` : ''}${p.required ? ` ${t('gofi.canvas.python-source-form.required-marker', '(required)')}` : ''}`).join(', ') }) : t('gofi.canvas.python-source-form.config-description', 'Passed to the function as keyword arguments.')}
      invalid={!!configError} error={configError}>
      <TextArea disabled={readOnly} rows={4} value={configText} placeholder='{"limit": 100}'
        onChange={e => {
          const text = e.currentTarget.value
          setConfigText(text)
          const parsed = parseConfigText(text)
          setConfigError(parsed.error)
          if (!parsed.error) onChange({ ...python, config: parsed.config })
        }} />
    </Field>
  </>
}

function configTextOf(config?: Record<string, unknown>): string {
  return config && Object.keys(config).length ? JSON.stringify(config, null, 2) : ''
}
