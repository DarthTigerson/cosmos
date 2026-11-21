// The Electron shell. It owns the window and nothing else: Nova's mind is
// the cosmos-core process, which this file starts and stops.
const { app, BrowserWindow, Menu, ipcMain, dialog, shell, session, systemPreferences } = require('electron')
const { spawn } = require('node:child_process')
const crypto = require('node:crypto')
const fs = require('node:fs')
const net = require('node:net')
const path = require('node:path')
const system = require('./system.cjs')

let core = null
let conn = null
let quitting = false

function freePort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer()
    srv.on('error', reject)
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address()
      srv.close(() => resolve(port))
    })
  })
}

function coreBinary() {
  if (process.env.COSMOS_CORE) return process.env.COSMOS_CORE
  const packaged = path.join(process.resourcesPath || '', 'cosmos-core')
  if (app.isPackaged && fs.existsSync(packaged)) return packaged
  return path.join(__dirname, '..', '..', 'core', 'bin', 'cosmos-core')
}

async function waitForHealth(baseUrl, ms) {
  const deadline = Date.now() + ms
  while (Date.now() < deadline) {
    try {
      const r = await fetch(baseUrl + '/api/health')
      if (r.ok) return true
    } catch { /* not up yet */ }
    await new Promise((r) => setTimeout(r, 150))
  }
  return false
}

function dataDir() {
  return process.env.COSMOS_DATA || path.join(app.getPath('appData'), 'Cosmos')
}

function readToken() {
  try {
    return fs.readFileSync(path.join(dataDir(), 'token'), 'utf8').trim()
  } catch {
    return ''
  }
}

let lastLines = ''
let restarts = []

// spawnCore starts the core on a fixed address. If it stops while the app
// is open (a crash, or a new build being swapped in), it is started again:
// the address and token stay the same, so the window carries on.
function spawnCore(port) {
  const bin = coreBinary()
  const args = ['--addr', `127.0.0.1:${port}`]
  if (process.env.COSMOS_DATA) args.push('--data', process.env.COSMOS_DATA)
  core = spawn(bin, args, { env: process.env, stdio: ['ignore', 'pipe', 'pipe'] })
  core.stderr.on('data', (d) => { lastLines = (lastLines + d.toString()).slice(-2000); process.stderr.write(d) })
  core.stdout.on('data', (d) => process.stdout.write(d))
  core.on('exit', (code) => {
    core = null
    if (quitting) return
    const now = Date.now()
    restarts = restarts.filter((t) => now - t < 60000)
    if (restarts.length >= 5) {
      dialog.showErrorBox('Nova stopped', `The core keeps exiting (code ${code}).\n\n${lastLines}`)
      app.quit()
      return
    }
    restarts.push(now)
    setTimeout(() => { if (!quitting) spawnCore(port) }, 600)
  })
}

// startCore returns how to reach the core. Set COSMOS_CORE_URL and
// COSMOS_TOKEN to use a core running elsewhere, for example on another
// machine. Otherwise the app uses the core on this Mac: one that is
// already running at the usual address, or one it starts itself.
async function startCore() {
  if (process.env.COSMOS_CORE_URL) {
    return { baseUrl: process.env.COSMOS_CORE_URL.replace(/\/$/, ''), token: process.env.COSMOS_TOKEN || '' }
  }
  const bin = coreBinary()
  if (!fs.existsSync(bin)) {
    throw new Error(`The core was not found at ${bin}. Build it with "npm run core".`)
  }
  // A scratch data folder gets a port of its own, so a test copy never
  // collides with the real one.
  const port = process.env.COSMOS_PORT || (process.env.COSMOS_DATA ? await freePort() : 7717)
  const baseUrl = `http://127.0.0.1:${port}`
  if (!(await waitForHealth(baseUrl, 400))) {
    spawnCore(port)
    if (!(await waitForHealth(baseUrl, 10000))) {
      throw new Error(`The core did not start.\n\n${lastLines}`)
    }
  }
  const token = process.env.COSMOS_TOKEN || readToken()
  if (!token) throw new Error('The core is running but its token file could not be read.')
  return { baseUrl, token }
}

// The menu is Electron's usual one, except for text size: ⌘⇧+ / ⌘⇧− / ⌘⇧0
// ask the page to change it (it keeps the size and shows it in the footer).
// The plain ⌘+ / ⌘− / ⌘0 page zoom is left out on purpose.
function setMenu() {
  const size = (dir) => () => BrowserWindow.getFocusedWindow()?.webContents.send('cosmos:text-size', dir)
  Menu.setApplicationMenu(Menu.buildFromTemplate([
    ...(process.platform === 'darwin' ? [{ role: 'appMenu' }] : []),
    { role: 'editMenu' },
    {
      label: 'View',
      submenu: [
        { role: 'reload' },
        { role: 'forceReload' },
        { role: 'toggleDevTools' },
        { type: 'separator' },
        { label: 'Actual Text Size', accelerator: 'CmdOrCtrl+Shift+0', click: size('reset') },
        { label: 'Larger Text', accelerator: 'CmdOrCtrl+Shift+=', click: size('larger') },
        { label: 'Smaller Text', accelerator: 'CmdOrCtrl+Shift+-', click: size('smaller') },
        { type: 'separator' },
        { role: 'togglefullscreen' },
      ],
    },
    { role: 'windowMenu' },
  ]))
}

