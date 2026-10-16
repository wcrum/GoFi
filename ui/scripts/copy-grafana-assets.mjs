import { cp, mkdir } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
const root = new URL('../', import.meta.url)
const destination = new URL('public/public/build/img/icons/', root)
await mkdir(destination, { recursive: true })
await cp(fileURLToPath(new URL('node_modules/@grafana/ui/dist/public/img/icons/', root)), fileURLToPath(destination), { recursive: true })
