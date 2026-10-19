import { formatRelativeTime, t } from '../i18n/index.ts'

/** Where a flow revision came from (history.proto). */
export type RevisionSource = 'portal' | 'external' | 'rollback' | 'import'
type BadgeColor = 'blue' | 'red' | 'green' | 'orange' | 'purple' | 'darkgrey' | 'brand'

export interface SourceBadge { text: string; color: BadgeColor; tooltip: string }

/** Badge text, color, and tooltip per revision source. Translated at call
 * time so a language switch is picked up. */
function sourceBadges(): Record<RevisionSource, SourceBadge> {
  return {
    portal: { text: t('gofi.lib.revisions.source-portal', 'Portal'), color: 'blue', tooltip: t('gofi.lib.revisions.source-portal-tooltip', 'Saved through GoFi') },
    external: { text: t('gofi.lib.revisions.source-external', 'External'), color: 'orange', tooltip: t('gofi.lib.revisions.source-external-tooltip', 'Changed outside GoFi (kubectl, GitOps, or the flow file)') },
    rollback: { text: t('gofi.lib.revisions.source-rollback', 'Restore'), color: 'purple', tooltip: t('gofi.lib.revisions.source-rollback-tooltip', 'An earlier revision restored through GoFi') },
    import: { text: t('gofi.lib.revisions.source-import', 'Imported'), color: 'darkgrey', tooltip: t('gofi.lib.revisions.source-import-tooltip', 'The flow as GoFi first found it') },
  }
}
export function sourceBadge(source: string): SourceBadge {
  return sourceBadges()[source as RevisionSource] ?? { text: source || t('gofi.lib.revisions.source-unknown', 'unknown'), color: 'darkgrey' as const, tooltip: '' }
}

/** First 8 hex digits, as Git abbreviates commits. */
export const shortHash = (hash: string) => hash.slice(0, 8)

/** "just now", "5 minutes ago", "yesterday", "3 weeks ago". */
export function relativeTime(date: Date, now: Date = new Date()): string {
  const seconds = Math.round((date.getTime() - now.getTime()) / 1000)
  if (Math.abs(seconds) < 45) return t('gofi.lib.revisions.just-now', 'just now')
  return formatRelativeTime(date, now)
}

/** What the selected revision is compared with. */
export type CompareMode = 'previous' | 'current'

interface RevisionLike { revision: number; deleted: boolean }

/** The revision to show on the left of a "previous" diff: the newest older
 * revision that holds a definition (tombstones are skipped). revisions is
 * newest first, as the API lists them. undefined means none: the selected
 * revision is compared with an empty document. */
export function previousRevision<T extends RevisionLike>(revisions: T[], selected: number): T | undefined {
  return revisions.find(rev => rev.revision < selected && !rev.deleted)
}

/** "saved by ada" / "saved" and the equivalents for the other sources. */
function revisionAction(source: string, author: string): string {
  const named = !!author && author !== 'unknown'
  switch (source) {
    case 'external': return named ? t('gofi.lib.revisions.action-external-by', 'changed outside GoFi by {{author}}', { author }) : t('gofi.lib.revisions.action-external', 'changed outside GoFi')
    case 'import': return named ? t('gofi.lib.revisions.action-import-by', 'imported by {{author}}', { author }) : t('gofi.lib.revisions.action-import', 'imported')
    case 'rollback': return named ? t('gofi.lib.revisions.action-rollback-by', 'restored by {{author}}', { author }) : t('gofi.lib.revisions.action-rollback', 'restored')
    default: return named ? t('gofi.lib.revisions.action-saved-by', 'saved by {{author}}', { author }) : t('gofi.lib.revisions.action-saved', 'saved')
  }
}

/** Header text for a flow's latest revision: "Revision 7 · saved by ada, 5 minutes ago". */
export function revisionStamp(rev: { revision: number; author: string; source: string; createdAt: Date }, now: Date = new Date()): string {
  return t('gofi.lib.revisions.stamp', 'Revision {{revision}} · {{action}}, {{when}}', { revision: rev.revision, action: revisionAction(rev.source, rev.author), when: relativeTime(rev.createdAt, now) })
}
