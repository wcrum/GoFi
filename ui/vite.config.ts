import { readFileSync } from 'node:fs'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Optional TLS for the dev server, for browsers that upgrade typed hostnames
// to HTTPS. Set both GOFI_DEV_TLS_CERT and GOFI_DEV_TLS_KEY to PEM file paths.
const tlsCert = process.env.GOFI_DEV_TLS_CERT
const tlsKey = process.env.GOFI_DEV_TLS_KEY
const https = tlsCert && tlsKey ? { cert: readFileSync(tlsCert), key: readFileSync(tlsKey) } : undefined
// Extra hostnames the dev server answers to (comma-separated), e.g. a LAN or
// Tailscale name. Vite rejects unlisted hostnames to prevent DNS rebinding.
const allowedHosts = process.env.GOFI_DEV_ALLOWED_HOSTS?.split(',').map(host => host.trim()).filter(Boolean)

export default defineConfig({
  plugins: [react()],
  server: {
    https,
    allowedHosts,
    proxy: {
      '/gofi.v1.': process.env.GOFI_API_PROXY ?? 'http://localhost:9090',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
