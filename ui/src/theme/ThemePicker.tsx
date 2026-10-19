import { css, cx } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import type { ReactNode } from 'react'
import { Toggletip, ToolbarButton, Tooltip, useStyles2 } from '@grafana/ui'
import { accentShade, createGoFiTheme } from '../grafana-theme'
import { ACCENTS, findAccent, findTheme, THEMES } from './themes'
import { useThemeDefinition, useThemePreference } from './ThemeProvider'
import type { AccentDefinition, SyntaxColors, ThemeDefinition } from './types'
import { LOCALES, t, useLocale } from '../i18n'

// Monaco's vs-dark / vs token colors, for previewing the stock Grafana themes.
const STOCK_SYNTAX: Record<'dark' | 'light', Pick<SyntaxColors, 'comment' | 'keyword' | 'string' | 'function'>> = {
  dark: { comment: '#6a9955', keyword: '#569cd6', string: '#ce9178', function: '#dcdcaa' },
  light: { comment: '#008000', keyword: '#0000ff', string: '#a31515', function: '#795e26' },
}

/** Top-bar appearance picker: theme (System or a named theme) and accent. */
export function ThemePicker() {
  const styles = useStyles2(getStyles)
  const definition = useThemeDefinition()
  const [preference, setTheme, setAccent] = useThemePreference()
  const [locale, setLocale] = useLocale()
  const accent = findAccent(preference.accent)
  const label = preference.theme === 'system'
    ? t('gofi.theme.picker.appearance-system-label', 'Appearance: System ({{theme}})', { theme: definition.name })
    : t('gofi.theme.picker.appearance-label', 'Appearance: {{theme}}', { theme: definition.name })

  const tile = (id: string, name: string, preview: ReactNode) => {
    const selected = preference.theme === id
    return <button key={id} type="button" className={cx(styles.tile, selected && styles.tileSelected)}
      aria-pressed={selected} onClick={() => setTheme(id)}>
      <span className={styles.previewRow}>{preview}</span>
      <span className={styles.tileName}>{name}</span>
    </button>
  }
  const group = (mode: 'dark' | 'light', title: string, label: string) => <section className={styles.section} aria-label={label}>
    <h4 className={styles.subheading}>{title}</h4>
    <div className={styles.grid}>
      {THEMES.filter(theme => theme.mode === mode).map(theme =>
        tile(theme.id, theme.name, <Preview definition={theme} accent={accent} />))}
    </div>
  </section>

  const content = <div className={styles.content}>
    <h3 className={styles.heading}>{t('gofi.theme.picker.theme', 'Theme')}</h3>
    <div className={styles.grid}>
      {tile('system', t('gofi.theme.picker.system', 'System'), <>
        <Preview definition={findTheme('grafana-dark')!} accent={accent} />
        <Preview definition={findTheme('grafana-light')!} accent={accent} />
      </>)}
    </div>
    {group('dark', t('gofi.theme.picker.dark', 'Dark'), t('gofi.theme.picker.dark-themes', 'Dark themes'))}
    {group('light', t('gofi.theme.picker.light', 'Light'), t('gofi.theme.picker.light-themes', 'Light themes'))}
    <h3 className={styles.heading}>{t('gofi.theme.picker.accent', 'Accent')}</h3>
    <div className={styles.swatches} role="group" aria-label={t('gofi.theme.picker.accent-color', 'Accent color')}>
      {ACCENTS.map(option => {
        const selected = preference.accent === option.id
        const isDefault = !option[definition.mode]
        const name = isDefault ? t('gofi.theme.picker.theme-default-accent', 'Theme default ({{theme}})', { theme: definition.name }) : option.name
        return <Tooltip key={option.id} content={name} placement="top">
          <button type="button" aria-label={name} aria-pressed={selected}
            className={cx(styles.swatch, isDefault && styles.swatchDefault, selected && styles.swatchSelected)}
            style={{ background: accentShade(definition, option).main }}
            onClick={() => setAccent(option.id)} />
        </Tooltip>
      })}
    </div>
    <h3 className={styles.heading}>{t('gofi.theme.picker.language', 'Language')}</h3>
    {/* Native names on purpose: a user who cannot read the current language
        still finds their own. Switching remounts the app (I18nProvider), so
        this popover closes after a pick. */}
    <div className={styles.grid} role="group" aria-label={t('gofi.theme.picker.language', 'Language')}>
      {LOCALES.map(option => {
        const selected = locale === option.tag
        return <button key={option.tag} type="button" lang={option.tag} aria-pressed={selected}
          className={cx(styles.tile, styles.languageTile, selected && styles.tileSelected)}
          onClick={() => { void setLocale(option.tag) }}>
          <span className={styles.tileName}>{option.nativeName}</span>
          {option.nativeName !== option.englishName && <span className={styles.languageEnglish} lang="en">{option.englishName}</span>}
        </button>
      })}
    </div>
  </div>

  return <Toggletip title={t('gofi.theme.picker.appearance', 'Appearance')} placement="bottom-end" content={content}>
    <ToolbarButton icon="palette" aria-label={label} tooltip={label} />
  </Toggletip>
}

