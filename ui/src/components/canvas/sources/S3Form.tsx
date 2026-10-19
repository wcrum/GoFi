import { Checkbox, ControlledCollapse, Field, Input, RadioButtonGroup, Stack, Text, TextArea } from '@grafana/ui'
import type { FetchSource, S3Fetch } from '../../../types'
import { bucketProblem, durationProblem, rfc3339Problem, splitProblem, urlProblem } from './validation'
import { FieldRow } from './FieldRow'
import { t } from '../../../i18n'

export const DEFAULT_S3: S3Fetch = { bucket: '', prefix: '', every: '30s', timeout: '10s' }

type AfterMode = 'keep' | 'delete' | 'move'
const afterOptions = (): Array<{ value: AfterMode; label: string; description: string }> => [
  { value: 'keep', label: t('gofi.canvas.s3-form.after-keep', 'Keep'), description: t('gofi.canvas.s3-form.after-keep-description', 'In-memory cursor: a restart re-reads the prefix unless Since is set') },
  { value: 'delete', label: t('gofi.common.delete', 'Delete'), description: t('gofi.canvas.s3-form.after-delete-description', 'Delete each object after hand-off (at-least-once)') },
  { value: 'move', label: t('gofi.canvas.s3-form.after-move', 'Move'), description: t('gofi.canvas.s3-form.after-move-description', 'Move each object under another prefix after hand-off (at-least-once)') },
]

