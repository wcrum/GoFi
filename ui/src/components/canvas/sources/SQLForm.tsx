import { ControlledCollapse, Field, Input, RadioButtonGroup, Stack, Text, TextArea } from '@grafana/ui'
import type { FetchSource, SQLFetch } from '../../../types'
import { KeyValueEditor } from '../KeyValueEditor'
import { durationProblem, SQL_PLACEHOLDERS, sqlQueryProblem } from './validation'
import { FieldRow } from './FieldRow'
import { t } from '../../../i18n'

export const DEFAULT_SQL: SQLFetch = { driver: 'postgres', host: '', query: 'SELECT * FROM events WHERE id > $1 ORDER BY id', cursorColumn: 'id', cursorStart: '0', every: '30s', timeout: '10s' }

const DRIVER_OPTIONS: Array<{ value: SQLFetch['driver']; label: string }> = [
  { value: 'postgres', label: 'PostgreSQL' },
  { value: 'mysql', label: 'MySQL' },
  { value: 'sqlserver', label: 'SQL Server' },
]
const DEFAULT_PORTS = { postgres: 5432, mysql: 3306, sqlserver: 1433 } as const
const tlsOptions = (): Array<{ value: NonNullable<SQLFetch['tls']>; label: string }> => [
  { value: '', label: t('gofi.canvas.sql-form.tls-driver-default', 'Driver default') },
  { value: 'disable', label: t('gofi.canvas.sql-form.tls-off', 'Off') },
  { value: 'require', label: t('gofi.canvas.sql-form.tls-require', 'Require') },
  { value: 'verify-full', label: t('gofi.canvas.sql-form.tls-verify', 'Verify') },
]

