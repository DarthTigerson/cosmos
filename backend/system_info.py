import json
import os
import re
import subprocess
import time

READINGS_FILE = os.getenv("BATTERY_READINGS_FILE", "./data/battery_readings.json")
MAX_READINGS = 120   # ~1 hour at 30s poll interval
MIN_TRACKING_SECONDS = 120  # need 2 min before showing a rate


# ── Battery readings history ──────────────────────────────────────────────────

_readings: list[dict] = []  # [{ts: float, level: int, charging: bool}]


def _load_readings():
    global _readings
    try:
        if os.path.exists(READINGS_FILE):
            with open(READINGS_FILE) as f:
                _readings = json.load(f)
            _readings = _readings[-MAX_READINGS:]
    except Exception:
        _readings = []


def _save_readings():
    try:
        os.makedirs(os.path.dirname(os.path.abspath(READINGS_FILE)), exist_ok=True)
        with open(READINGS_FILE, "w") as f:
            json.dump(_readings[-MAX_READINGS:], f)
    except Exception:
        pass


def record_battery_reading(level: int, charging: bool):
    now = time.time()
    # Deduplicate: skip if last reading was < 20s ago with same level
    if _readings:
        last = _readings[-1]
        if now - last["ts"] < 20 and last["level"] == level and last["charging"] == charging:
            return
    _readings.append({"ts": now, "level": level, "charging": charging})
    if len(_readings) > MAX_READINGS:
        _readings.pop(0)
    _save_readings()


def get_drain_stats() -> dict:
    """Calculate drain rate and predictions from recorded readings."""
    if not _readings:
        return _empty_stats(0)

    # Use only readings matching the current charging state
    current_charging = _readings[-1]["charging"]
    consistent = [r for r in _readings if r["charging"] == current_charging]

    if len(consistent) < 2:
        return _empty_stats(len(consistent))

    first, last = consistent[0], consistent[-1]
    elapsed_sec = last["ts"] - first["ts"]

    if elapsed_sec < MIN_TRACKING_SECONDS:
        return _empty_stats(len(consistent), elapsed_sec)

    elapsed_hours = elapsed_sec / 3600
    level_change = last["level"] - first["level"]   # negative = draining
    rate_per_hour = level_change / elapsed_hours     # negative = draining

    predicted_empty = predicted_full = None
    if rate_per_hour < -0.1:   # draining meaningfully
        hours_left = last["level"] / abs(rate_per_hour)
        predicted_empty = last["ts"] + hours_left * 3600
    elif rate_per_hour > 0.1:  # charging meaningfully
        hours_to_full = (100 - last["level"]) / rate_per_hour
        predicted_full = last["ts"] + hours_to_full * 3600

    return {
        "rate_pct_per_hour": round(rate_per_hour, 1),
        "predicted_empty": predicted_empty,
        "predicted_full": predicted_full,
        "observations": len(consistent),
        "tracked_seconds": round(elapsed_sec),
    }


def _empty_stats(observations: int, tracked_seconds: float = 0) -> dict:
    return {
        "rate_pct_per_hour": None,
        "predicted_empty": None,
        "predicted_full": None,
        "observations": observations,
        "tracked_seconds": round(tracked_seconds),
    }


# ── Battery current ────────────────────────────────────────────────────────────

def get_battery() -> dict | None:
    """Return battery status from pmset. Returns None if not available."""
    try:
        out = subprocess.run(
            ["pmset", "-g", "batt"],
            capture_output=True, text=True, timeout=3
        ).stdout
    except Exception:
        return None

    m = re.search(r'(\d+)%', out)
    if not m:
        return None

    level = int(m.group(1))
    charging = "AC Power" in out
    discharging = "discharging" in out

    time_remaining = None
    tm = re.search(r'(\d+:\d+) remaining', out)
    if tm:
        time_remaining = tm.group(1)

    record_battery_reading(level, charging)
    stats = get_drain_stats()

    return {
        "level": level,
        "charging": charging,
        "discharging": discharging,
        "time_remaining": time_remaining,
        "stats": stats,
    }


