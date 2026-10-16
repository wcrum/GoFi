import { useState } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Alert, Badge, Button, Icon, Stack, Text, TextLink, Tooltip, useStyles2 } from '@grafana/ui'
import type { ResultType } from '../../expression-api'
import type { ExpressionRef } from '../../types'
import { paths, to } from '../../router'
import { CelEditor } from './CelEditor'
import { LibraryPicker } from './LibraryPicker'
import { useExpressions } from './useExpressions'

export interface CelFieldProps {
  value: string
  onChange: (value: string) => void
  resultType: ResultType
  disabled?: boolean
  placeholder?: string
  fieldNames?: string[]
  'aria-label'?: string
  /** Library link for this expression slot; omit both to hide linking. */
  libraryRef?: ExpressionRef
  onLibraryRefChange?: (ref: ExpressionRef | undefined) => void
}

/** A CEL input that can be typed directly or linked to a library entry.
 * A literal expression takes precedence over a link (server rule), so
 * linking clears the literal and the linked state replaces the editor. */
export function CelField({ value, onChange, resultType, disabled, placeholder, fieldNames, 'aria-label': ariaLabel, libraryRef, onLibraryRefChange }: CelFieldProps) {
  const styles = useStyles2(getStyles)
  const [picking, setPicking] = useState(false)
  const linkable = !!onLibraryRefChange
  const linked = linkable && libraryRef && !value
  const { expressions } = useExpressions(!!libraryRef)
  const entry = libraryRef ? expressions.find(e => e.name === libraryRef.name) : undefined
  const pinned = !!libraryRef?.generation
  const linkedText = !entry ? undefined : !pinned || libraryRef?.generation === entry.generation ? entry.expression : entry.pinnedRevisions?.find(r => r.generation === libraryRef?.generation)?.expression

  return <div className={styles.field}>
    {linked ? <div className={styles.linked}>
      <Stack justifyContent="space-between" alignItems="center" wrap="wrap" gap={1}>
        <Stack gap={1} alignItems="center">
          <Icon name="link" />
          <TextLink href={to(paths.expressions(libraryRef!.name))} inline={false} weight="medium">{libraryRef!.name}</TextLink>
          {pinned ? <Tooltip content="Pinned: later library edits do not reach this stage."><span><Badge color="orange" text={`pinned · gen ${libraryRef!.generation}`} icon="lock" /></span></Tooltip>
            : <Tooltip content="Follows the latest generation: library edits reload this flow."><span><Badge color="green" text={`latest${entry ? ` · gen ${entry.generation}` : ''}`} icon="sync" /></span></Tooltip>}
        </Stack>
        {!disabled && <Stack gap={0.5}>
          {pinned ? <Button size="sm" variant="secondary" fill="text" onClick={() => onLibraryRefChange!({ name: libraryRef!.name })}>Follow latest</Button>
            : <Button size="sm" variant="secondary" fill="text" disabled={!entry} onClick={() => entry && onLibraryRefChange!({ name: entry.name, generation: entry.generation })}>Pin</Button>}
          <Button size="sm" variant="secondary" fill="text" icon="times" tooltip="Copy the current text into this stage and stop following the library" onClick={() => { onLibraryRefChange!(undefined); if (linkedText) onChange(linkedText) }}>Unlink</Button>
        </Stack>}
      </Stack>
      {linkedText !== undefined ? <code className={styles.code}>{linkedText}</code>
        : <Text color={entry || !expressions.length ? 'secondary' : 'error'} variant="bodySmall">{entry ? `Generation ${libraryRef!.generation} is no longer retained.` : expressions.length ? 'This library entry does not exist.' : 'Loading library entry...'}</Text>}
    </div> : <>
      <CelEditor value={value} onChange={onChange} resultType={resultType} disabled={disabled} placeholder={placeholder} fieldNames={fieldNames} aria-label={ariaLabel} />
      {linkable && libraryRef && value && <Alert severity="info" title={`The typed expression overrides linked "${libraryRef.name}"`} bottomSpacing={0} topSpacing={1}>
        <Stack gap={1}><Button size="sm" variant="secondary" onClick={() => onChange('')}>Use library entry</Button><Button size="sm" variant="secondary" fill="text" onClick={() => onLibraryRefChange!(undefined)}>Remove link</Button></Stack>
      </Alert>}
    </>}
    {linkable && !disabled && <div className={styles.actions}>
      <Button size="sm" variant="secondary" fill="text" icon="book-open" onClick={() => setPicking(true)}>{linked ? 'Change' : 'Library'}</Button>
    </div>}
    {linkable && <LibraryPicker isOpen={picking} resultType={resultType} onDismiss={() => setPicking(false)}
      onLink={e => { onChange(''); onLibraryRefChange!({ name: e.name }); setPicking(false) }}
      onInsert={e => { onLibraryRefChange!(undefined); onChange(e.expression); setPicking(false) }} />}
  </div>
}

const getStyles = (theme: GrafanaTheme2) => ({
  field: css({ minWidth: 0 }),
  linked: css({ border: `1px solid ${theme.colors.border.medium}`, borderLeft: `3px solid ${theme.colors.primary.main}`, borderRadius: theme.shape.radius.default, padding: theme.spacing(1), background: theme.colors.background.secondary }),
  code: css({ display: 'block', marginTop: theme.spacing(0.75), fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }),
  actions: css({ display: 'flex', justifyContent: 'flex-end', marginTop: theme.spacing(0.25) }),
})
