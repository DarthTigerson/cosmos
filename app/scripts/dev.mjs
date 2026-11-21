// Starts the Vite dev server, then Electron pointed at it.
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'
import { createServer } from 'vite'

const require = createRequire(import.meta.url)
const server = await createServer()
await server.listen()
const url = server.resolvedUrls.local[0]
const electron = spawn(require('electron'), ['.'], { stdio: 'inherit', env: { ...process.env, VITE_DEV_URL: url } })
electron.on('exit', async (code) => {
  await server.close()
  process.exit(code ?? 0)
})
