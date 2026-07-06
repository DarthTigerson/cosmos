import re
import subprocess


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
    charging = "AC Power" in out  # covers "charging" and "charged" states
    discharging = "discharging" in out

    time_remaining = None
    tm = re.search(r'(\d+:\d+) remaining', out)
    if tm:
        time_remaining = tm.group(1)

    return {
        "level": level,
        "charging": charging,
        "discharging": discharging,
        "time_remaining": time_remaining,
    }


def get_network() -> dict:
    """Return WiFi connectivity status on macOS."""
    try:
        # scutil --nwi lists active interfaces with reachability flags
        nwi = subprocess.run(
            ["scutil", "--nwi"],
            capture_output=True, text=True, timeout=3
        ).stdout

        # en0 is listed in "Network interfaces:" and has a Reachable reach line
        wifi_connected = "en0" in nwi and "(Reachable)" in nwi

        # Try to get SSID (may be redacted on macOS 15+ due to privacy changes)
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

        return {"wifi": wifi_connected, "ssid": ssid}
    except Exception:
        pass
    return {"wifi": False, "ssid": None}
