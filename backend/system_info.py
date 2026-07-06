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