function createWindow() {
  const win = new BrowserWindow({
    width: 1240,
    height: 820,
    minWidth: 720,
    minHeight: 480,
    titleBarStyle: 'hiddenInset',
    trafficLightPosition: { x: 14, y: 10 },
    backgroundColor: '#0b0e17',
    show: !process.env.COSMOS_SCREENSHOT_DIR,
    webPreferences: { preload: path.join(__dirname, 'preload.cjs'), contextIsolation: true, sandbox: true },
  })
  // The only thing the page may ask for is the microphone, for talking
  // to her. Everything else is refused.
  session.defaultSession.setPermissionRequestHandler((_wc, permission, done, details) => {
    if (permission !== 'media' || (details.mediaTypes || []).includes('video')) return done(false)
    if (process.platform === 'darwin') systemPreferences.askForMediaAccess('microphone').then(done, () => done(false))
    else done(true)
  })
  win.webContents.setWindowOpenHandler(({ url }) => {
    shell.openExternal(url)
    return { action: 'deny' }
  })
  if (process.env.VITE_DEV_URL) win.loadURL(process.env.VITE_DEV_URL)
  else win.loadFile(path.join(__dirname, '..', 'dist', 'index.html'))
  return win
}

// With COSMOS_SCREENSHOT_DIR set, the app renders each screen off-screen,
// saves a picture of its own window, and quits. Used to check the UI.
async function screenshots(win, dir) {
  // Problems in the page are printed, so a blank picture can be explained.
  win.webContents.on('console-message', (_e, level, message) => {
    if (level >= 2) console.log('page:', message)
  })
  fs.mkdirSync(dir, { recursive: true })
  await new Promise((r) => win.webContents.once('did-finish-load', r))
  // COSMOS_TEST_SEND types a message into the window and sends it, then
  // reports how many copies of it are on screen while she is replying.
  if (process.env.COSMOS_TEST_SEND) {
    const text = JSON.stringify(process.env.COSMOS_TEST_SEND)
    await new Promise((r) => setTimeout(r, 1200))
    await win.webContents.executeJavaScript(`(() => {
      const input = document.getElementById('say')
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(input, ${text})
      input.dispatchEvent(new Event('input', { bubbles: true }))
      input.form.requestSubmit()
    })()`)
    const count = () => win.webContents.executeJavaScript(`[...document.querySelectorAll('.bubble')].filter((b) => b.textContent === ${text}).length`)
    for (const ms of [400, 1500, 4000]) {
      await new Promise((r) => setTimeout(r, ms))
      console.log(`copies on screen after +${ms}ms: ${await count()}`)
    }
    fs.writeFileSync(path.join(dir, 'sending.png'), (await win.webContents.capturePage()).toPNG())
    for (let i = 0; i < 120; i++) {
      await new Promise((r) => setTimeout(r, 500))
      if (!(await win.webContents.executeJavaScript(`!!document.querySelector('.thinking')`))) break
    }
    console.log(`copies after the reply: ${await count()}; replies: ${await win.webContents.executeJavaScript(`document.querySelectorAll('.reply').length`)}`)
  }
  // COSMOS_TEST_CLICK clicks the last element matching a selector on her
  // screen and saves a picture of the result.
  if (process.env.COSMOS_TEST_CLICK) {
    await new Promise((r) => setTimeout(r, 1200))
    const hit = await win.webContents.executeJavaScript(`(() => { const all = document.querySelectorAll(${JSON.stringify(process.env.COSMOS_TEST_CLICK)}); if (!all.length) return 0; all[all.length - 1].click(); return all.length })()`)
    console.log(`clicked: ${hit} matching`)
    await new Promise((r) => setTimeout(r, 900))
    fs.writeFileSync(path.join(dir, 'clicked.png'), (await win.webContents.capturePage()).toPNG())
  }
  for (const screen of ['nova', 'mind', 'memory', 'people', 'calendar', 'lists', 'podcasts', 'sleep', 'models', 'settings']) {
    await win.webContents.executeJavaScript(`location.hash = '#${screen}'`)
    // Her 3D model takes a few seconds to arrive on the first screen.
    await new Promise((r) => setTimeout(r, screen === 'nova' ? Number(process.env.COSMOS_SHOT_WAIT || 900) : 900))
    const image = await win.webContents.capturePage()
    fs.writeFileSync(path.join(dir, `${screen}.png`), image.toPNG())
  }
  app.quit()
}

app.whenReady().then(async () => {
  try {
    conn = await startCore()
  } catch (err) {
    dialog.showErrorBox('Cosmos could not start', String(err.message || err))
    app.quit()
    return
  }
  ipcMain.handle('cosmos:conn', () => conn)
  ipcMain.handle('cosmos:memory', () => system.memory())
  ipcMain.handle('cosmos:memory-breakdown', () => system.breakdown(app.getAppMetrics().map((p) => p.pid)))
  ipcMain.handle('cosmos:battery', () => system.battery())
  setMenu()
  const win = createWindow()
  if (process.env.COSMOS_SCREENSHOT_DIR) screenshots(win, process.env.COSMOS_SCREENSHOT_DIR)
  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow()
  })
})

// Closing the window closes the app, and the app takes the core with it.
// To keep Nova awake with the window shut, run cosmos-core on its own and
// point the app at it with COSMOS_CORE_URL.
app.on('window-all-closed', () => app.quit())
app.on('before-quit', () => {
  quitting = true
  if (core) core.kill('SIGTERM')
})