/** `fetch.sql`: one JSON frame per row, optionally incremental by a cursor column. */
export function SQLForm({ fetch, readOnly, onChange }: { fetch: FetchSource; readOnly?: boolean; onChange: (fetch: FetchSource) => void }) {
  const sql = fetch.sql ?? DEFAULT_SQL
  const set = (next: Partial<SQLFetch>) => onChange({ ...fetch, split: undefined, sql: { ...sql, ...next } })
  const useDSN = sql.dsn !== undefined
  const placeholder = SQL_PLACEHOLDERS[sql.driver]
  const queryProblem = sqlQueryProblem(sql.query, sql.driver, sql.cursorColumn)
  const switchDriver = (driver: SQLFetch['driver']) => {
    const from = SQL_PLACEHOLDERS[sql.driver]
    set({ driver, query: from !== SQL_PLACEHOLDERS[driver] ? sql.query.split(from).join(SQL_PLACEHOLDERS[driver]) : sql.query })
  }
  return <>
    <Field noMargin label={t('gofi.canvas.sql-form.database', 'Database')}>
      <RadioButtonGroup disabled={readOnly} value={sql.driver} options={DRIVER_OPTIONS} onChange={switchDriver} />
    </Field>
    <Field noMargin label={t('gofi.canvas.sql-form.connection', 'Connection')}>
      <RadioButtonGroup disabled={readOnly} value={useDSN ? 'dsn' : 'fields'} options={[{ value: 'fields', label: t('gofi.canvas.sql-form.host-and-credentials', 'Host and credentials') }, { value: 'dsn', label: 'DSN' }]}
        onChange={(mode) => set(mode === 'dsn' ? { dsn: '', host: undefined, port: undefined, database: undefined, username: undefined, password: undefined, tls: undefined, params: undefined } : { dsn: undefined, host: '' })} />
    </Field>
    {useDSN
      ? <Field noMargin label="DSN" description={t('gofi.canvas.sql-form.dsn-description', "The driver's own connection string. It usually contains the password and is stored in the flow definition.")} invalid={!sql.dsn} error={sql.dsn ? undefined : t('gofi.common.required', 'Required.')}>
        <Input disabled={readOnly} type="password" value={sql.dsn ?? ''} placeholder={sql.driver === 'mysql' ? 'user:pass@tcp(db:3306)/app' : sql.driver === 'sqlserver' ? 'sqlserver://user:pass@db:1433?database=app' : 'postgres://user:pass@db:5432/app'} onChange={(e) => set({ dsn: e.currentTarget.value })} />
      </Field>
      : <>
        <FieldRow>
          <Field noMargin label={t('gofi.canvas.sql-form.host', 'Host')} invalid={!sql.host} error={sql.host ? undefined : t('gofi.common.required', 'Required.')}>
            <Input disabled={readOnly} value={sql.host ?? ''} placeholder="postgres.db.svc.cluster.local" onChange={(e) => set({ host: e.currentTarget.value })} />
          </Field>
          <Field noMargin label={t('gofi.common.port', 'Port')}>
            <Input disabled={readOnly} type="number" value={sql.port ?? ''} placeholder={String(DEFAULT_PORTS[sql.driver])} onChange={(e) => set({ port: Number(e.currentTarget.value) || undefined })} />
          </Field>
        </FieldRow>
        <Field noMargin label={t('gofi.canvas.sql-form.database-name', 'Database name')}>
          <Input disabled={readOnly} value={sql.database ?? ''} onChange={(e) => set({ database: e.currentTarget.value || undefined })} />
        </Field>
        <FieldRow>
          <Field noMargin label={t('gofi.common.username', 'Username')} description={t('gofi.canvas.sql-form.username-description', 'Use a read-only user.')}>
            <Input disabled={readOnly} value={sql.username ?? ''} onChange={(e) => set({ username: e.currentTarget.value || undefined })} />
          </Field>
          <Field noMargin label={t('gofi.common.password', 'Password')}>
            <Input disabled={readOnly} type="password" value={sql.password ?? ''} onChange={(e) => set({ password: e.currentTarget.value || undefined })} />
          </Field>
        </FieldRow>
        <Field noMargin label="TLS">
          <RadioButtonGroup disabled={readOnly} value={sql.tls ?? ''} options={tlsOptions()} onChange={(tls) => set({ tls: tls || undefined })} />
        </Field>
      </>}
    <Field noMargin label={t('gofi.canvas.sql-form.query', 'Query')} description={`${t('gofi.canvas.sql-form.query-description', 'Each row becomes one JSON object frame.')} ${sql.cursorColumn ? t('gofi.canvas.sql-form.query-cursor-hint', 'Bind the last cursor value with {{placeholder}} and ORDER BY {{column}}.', { placeholder, column: sql.cursorColumn }) : t('gofi.canvas.sql-form.query-no-cursor-hint', 'Without a cursor column every poll re-reads the same rows.')}`} invalid={!!queryProblem} error={queryProblem}>
      <TextArea disabled={readOnly} rows={4} value={sql.query} placeholder={`SELECT * FROM events WHERE id > ${placeholder} ORDER BY id`} onChange={(e) => set({ query: e.currentTarget.value })} />
    </Field>
    <FieldRow>
      <Field noMargin label={t('gofi.canvas.sql-form.cursor-column', 'Cursor column (optional)')} description={t('gofi.canvas.sql-form.cursor-column-description', 'A unique, increasing column such as id.')}>
        <Input disabled={readOnly} value={sql.cursorColumn ?? ''} placeholder={'id' /* i18n-ignore: example identifier */} onChange={(e) => set({ cursorColumn: e.currentTarget.value || undefined, cursorStart: e.currentTarget.value ? sql.cursorStart : undefined })} />
      </Field>
      {sql.cursorColumn && <Field noMargin label={t('gofi.canvas.sql-form.start-after', 'Start after')} description={t('gofi.canvas.sql-form.start-after-description', 'An integer, RFC 3339 time, or text.')} invalid={!sql.cursorStart} error={sql.cursorStart ? undefined : t('gofi.common.required', 'Required.')}>
        <Input disabled={readOnly} value={sql.cursorStart ?? ''} placeholder="0" onChange={(e) => set({ cursorStart: e.currentTarget.value || undefined })} />
      </Field>}
    </FieldRow>
    <FieldRow>
      <Field noMargin label={t('gofi.canvas.sql-form.every', 'Every')} description={t('gofi.canvas.sql-form.polls-never-overlap', 'Polls never overlap.')} invalid={!!durationProblem(sql.every, 1)} error={durationProblem(sql.every, 1)}>
        <Input disabled={readOnly} value={sql.every ?? ''} placeholder="30s" onChange={(e) => set({ every: e.currentTarget.value || undefined })} />
      </Field>
      <Field noMargin label={t('gofi.canvas.sql-form.timeout', 'Timeout')} invalid={!!durationProblem(sql.timeout)} error={durationProblem(sql.timeout)}>
        <Input disabled={readOnly} value={sql.timeout ?? ''} placeholder="10s" onChange={(e) => set({ timeout: e.currentTarget.value || undefined })} />
      </Field>
      <Field noMargin label={t('gofi.canvas.sql-form.rows-per-poll', 'Rows per poll')}>
        <Input disabled={readOnly} type="number" min={1} max={100000} value={sql.maxRows ?? ''} placeholder="1000" onChange={(e) => set({ maxRows: Number(e.currentTarget.value) || undefined })} />
      </Field>
    </FieldRow>
    {!useDSN && <ControlledCollapse label={t('gofi.canvas.sql-form.connection-parameters', 'Connection parameters')} isOpen={Object.keys(sql.params ?? {}).length > 0}>
      <KeyValueEditor disabled={readOnly} value={sql.params ?? {}} keyPlaceholder={t('gofi.canvas.sql-form.parameter-placeholder', 'Parameter')} valuePlaceholder={t('gofi.canvas.sql-form.value-placeholder', 'Value')} onChange={(params) => set({ params: Object.keys(params).length ? params : undefined })} />
    </ControlledCollapse>}
    <Text variant="bodySmall" color="secondary">{t('gofi.canvas.sql-form.read-only-hint', 'PostgreSQL and MySQL polls run in a read-only transaction and GoFi rejects obvious write statements, but neither is a security boundary: connect as a database user that can only read.')}</Text>
  </>
}