/** `fetch.s3`: AWS S3 or an S3-compatible store (MinIO, Ceph, GCS interop). */
export function S3Form({ fetch, readOnly, onChange }: { fetch: FetchSource; readOnly?: boolean; onChange: (fetch: FetchSource) => void }) {
  const s3 = fetch.s3 ?? DEFAULT_S3
  const set = (next: Partial<S3Fetch>) => onChange({ ...fetch, s3: { ...s3, ...next } })
  const after: AfterMode = s3.moveTo !== undefined ? 'move' : s3.deleteAfter ? 'delete' : 'keep'
  const moveProblem = after === 'move' && (!s3.moveTo || s3.moveTo.startsWith(s3.prefix ?? '')) ? t('gofi.canvas.s3-form.move-prefix-problem', 'Use a prefix outside the watched prefix, e.g. done/.') : undefined
  const keyProblem = !!s3.accessKeyId !== !!s3.secretAccessKey ? t('gofi.canvas.s3-form.key-pair-problem', 'Set both keys, or neither for ambient credentials.') : undefined
  const options = afterOptions()
  return <>
    <Field noMargin label={t('gofi.canvas.s3-form.endpoint', 'Endpoint (optional)')} description={t('gofi.canvas.s3-form.endpoint-description', 'Leave blank for AWS S3. For MinIO, Ceph, or GCS interop use e.g. https://minio.example:9000.')} invalid={!!(s3.endpoint && urlProblem(s3.endpoint, ['http', 'https']))} error={s3.endpoint ? urlProblem(s3.endpoint, ['http', 'https']) : undefined}>
      <Input disabled={readOnly} value={s3.endpoint ?? ''} placeholder="https://s3.us-east-1.amazonaws.com" onChange={(e) => set({ endpoint: e.currentTarget.value || undefined })} />
    </Field>
    <FieldRow>
      <Field noMargin label={t('gofi.canvas.s3-form.bucket', 'Bucket')} invalid={!!bucketProblem(s3.bucket)} error={bucketProblem(s3.bucket)}>
        <Input disabled={readOnly} value={s3.bucket} placeholder={'telemetry' /* i18n-ignore: example identifier */} onChange={(e) => set({ bucket: e.currentTarget.value })} />
      </Field>
      <Field noMargin label={t('gofi.canvas.s3-form.region', 'Region')}>
        <Input disabled={readOnly} value={s3.region ?? ''} placeholder="us-east-1" onChange={(e) => set({ region: e.currentTarget.value || undefined })} />
      </Field>
    </FieldRow>
    <FieldRow>
      <Field noMargin label={t('gofi.canvas.s3-form.prefix', 'Prefix (optional)')} description={t('gofi.canvas.s3-form.prefix-description', 'Listed recursively.')}>
        <Input disabled={readOnly} value={s3.prefix ?? ''} placeholder="incoming/" onChange={(e) => set({ prefix: e.currentTarget.value || undefined })} />
      </Field>
      <Field noMargin label={t('gofi.canvas.s3-form.key-glob', 'Key glob (optional)')} description={t('gofi.canvas.s3-form.key-glob-description', 'Matched after the prefix; * does not cross /.')}>
        <Input disabled={readOnly} value={s3.glob ?? ''} placeholder="*.json" onChange={(e) => set({ glob: e.currentTarget.value || undefined })} />
      </Field>
    </FieldRow>
    <FieldRow>
      <Field noMargin label={t('gofi.canvas.s3-form.every', 'Every')} description={t('gofi.canvas.s3-form.polls-never-overlap', 'Polls never overlap.')} invalid={!!durationProblem(s3.every, 1)} error={durationProblem(s3.every, 1)}>
        <Input disabled={readOnly} value={s3.every ?? ''} placeholder="30s" onChange={(e) => set({ every: e.currentTarget.value || undefined })} />
      </Field>
      <Field noMargin label={t('gofi.canvas.s3-form.timeout', 'Timeout')} description={t('gofi.canvas.s3-form.timeout-description', 'Per request.')} invalid={!!durationProblem(s3.timeout)} error={durationProblem(s3.timeout)}>
        <Input disabled={readOnly} value={s3.timeout ?? ''} placeholder="10s" onChange={(e) => set({ timeout: e.currentTarget.value || undefined })} />
      </Field>
      <Field noMargin label={t('gofi.canvas.s3-form.objects-per-poll', 'Objects per poll')}>
        <Input disabled={readOnly} type="number" min={1} max={10000} value={s3.maxObjects ?? ''} placeholder="100" onChange={(e) => set({ maxObjects: Number(e.currentTarget.value) || undefined })} />
      </Field>
    </FieldRow>
    <Field noMargin label={t('gofi.canvas.s3-form.split', 'Split (optional)')} description={t('gofi.canvas.s3-form.split-description', 'One frame per element of a JSON array in each object, e.g. $[*]. For one frame per line, use line framing instead.')} invalid={!!splitProblem(fetch.split)} error={splitProblem(fetch.split)}>
      <Input disabled={readOnly} value={fetch.split ?? ''} placeholder="$[*]" onChange={(e) => onChange({ ...fetch, split: e.currentTarget.value || undefined })} />
    </Field>
    <Field noMargin label={t('gofi.canvas.s3-form.after-hand-off', 'After hand-off')} description={options.find((o) => o.value === after)?.description}>
      <RadioButtonGroup disabled={readOnly} value={after} options={options} onChange={(value) => set({ deleteAfter: value === 'delete' || undefined, moveTo: value === 'move' ? (s3.moveTo ?? 'done/') : undefined })} />
    </Field>
    {after === 'move' && <Field noMargin label={t('gofi.canvas.s3-form.move-to-prefix', 'Move to prefix')} invalid={!!moveProblem} error={moveProblem}>
      <Input disabled={readOnly} value={s3.moveTo ?? ''} placeholder="done/" onChange={(e) => set({ moveTo: e.currentTarget.value })} />
    </Field>}
    <Field noMargin label={t('gofi.canvas.s3-form.since', 'Since (optional)')} description={t('gofi.canvas.s3-form.since-description', 'Skip objects last modified at or before this time.')} invalid={!!rfc3339Problem(s3.since)} error={rfc3339Problem(s3.since)}>
      <Input disabled={readOnly} value={s3.since ?? ''} placeholder="2026-01-02T15:04:05Z" onChange={(e) => set({ since: e.currentTarget.value || undefined })} />
    </Field>
    <Text variant="bodySmall" color="secondary">{t('gofi.canvas.s3-form.credentials-hint', 'Leave the keys blank to use ambient credentials (AWS_* environment, IRSA web identity, or instance metadata). Inline keys are stored in the flow definition.')}</Text>
    <FieldRow>
      <Field noMargin label={t('gofi.canvas.s3-form.access-key-id', 'Access key ID')} invalid={!!keyProblem} error={keyProblem}>
        <Input disabled={readOnly} value={s3.accessKeyId ?? ''} onChange={(e) => set({ accessKeyId: e.currentTarget.value || undefined })} />
      </Field>
      <Field noMargin label={t('gofi.canvas.s3-form.secret-access-key', 'Secret access key')}>
        <Input disabled={readOnly} type="password" value={s3.secretAccessKey ?? ''} onChange={(e) => set({ secretAccessKey: e.currentTarget.value || undefined })} />
      </Field>
    </FieldRow>
    <ControlledCollapse label={t('gofi.common.advanced', 'Advanced')} isOpen={!!(s3.sessionToken || s3.pathStyle || s3.insecureSkipVerify || s3.tlsCa)}>
      <Stack direction="column" gap={2}>
        {/* Wrapped so the inline-grid checkbox keeps its own width in the stack. */}
        <div><Checkbox disabled={readOnly} value={s3.pathStyle ?? false} label={t('gofi.canvas.s3-form.path-style', 'Path-style requests')} description={t('gofi.canvas.s3-form.path-style-description', 'Usually required by MinIO and Ceph.')} onChange={(e) => set({ pathStyle: e.currentTarget.checked || undefined })} /></div>
        <Field noMargin label={t('gofi.canvas.s3-form.session-token', 'Session token (optional)')}>
          <Input disabled={readOnly} type="password" value={s3.sessionToken ?? ''} onChange={(e) => set({ sessionToken: e.currentTarget.value || undefined })} />
        </Field>
        <Field noMargin label={t('gofi.canvas.s3-form.ca-certificate', 'CA certificate (PEM, optional)')}>
          <TextArea disabled={readOnly} rows={3} value={s3.tlsCa ?? ''} onChange={(e) => set({ tlsCa: e.currentTarget.value || undefined })} />
        </Field>
        <div><Checkbox disabled={readOnly} value={s3.insecureSkipVerify ?? false} label={t('gofi.canvas.s3-form.skip-tls-verification', 'Skip TLS verification')} description={t('gofi.canvas.s3-form.skip-tls-verification-description', 'Not recommended.')} onChange={(e) => set({ insecureSkipVerify: e.currentTarget.checked || undefined })} /></div>
      </Stack>
    </ControlledCollapse>
  </>
}