# ── Memory ────────────────────────────────────────────────────────────────────

import ctypes
import ctypes.util

_libc = ctypes.CDLL(ctypes.util.find_library("c"), use_errno=True)

def _phys_footprint(pid: int) -> int | None:
    # proc_pid_rusage with RUSAGE_INFO_V4 returns ri_phys_footprint at offset 72,
    # which matches Activity Monitor's "Memory" column (includes Metal/GPU pages).
    try:
        buf = ctypes.create_string_buffer(1024)
        if _libc.proc_pid_rusage(pid, 4, buf) == 0:
            return int.from_bytes(buf.raw[72:80], "little")
    except Exception:
        pass
    return None


def get_memory() -> dict:
    """Return physical footprint breakdown of Cosmos processes and total system RAM."""
    total_bytes = 0
    try:
        total_bytes = int(subprocess.run(
            ["sysctl", "-n", "hw.memsize"],
            capture_output=True, text=True, timeout=3
        ).stdout.strip())
    except Exception:
        pass

    model_bytes = 0
    backend_bytes = 0
    try:
        out = subprocess.run(
            ["ps", "ax", "-o", "pid=,rss=,command="],
            capture_output=True, text=True, timeout=5
        ).stdout
        for line in out.splitlines():
            parts = line.strip().split(None, 2)
            if len(parts) < 3:
                continue
            try:
                pid = int(parts[0])
                rss_kb = int(parts[1])
            except ValueError:
                continue
            cmd = parts[2]
            if "vmlx_engine" in cmd:
                fp = _phys_footprint(pid)
                model_bytes += fp if fp is not None else rss_kb * 1024
            elif "uvicorn" in cmd and "main:app" in cmd:
                fp = _phys_footprint(pid)
                backend_bytes += fp if fp is not None else rss_kb * 1024
    except Exception:
        pass

    system_used_bytes = 0
    try:
        vm = subprocess.run(
            ["vm_stat"], capture_output=True, text=True, timeout=3
        ).stdout
        page_size = 16384
        m = re.search(r'page size of (\d+) bytes', vm)
        if m:
            page_size = int(m.group(1))

        def _pages(label):
            match = re.search(rf'{re.escape(label)}:\s+([\d]+)', vm)
            return int(match.group(1)) if match else 0

        anonymous = _pages("Anonymous pages")
        wired = _pages("Pages wired down")
        compressed = _pages("Pages occupied by compressor")
        system_used_bytes = (anonymous + wired + compressed) * page_size
    except Exception:
        pass

    return {
        "cosmos_bytes": model_bytes + backend_bytes,
        "model_bytes": model_bytes,
        "backend_bytes": backend_bytes,
        "system_used_bytes": system_used_bytes,
        "total_bytes": total_bytes,
    }


# ── Network ────────────────────────────────────────────────────────────────────

def get_network() -> dict:
    """Return WiFi connectivity status on macOS."""
    try:
        nwi = subprocess.run(
            ["scutil", "--nwi"],
            capture_output=True, text=True, timeout=3
        ).stdout
        wifi_connected = "en0" in nwi and "(Reachable)" in nwi

        ssid = None
        try:
            ns_out = subprocess.run(
                ["networksetup", "-getairportnetwork", "en0"],
                capture_output=True, text=True, timeout=3
            ).stdout.strip()
            if "Current Wi-Fi Network:" in ns_out:
                ssid = ns_out.split("Current Wi-Fi Network:", 1)[-1].strip()
        except Exception:
            pass

        ip = None
        try:
            ip_out = subprocess.run(
                ["ipconfig", "getifaddr", "en0"],
                capture_output=True, text=True, timeout=3
            ).stdout.strip()
            if ip_out:
                ip = ip_out
        except Exception:
            pass

        return {"wifi": wifi_connected, "ssid": ssid, "ip": ip}
    except Exception:
        pass
    return {"wifi": False, "ssid": None, "ip": None}
