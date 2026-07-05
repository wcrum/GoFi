// crypto.randomUUID() only exists in "secure contexts" (HTTPS, or localhost)
// per the Web Crypto API spec — browsers omit it entirely everywhere else,
// including a plain-HTTP LAN/IP address like http://100.88.88.8:9091. These
// ids are only ever used as local React Flow node/stage identifiers (never
// as a security token), so a non-cryptographic fallback is fine.
export function genId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return 'id-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2)
}
