import { Fragment, useMemo } from 'react'
import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { useStyles2 } from '@grafana/ui'
import { navigate } from '../../router'
import { linkKind, parseBlocks, parseInline, type Alignment, type Block, type Inline } from './markdown'
import { codeLanguage } from './helpers'
import { CodeBlock } from './CodeBlock'

/** Renders an assistant reply with the in-house Markdown subset. Hash links
 * navigate in-app; http(s) links open a new tab; other schemes stay text. */
export function Markdown({ text }: { text: string }) {
  const styles = useStyles2(getStyles)
  const blocks = useMemo(() => parseBlocks(text), [text])
  return <div className={styles.root}>{blocks.map((block, index) => <BlockView key={index} block={block} />)}</div>
}

function BlockView({ block }: { block: Block }) {
  const styles = useStyles2(getStyles)
  switch (block.type) {
    case 'code': return <div className={styles.codeBlock}><CodeBlock source={block.code} language={codeLanguage(block.language)} label={block.language || undefined} /></div>
    case 'heading': {
      const Tag = (`h${Math.min(6, block.level + 2)}` as 'h3' | 'h4' | 'h5' | 'h6')
      return <Tag className={styles.heading}><InlineText text={block.text} /></Tag>
    }
    case 'list': {
      const items = block.items.map((item, index) => <li key={index}><InlineText text={item} /></li>)
      return block.ordered ? <ol className={styles.list}>{items}</ol> : <ul className={styles.list}>{items}</ul>
    }
    case 'table': {
      const cellStyle = (align: Alignment) => align ? { textAlign: align } : undefined
      return <div className={styles.tableWrap}>
        <table className={styles.table}>
          <thead><tr>{block.header.map((cell, index) => <th key={index} style={cellStyle(block.align[index])}><InlineText text={cell} /></th>)}</tr></thead>
          <tbody>{block.rows.map((row, rowIndex) => <tr key={rowIndex}>{row.map((cell, index) => <td key={index} style={cellStyle(block.align[index])}><InlineText text={cell} /></td>)}</tr>)}</tbody>
        </table>
      </div>
    }
    default: return <p className={styles.paragraph}><InlineText text={block.text} /></p>
  }
}

function InlineText({ text }: { text: string }) {
  const runs = useMemo(() => parseInline(text), [text])
  return <Runs runs={runs} />
}

function Runs({ runs }: { runs: Inline[] }) {
  const styles = useStyles2(getStyles)
  return <>{runs.map((run, index) => {
    switch (run.type) {
      case 'code': return <code key={index} className={styles.code}>{run.text}</code>
      case 'strong': return <strong key={index}><Runs runs={run.children} /></strong>
      case 'link': {
        const kind = linkKind(run.href)
        if (kind === 'app') return <a key={index} href={run.href} className={styles.link} onClick={event => { event.preventDefault(); navigate(run.href) }}><Runs runs={run.children} /></a>
        if (kind === 'external') return <a key={index} href={run.href} className={styles.link} target="_blank" rel="noopener noreferrer"><Runs runs={run.children} /></a>
        return <Fragment key={index}><Runs runs={run.children} /></Fragment>
      }
      default: return <Fragment key={index}>{run.text}</Fragment>
    }
  })}</>
}

const getStyles = (theme: GrafanaTheme2) => ({
  root: css({ minWidth: 0, overflowWrap: 'anywhere', '& > :first-child': { marginTop: 0 }, '& > :last-child': { marginBottom: 0 } }),
  paragraph: css({ margin: theme.spacing(0, 0, 1), whiteSpace: 'pre-wrap' }),
  heading: css({ margin: theme.spacing(1.5, 0, 0.5), fontSize: theme.typography.h5.fontSize, fontWeight: theme.typography.fontWeightMedium }),
  // Explicit markers: the app's base styles reset list-style.
  list: css({ margin: theme.spacing(0, 0, 1), paddingLeft: theme.spacing(2.5), '&ul': { listStyleType: 'disc' }, '&ol': { listStyleType: 'decimal' }, li: { whiteSpace: 'pre-wrap', marginBottom: theme.spacing(0.25), display: 'list-item' } }),
  codeBlock: css({ margin: theme.spacing(0.5, 0, 1) }),
  // Wide tables scroll sideways inside the reply instead of widening the drawer.
  tableWrap: css({ margin: theme.spacing(0.5, 0, 1), maxWidth: '100%', overflowX: 'auto' }),
  table: css({
    borderCollapse: 'collapse', fontSize: theme.typography.bodySmall.fontSize, lineHeight: theme.typography.bodySmall.lineHeight,
    'th, td': { padding: theme.spacing(0.5, 1), border: `1px solid ${theme.colors.border.weak}`, verticalAlign: 'top', textAlign: 'left', whiteSpace: 'pre-wrap', overflowWrap: 'normal', minWidth: theme.spacing(8) },
    th: { background: theme.colors.background.secondary, fontWeight: theme.typography.fontWeightMedium, color: theme.colors.text.primary },
    'tbody tr:nth-of-type(even) td': { background: theme.colors.background.canvas },
  }),
  code: css({ fontFamily: theme.typography.fontFamilyMonospace, fontSize: '0.9em', padding: theme.spacing(0, 0.5), background: theme.colors.background.canvas, border: `1px solid ${theme.colors.border.weak}`, borderRadius: theme.shape.radius.default, color: theme.colors.text.primary }),
  link: css({ color: theme.colors.text.link, textDecoration: 'underline', '&:hover': { color: theme.colors.text.primary } }),
})
