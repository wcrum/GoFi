import { t } from '../../../i18n/index.ts'

/** Broker source field checks; each returns an error message or undefined. */
// Mirrors spec validation (spec/subscribe_streams.go).
export const hostPortProblem = (value: string): string | undefined => {
  const match = /^(.+):(\d+)$/.exec(value.trim())
  if (!match || !match[1] || match[1].includes('/')) return t('gofi.canvas.broker-validation.host-port', 'Use host:port.')
  const port = Number(match[2])
  return port >= 1 && port <= 65535 ? undefined : t('gofi.canvas.broker-validation.port-range', 'Port must be 1-65535.')
}
export const kafkaTopicProblem = (topic: string): string | undefined =>
  !topic.trim() || /[\s/\\,]/.test(topic) || topic.length > 249 ? t('gofi.canvas.broker-validation.topic-name', '"{{topic}}" is not a valid topic name.', { topic }) : undefined
export const jetStreamNameProblem = (value: string, required: boolean): string | undefined =>
  !value.trim() ? (required ? t('gofi.common.required', 'Required.') : undefined) : /[\s.*>]/.test(value) ? t('gofi.canvas.broker-validation.jetstream-name', "Cannot contain spaces, '.', '*', or '>'.") : undefined
