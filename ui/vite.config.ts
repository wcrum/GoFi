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
      '/gofi.v1.': {
        target: process.env.GOFI_API_PROXY ?? 'http://localhost:9090',
        // When the controller dies mid-stream (restart, crash), http-proxy
        // only answers 502 if the headers were not sent yet; a server stream
        // whose body was already flowing would otherwise stay open on the
        // browser side forever, and the portal's reconnect loop never ran.
        // Close the client response when the upstream one ends incomplete.
        configure: (proxy) => {
          proxy.on('proxyRes', (proxyRes, _req, res) => {
            proxyRes.on('close', () => { if (!proxyRes.complete && !res.writableEnded) res.destroy() })
          })
        },
      },
    },
  },
  // src/lib/monaco.ts imports monaco-editor directly (shared with
  // @grafana/ui); pre-bundle it up front so lazily loaded editor pages don't
  // trigger a mid-session re-optimization that 504s stale module requests.
  optimizeDeps: {
    include: ['monaco-editor'],
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
