// What the title bar shows about the Mac itself: memory and battery. After
// vIDE's systemMemory.ts and systemBattery.ts, macOS only (Cosmos is).
const { execFile } = require('node:child_process')
const os = require('node:os')
const { promisify } = require('node:util')

const run = promisify(execFile)

// Pressure leans on swap and compression, the honest signs of distress, and
// only uses a very high used share as a backstop, so a Mac merely holding a
// lot of reclaimable file cache is not flagged.
function pressure(u) {
  const used = u.totalBytes > 0 ? u.usedBytes / u.totalBytes : 0
  const comp = u.totalBytes > 0 ? u.compressedBytes / u.totalBytes : 0
  if (u.swapUsedBytes > 0 || comp >= 0.2 || used >= 0.95) return 'critical'
  if (comp >= 0.08 || used >= 0.85) return 'warn'
  return 'normal'
}

// `sysctl vm.swapusage`: "total = 2048.00M  used = 512.00M  free = …".
function parseSwap(stdout) {
  const m = stdout.match(/used\s*=\s*([\d.]+)([KMGT]?)/i)
  if (!m) return 0
  const unit = m[2].toUpperCase()
  const mult = unit === 'G' ? 1024 ** 3 : unit === 'T' ? 1024 ** 4 : unit === 'K' ? 1024 : 1024 ** 2
  return Math.round(Number(m[1]) * mult)
}

// Free, inactive and purgeable pages are given back on demand, so like
// Activity Monitor's "Memory Used" they do not count as used.
function parseVmStat(stdout, totalBytes) {
  const pageSize = Number(stdout.match(/page size of (\d+) bytes/)?.[1] ?? 16384)
  const pages = {}
  for (const line of stdout.split('\n')) {
    const m = line.match(/^Pages (\w[\w -]*?):\s+(\d+)\.?$/)
    if (m) pages[m[1]] = Number(m[2])
  }
  const reclaimable = (pages.free ?? 0) + (pages.inactive ?? 0) + (pages.purgeable ?? 0)
  return {
    usedBytes: totalBytes - reclaimable * pageSize,
    totalBytes,
    compressedBytes: (pages['occupied by compressor'] ?? 0) * pageSize,
  }
}

async function memory() {
  const { stdout } = await run('vm_stat')
  const base = parseVmStat(stdout, os.totalmem())
  let swapUsedBytes = 0
  try {
    swapUsedBytes = parseSwap((await run('sysctl', ['vm.swapusage'])).stdout)
  } catch { /* no sysctl: count it as no swap */ }
  const u = { ...base, swapUsedBytes }
  return { ...u, pressure: pressure(u) }
}

// `top`'s MEM is each process's footprint, the number Activity Monitor
// shows. Unlike ps's resident size it includes what a model holds on the
// GPU, which is most of it. "313M", "1.2G", "512K", sometimes with + or -.
function parseFootprints(stdout) {
  const out = new Map()
  for (const line of stdout.split('\n')) {
    const m = line.trim().match(/^(\d+)\s+([\d.]+)([BKMGT])[+-]?/)
    if (!m) continue
    const mult = { B: 1, K: 1024, M: 1024 ** 2, G: 1024 ** 3, T: 1024 ** 4 }[m[3]]
    out.set(Number(m[1]), Math.round(Number(m[2]) * mult))
  }
  return out
}

// breakdown: what Cosmos itself holds. The app's own processes, the core,
// and each llama-server by the port it listens on, so the page can tell
// which model it is. Servers are found by name, not as children of the
// core, because the core may have been started outside the app.
async function breakdown(appPids) {
  const { stdout } = await run('ps', ['-axo', 'pid=,command='])
  let core = 0
  const servers = []
  for (const line of stdout.split('\n')) {
    const m = line.trim().match(/^(\d+)\s+(.*)$/)
    if (!m) continue
    const pid = Number(m[1])
    const cmd = m[2]
    if (/(^|\/)cosmos-core(\s|$)/.test(cmd)) core = pid
    else if (/(^|\/)llama-server(\s|$)/.test(cmd)) {
      const port = Number(cmd.match(/--port[ =](\d+)/)?.[1] ?? 8080)
      servers.push({ pid, port })
    }
  }
  const pids = [...appPids, ...(core ? [core] : []), ...servers.map((s) => s.pid)]
  const args = ['-l', '1', '-stats', 'pid,mem']
  for (const pid of pids) args.push('-pid', String(pid))
  const sizes = parseFootprints((await run('top', args)).stdout)
  const size = (pid) => sizes.get(pid) ?? 0
  return {
    appBytes: appPids.reduce((sum, pid) => sum + size(pid), 0),
    coreBytes: core ? size(core) : 0,
    models: servers.map((s) => ({ port: s.port, bytes: size(s.pid) })),
  }
}

// `pmset -g batt`: a laptop has a line like
//   " -InternalBattery-0 (id=4653155)	72%; discharging; 3:12 remaining present: true"
// A Mac without a battery prints none, and gets null.
function parseBattery(stdout) {
  const line = stdout.split('\n').find((l) => /InternalBattery/.test(l))
  const percent = line?.match(/(\d+)%/)
  if (!percent) return null
  const state = (line.match(/%;\s*([^;]+);/)?.[1] ?? '').trim().toLowerCase()
  const pluggedIn = /'AC Power'/.test(stdout) || state !== 'discharging'
  const time = line.match(/(\d+):(\d{2}) remaining/)
  const minutes = time ? Number(time[1]) * 60 + Number(time[2]) : null
  return {
    percent: Number(percent[1]),
    charging: state === 'charging',
    pluggedIn,
    // "0:00 remaining" shows while macOS is still working it out.
    minutesLeft: !pluggedIn && minutes ? minutes : null,
  }
}

async function battery() {
  try {
    return parseBattery((await run('pmset', ['-g', 'batt'])).stdout)
  } catch {
    return null
  }
}

module.exports = { memory, breakdown, battery, parseVmStat, parseSwap, parseFootprints, parseBattery, pressure }
