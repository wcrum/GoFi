import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Combobox, Field, Stack, Text, Toggletip, ToolbarButton, useStyles2 } from '@grafana/ui'
import type { FlowSettings as FlowSettingsValue } from './convert'
import { useLibraryFunctions } from './python-functions'
import { formatNumber, t } from '../../i18n'

/** Flow-wide settings that are not a node on the graph, in a toolbar
 * toggletip: today the default Python library. */
export function FlowSettingsButton({ value, onChange, readOnly, defaultRefs }: {
  value: FlowSettingsValue
  onChange: (value: FlowSettingsValue) => void
  readOnly?: boolean
  /** Python function refs in the flow that omit their library. */
  defaultRefs: number
}) {
  const styles = useStyles2(getStyles)
  const { functions, error } = useLibraryFunctions()
  const current = value.python?.library ?? ''
  const libraries = [...new Set(functions.map((fn) => fn.library))].sort()
  if (current && !libraries.includes(current)) libraries.unshift(current)
  const functionCount = (library: string) => functions.filter((fn) => fn.library === library).length
  const options = libraries.map((library) => ({ value: library, label: library, description: library === current && !functions.some((fn) => fn.library === library) ? t('gofi.canvas.flow-settings.not-launched', 'Not launched') : t('gofi.canvas.flow-settings.function-count', '{{n}} functions', { count: functionCount(library), n: formatNumber(functionCount(library)), defaultValue_one: '{{n}} function' }) }))
  const usage = !current ? t('gofi.canvas.flow-settings.usage-none', 'Python functions name their library.')
    : defaultRefs ? t('gofi.canvas.flow-settings.usage-refs', '{{n}} Python functions in this flow use {{library}}. Removing the default writes it into them.', { count: defaultRefs, n: formatNumber(defaultRefs), library: current, defaultValue_one: '{{n}} Python function in this flow uses {{library}}. Removing the default writes it into them.' })
    : t('gofi.canvas.flow-settings.usage-default', 'Functions you pick from {{library}} omit the library.', { library: current })
  const content = <div className={styles.content}>
    <Stack direction="column" gap={1}>
      <Text element="h3" variant="h6">{t('gofi.canvas.flow-settings.title', 'Flow settings')}</Text>
      <Field label={t('gofi.canvas.flow-settings.default-library', 'Default Python library')} description={t('gofi.canvas.flow-settings.default-library-description', "Python framers, decoders, parsers, stages, and sinks that don't name a library run in this one.")} noMargin>
        <Combobox aria-label={t('gofi.canvas.flow-settings.default-library', 'Default Python library')} disabled={readOnly} isClearable createCustomValue placeholder={t('gofi.common.none', 'None')} value={current || null} options={options}
          onChange={(option) => onChange({ ...value, python: option?.value ? { library: option.value } : undefined })} />
      </Field>
      <Text variant="bodySmall" color="secondary">{usage}</Text>
      {error && <Text variant="bodySmall" color="warning">{t('gofi.canvas.registry-unavailable', 'Python function registry unavailable.')}</Text>}
    </Stack>
  </div>
  return <Toggletip content={content} placement="bottom-end" closeButton={false}>
    <ToolbarButton icon="cog" aria-label={t('gofi.canvas.flow-settings.title', 'Flow settings')} tooltip={t('gofi.canvas.flow-settings.title', 'Flow settings')}>{current ? t('gofi.canvas.flow-settings.python-library-button', 'Python: {{library}}', { library: current }) : t('gofi.canvas.flow-settings.settings-button', 'Settings')}</ToolbarButton>
  </Toggletip>
}

const getStyles = (theme: GrafanaTheme2) => ({
  content: css({ width: theme.spacing(40), maxWidth: '80vw' }),
})
