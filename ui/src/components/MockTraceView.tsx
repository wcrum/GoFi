import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Badge, Stack, Text, useStyles2 } from '@grafana/ui'
import type { SimulateResult } from '../types'
import { formatDuration } from './python/RunResultView'

// Presentational renderer for a SimulateResult dry-run trace. No data
// fetching — MockPanel (editor, stepping through with Next/Prev) and the
// live traffic view's "Test captured frame" (whole trace) feed it a result.
//
// When `activeIndex` is provided, the stage row at that index is highlighted
// (for the editor's step-through: index -1 = parse step, 0..n-1 = stages).
// When omitted, the full trace renders flat with no per-row emphasis.

interface Props {
  result: SimulateResult
  activeIndex?: number
}

export function MockTraceView({ result, activeIndex }: Props) {
  const styles = useStyles2(getStyles)
  const stepping = activeIndex !== undefined
  return <Stack direction="column" gap={1}>
    {result.parseError && <Alert severity="warning" title="Parse error" bottomSpacing={0}>{result.parseError}</Alert>}

    {/* Parse step: fields snapshot right after parsing, before any stage. */}
    <div className={cx(styles.step, stepping && activeIndex === -1 && styles.active)}>
      <Stack gap={1} alignItems="center"><Text weight="medium" variant="bodySmall">Parsed</Text><Badge text="ingress" color="darkgrey" /></Stack>
      <FieldsDump fields={result.parsedFields} />
    </div>

    {(result.stages ?? []).map((stage, index) => {
      const duration = stage.durationUs ? Number(stage.durationUs) : 0
      return <div key={stage.id} className={cx(styles.step, stepping && activeIndex === index && styles.active)}>
        <div className={styles.stepHeader}>
          <Stack gap={1} alignItems="center"><Text weight="medium" variant="bodySmall">{stage.kind}</Text>{stage.type && <Badge text={stage.type} color="blue" />}</Stack>
          <Stack gap={1} alignItems="center">{duration > 0 && <Badge color="darkgrey" icon="clock-nine" text={formatDuration(duration)} />}<Badge text={stage.pass ? 'pass' : 'drop'} color={stage.pass ? 'green' : 'red'} /></Stack>
        </div>
        {stage.error && <pre className={styles.error}>{stage.error}</pre>}
        {stage.stdout && <div className={styles.stdout}><Text variant="bodySmall" color="secondary">stdout</Text><pre className={styles.pre}>{stage.stdout}</pre></div>}
        <FieldsDump fields={stage.fields} />
      </div>
    })}

    {/* Final outcome. */}
    {result.dropped ? <div className={cx(styles.step, styles.dropped)}>
      <Stack gap={1} alignItems="center" wrap="wrap">
        <Badge text="dropped" color="red" />
        {result.droppedBy && <Text color="secondary" variant="bodySmall">by {result.droppedBy}</Text>}
        {result.deadLetter && <Badge text="→ dead-letter sink" color="orange" />}
      </Stack>
    </div> : <div className={styles.step}>
      <Text color="secondary" variant="bodySmall">Dry run — nothing was actually sent.</Text>
      <Stack direction="column" gap={0.5}>
        {(result.sinks ?? []).map(sink => <Stack key={sink.index} gap={1} alignItems="center">
          <Text variant="bodySmall">{sink.type}</Text>
          <Badge text={sink.sent ? 'would send' : 'skipped'} color={sink.sent ? 'green' : 'darkgrey'} />
          {!sink.sent && sink.reason && <span className={styles.reason} title={sink.reason}>{sink.reason}</span>}
        </Stack>)}
        {(result.sinks ?? []).length === 0 && <Text color="secondary" variant="bodySmall">No sinks matched.</Text>}
      </Stack>
    </div>}
  </Stack>
}

function FieldsDump({ fields }: { fields: Record<string, unknown> }) {
  const styles = useStyles2(getStyles)
  return <pre className={styles.pre}>{JSON.stringify(fields, null, 2)}</pre>
}

const getStyles = (theme: GrafanaTheme2) => {
  const mono = { margin: 0, fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, whiteSpace: 'pre-wrap' as const, wordBreak: 'break-all' as const }
  return {
    step: css({ display: 'flex', flexDirection: 'column', gap: theme.spacing(0.5), padding: theme.spacing(1), border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, background: theme.colors.background.primary }),
    active: css({ borderColor: theme.colors.warning.border, background: theme.colors.warning.transparent }),
    dropped: css({ borderColor: theme.colors.error.border, background: theme.colors.error.transparent }),
    stepHeader: css({ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: theme.spacing(1) }),
    pre: css({ ...mono, color: theme.colors.text.secondary }),
    error: css({ ...mono, color: theme.colors.error.text }),
    stdout: css({ padding: theme.spacing(0.5, 1), borderLeft: `2px solid ${theme.colors.border.medium}`, background: theme.colors.background.canvas }),
    reason: css({ ...mono, color: theme.colors.text.secondary, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }),
  }
}
