import { useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Badge, Button, Stack, Tab, TabsBar, Text, useStyles2 } from '@grafana/ui'
import type { PythonRunResult } from '../../sandbox-api'
import { t } from '../../i18n'

type ResultTab = 'output' | 'stdout' | 'stderr' | 'error'

/** One Python invocation's result: output, captured stdout/stderr, the
 * exception (with a jump to its source line), and the run time. */
export function RunResultView({ result, onJumpToLine, compact }: { result: PythonRunResult; onJumpToLine?: (line: number, column?: number) => void; compact?: boolean }) {
  const styles = useStyles2(getStyles)
  const [tab, setTab] = useState<ResultTab>(result.error ? 'error' : 'output')
  const output = result.drop ? null : result.rawText !== undefined ? result.rawText : result.rawBase64 !== undefined ? `base64:${result.rawBase64}` : JSON.stringify(result.fields, null, 2)
  const tabs: { id: ResultTab; label: string; counter?: number }[] = [
    { id: 'output', label: t('gofi.python.run-result.tab-output', 'Output') },
    { id: 'stdout', label: 'stdout', counter: lineCount(result.stdout) },
    { id: 'stderr', label: 'stderr', counter: lineCount(result.stderr) },
    { id: 'error', label: t('gofi.python.run-result.tab-error', 'Error'), counter: result.error ? 1 : 0 },
  ]
  return <div className={styles.frame} role="status" aria-label={t('gofi.python.run-result.aria-label', 'Function result')}>
    <div className={styles.header}>
      <TabsBar hideBorder>{tabs.map(item => <Tab key={item.id} label={item.label} active={tab === item.id} counter={item.counter || undefined} onChangeTab={() => setTab(item.id)} />)}</TabsBar>
      <Stack gap={1} alignItems="center">
        {result.error ? <Badge color="red" icon="exclamation-triangle" text={t('gofi.python.run-result.raised', 'Raised')} /> : result.drop ? <Badge color="orange" text={t('gofi.python.run-result.dropped', 'Dropped')} /> : <Badge color="green" icon="check" text={t('gofi.python.run-result.returned', 'Returned')} />}
        <Badge color="darkgrey" icon="clock-nine" text={formatDuration(result.durationUs)} />
      </Stack>
    </div>
    <div className={compact ? styles.bodyCompact : styles.body}>
      {tab === 'output' && (result.drop ? <Text color="secondary" italic>{t('gofi.python.run-result.none-returned', 'None returned — the message is dropped (sinks keep the original).')}</Text> : <pre className={styles.pre}>{output}</pre>)}
      {tab === 'stdout' && (result.stdout ? <pre className={styles.pre}>{result.stdout}</pre> : <Text color="secondary" italic>{t('gofi.python.run-result.no-stdout', 'No print output.')}</Text>)}
      {tab === 'stderr' && (result.stderr ? <pre className={styles.pre}>{result.stderr}</pre> : <Text color="secondary" italic>{t('gofi.python.run-result.no-stderr', 'Nothing written to stderr.')}</Text>)}
      {tab === 'error' && (result.error ? <Stack direction="column" gap={1}>
        <pre className={styles.errorText}>{result.error}</pre>
        {result.errorLocation && <div>
          {onJumpToLine ? <Button size="sm" variant="secondary" fill="outline" icon="arrow-right" onClick={() => onJumpToLine(result.errorLocation!.line, result.errorLocation!.column)}>{t('gofi.python.run-result.go-to-line', 'Go to line {{line}}', { line: result.errorLocation.line })}</Button>
            : <Text color="secondary">{t('gofi.python.run-result.line', 'Line {{line}}', { line: result.errorLocation.line })}</Text>}
        </div>}
      </Stack> : <Text color="secondary" italic>{t('gofi.python.run-result.no-exception', 'No exception.')}</Text>)}
    </div>
  </div>
}

const lineCount = (text: string) => text ? text.replace(/\n$/, '').split('\n').length : 0
export function formatDuration(us: number): string {
  if (!us) return '—'
  if (us < 1000) return t('gofi.python.log-model.duration-microseconds', '{{value}} µs', { value: us })
  if (us < 1_000_000) return t('gofi.python.log-model.duration-milliseconds', '{{value}} ms', { value: (us / 1000).toFixed(us < 10_000 ? 2 : 1) })
  return t('gofi.python.log-model.duration-seconds', '{{value}} s', { value: (us / 1_000_000).toFixed(2) })
}

const getStyles = (theme: GrafanaTheme2) => ({
  frame: css({ border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, background: theme.colors.background.primary, minWidth: 0 }),
  header: css({ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: theme.spacing(1), padding: theme.spacing(0, 1), borderBottom: `1px solid ${theme.colors.border.weak}` }),
  body: css({ padding: theme.spacing(1.5), maxHeight: 320, overflow: 'auto' }),
  bodyCompact: css({ padding: theme.spacing(1), maxHeight: 180, overflow: 'auto' }),
  pre: css({ margin: 0, fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, whiteSpace: 'pre-wrap', wordBreak: 'break-word', color: theme.colors.text.primary }),
  errorText: css({ margin: 0, fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, whiteSpace: 'pre-wrap', wordBreak: 'break-word', color: theme.colors.error.text }),
})
