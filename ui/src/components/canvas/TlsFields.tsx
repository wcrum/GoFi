import { css } from '@emotion/css'
import type { GrafanaTheme2 } from '@grafana/data'
import { Checkbox, Field, Input, TagsInput, TextArea, useStyles2 } from '@grafana/ui'
import { t } from '../../i18n'
import type { TLSConfig } from '../../types'

/** ALPN the QUIC listener and sink agree on when none is configured. */
export const DEFAULT_ALPN = ['gofi/1']

export function hasTlsValues(tls: TLSConfig | undefined): boolean {
  if (!tls) return false
  return Boolean(tls.cert || tls.key || tls.ca || tls.insecureSkipVerify || tls.serverName || tls.alpn?.length || tls.requireClientCert)
}

/** TLS for a QUIC endpoint. A listener presents `cert`/`key` and may require
 * client certificates signed by `ca`; a sink verifies the server against `ca`
 * (or `serverName`, or not at all) and may present a client certificate. */
export function TlsFields({ tls, role, readOnly, onChange }: { tls: TLSConfig | undefined; role: 'listen' | 'sink'; readOnly?: boolean; onChange: (tls: TLSConfig | undefined) => void }) {
  const styles = useStyles2(getStyles)
  const value = tls ?? {}
  const set = (next: Partial<TLSConfig>) => {
    const merged: TLSConfig = { ...value, ...next }
    onChange(hasTlsValues(merged) ? merged : undefined)
  }
  const server = role === 'listen'
  return (
    <div className={styles.fields}>
      <Field noMargin label={server ? t('gofi.canvas.tls.server-cert', 'Certificate') : t('gofi.canvas.tls.client-cert', 'Client certificate (optional)')} description={server ? t('gofi.canvas.tls.server-cert-description', 'PEM certificate the listener presents; senders verify it against their CA.') : undefined}>
        <TextArea disabled={readOnly} rows={4} className={styles.mono} value={value.cert ?? ''} onChange={(e) => set({ cert: e.currentTarget.value || undefined })} placeholder="-----BEGIN CERTIFICATE-----" />
      </Field>
      <Field noMargin label={server ? t('gofi.canvas.tls.server-key', 'Private key') : t('gofi.canvas.tls.client-key', 'Client key (optional)')}>
        <TextArea disabled={readOnly} rows={4} className={styles.mono} value={value.key ?? ''} onChange={(e) => set({ key: e.currentTarget.value || undefined })} placeholder="-----BEGIN PRIVATE KEY-----" />
      </Field>
      <Field noMargin label={t('gofi.canvas.tls.ca', 'CA certificate')} description={server ? t('gofi.canvas.tls.ca-listen-description', 'Client certificates are verified against this CA when required below.') : t('gofi.canvas.tls.ca-sink-description', 'The server certificate is verified against this CA; empty uses the system roots.')}>
        <TextArea disabled={readOnly} rows={4} className={styles.mono} value={value.ca ?? ''} onChange={(e) => set({ ca: e.currentTarget.value || undefined })} placeholder="-----BEGIN CERTIFICATE-----" />
      </Field>
      {server ? (
        <Checkbox
          disabled={readOnly}
          value={value.requireClientCert ?? false}
          onChange={(event) => set({ requireClientCert: event.currentTarget.checked || undefined })}
          label={t('gofi.canvas.tls.require-client-cert', 'Require client certificate')}
          description={t('gofi.canvas.tls.require-client-cert-description', 'Only senders presenting a certificate signed by the CA may connect (mTLS).')}
        />
      ) : (
        <>
          <Field noMargin label={t('gofi.canvas.tls.server-name', 'Server name (optional)')} description={t('gofi.canvas.tls.server-name-description', 'Name expected in the server certificate when it differs from the URL host.')}>
            <Input disabled={readOnly} value={value.serverName ?? ''} onChange={(e) => set({ serverName: e.currentTarget.value || undefined })} placeholder="receiver.example.com" />
          </Field>
          <Checkbox
            disabled={readOnly}
            value={value.insecureSkipVerify ?? false}
            onChange={(event) => set({ insecureSkipVerify: event.currentTarget.checked || undefined })}
            label={t('gofi.canvas.broker-forms.skip-verification', 'Skip certificate verification')}
            description={t('gofi.canvas.broker-forms.skip-verification-description', 'Insecure; for testing only.')}
          />
        </>
      )}
      <Field noMargin label={t('gofi.canvas.tls.alpn', 'ALPN protocols')} description={t('gofi.canvas.tls.alpn-description', 'Application protocols offered in the handshake; empty means {{alpn}}.', { alpn: DEFAULT_ALPN.join(', ') })}>
        <TagsInput disabled={readOnly} tags={value.alpn ?? []} onChange={(alpn) => set({ alpn: alpn.length ? alpn : undefined })} placeholder={DEFAULT_ALPN[0]} />
      </Field>
    </div>
  )
}

const getStyles = (theme: GrafanaTheme2) => ({
  // Fields, checkboxes, and tags share one rhythm; the block ends like a field does.
  fields: css({
    display: 'flex', flexDirection: 'column', gap: theme.spacing(2), marginBottom: theme.spacing(2),
    // A bare Checkbox is an inline grid; stretched to the column it would
    // split the spare width between the box and the label.
    '& > label': { alignSelf: 'flex-start' },
  }),
  mono: css({ fontFamily: theme.typography.fontFamilyMonospace, fontSize: theme.typography.bodySmall.fontSize }),
})