/** A miniature editor in the theme's colors: three code lines and an
 * accent-colored button. */
function Preview({ definition, accent }: { definition: ThemeDefinition; accent: AccentDefinition | undefined }) {
  const styles = useStyles2(getStyles)
  const colors = createGoFiTheme(definition, accent).colors
  const syntax = definition.syntax ?? STOCK_SYNTAX[definition.mode]
  const bar = (color: string, width: number) => <span className={styles.bar} style={{ background: color, width: `${width}%` }} />
  return <span className={styles.preview} aria-hidden
    style={{ background: colors.background.primary, borderColor: colors.border.weak }}>
    <span className={styles.line}>{bar(syntax.keyword, 24)}{bar(syntax.function, 46)}</span>
    <span className={styles.line}>{bar(colors.text.primary, 18)}{bar(syntax.string, 52)}</span>
    <span className={styles.line}>{bar(syntax.comment, 58)}</span>
    <span className={styles.accentPill} style={{ background: colors.primary.main }} />
  </span>
}

const getStyles = (theme: GrafanaTheme2) => ({
  content: css({
    width: 336,
    maxWidth: '100%',
    maxHeight: 'min(70vh, 560px)',
    overflowY: 'auto',
    paddingRight: theme.spacing(0.5),
  }),
  heading: css({
    ...theme.typography.h6,
    margin: theme.spacing(0, 0, 1),
    '&:not(:first-child)': { marginTop: theme.spacing(2) },
  }),
  section: css({ marginTop: theme.spacing(1.5) }),
  subheading: css({
    ...theme.typography.bodySmall,
    color: theme.colors.text.secondary,
    fontWeight: theme.typography.fontWeightMedium,
    margin: theme.spacing(0, 0, 0.75),
  }),
  grid: css({
    display: 'grid',
    gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
    gap: theme.spacing(1),
  }),
  tile: css({
    display: 'flex',
    flexDirection: 'column',
    gap: theme.spacing(0.75),
    padding: theme.spacing(0.75),
    background: theme.colors.background.secondary,
    border: `1px solid ${theme.colors.border.weak}`,
    borderRadius: theme.shape.radius.default,
    color: theme.colors.text.primary,
    textAlign: 'left',
    minWidth: 0,
    [theme.transitions.handleMotion('no-preference')]: {
      transition: theme.transitions.create(['border-color', 'background-color'], { duration: theme.transitions.duration.short }),
    },
    '&:hover': { borderColor: theme.colors.border.strong, background: theme.colors.action.hover },
  }),
  tileSelected: css({
    borderColor: theme.colors.primary.border,
    boxShadow: `inset 0 0 0 1px ${theme.colors.primary.border}`,
    '&:hover': { borderColor: theme.colors.primary.border },
  }),
  tileName: css({
    ...theme.typography.bodySmall,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  }),
  previewRow: css({ display: 'flex', gap: theme.spacing(0.5) }),
  preview: css({
    position: 'relative',
    flex: 1,
    display: 'flex',
    flexDirection: 'column',
    gap: 4,
    height: 44,
    padding: '7px 8px',
    border: '1px solid',
    borderRadius: theme.shape.radius.default,
    overflow: 'hidden',
  }),
  line: css({ display: 'flex', gap: 4 }),
  bar: css({ height: 4, borderRadius: 2, flexShrink: 0 }),
  accentPill: css({
    position: 'absolute',
    right: 6,
    bottom: 6,
    width: 18,
    height: 7,
    borderRadius: 2,
  }),
  swatches: css({
    display: 'flex',
    flexWrap: 'wrap',
    gap: theme.spacing(1.25),
    padding: theme.spacing(0.5, 0.5, 0.5),
  }),
  swatch: css({
    position: 'relative',
    width: 24,
    height: 24,
    padding: 0,
    borderRadius: theme.shape.radius.circle,
    border: `1px solid ${theme.colors.border.medium}`,
  }),
  // The theme-default swatch is hollow in the middle, marking it as "auto".
  swatchDefault: css({
    '&::after': {
      content: '""',
      position: 'absolute',
      inset: 7,
      borderRadius: theme.shape.radius.circle,
      background: theme.colors.background.primary,
    },
  }),
  swatchSelected: css({
    boxShadow: `0 0 0 2px ${theme.colors.background.primary}, 0 0 0 4px ${theme.colors.text.primary}`,
  }),
  languageTile: css({ gap: theme.spacing(0.25), padding: theme.spacing(1) }),
  languageEnglish: css({
    ...theme.typography.bodySmall,
    color: theme.colors.text.secondary,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  }),
})
