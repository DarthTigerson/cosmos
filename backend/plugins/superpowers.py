import asyncio
import os
import subprocess
from .base import Plugin


def _tool(name: str, description: str, properties: dict, required: list[str]) -> dict:
    return {
        "type": "function",
        "function": {
            "name": name,
            "description": description,
            "parameters": {
                "type": "object",
                "properties": properties,
                "required": required,
            },
        },
    }


class SuperpowersPlugin(Plugin):
    name = "superpowers"

    tools = [
        _tool(
            "run_shell",
            "Execute a shell command on the user's Mac. Returns stdout and stderr. Use for file operations, system info, running scripts, etc.",
            {"cmd": {"type": "string", "description": "The shell command to run"}},
            ["cmd"],
        ),
        _tool(
            "read_file",
            "Read the contents of a file on the user's machine.",
            {"path": {"type": "string", "description": "Absolute or ~ path to the file"}},
            ["path"],
        ),
        _tool(
            "write_file",
            "Write content to a file on the user's machine. Creates or overwrites.",
            {
                "path": {"type": "string", "description": "Absolute or ~ path to write"},
                "content": {"type": "string", "description": "Content to write"},
            },
            ["path", "content"],
        ),
        _tool(
            "list_files",
            "List files and directories at a given path.",
            {"path": {"type": "string", "description": "Directory path to list"}},
            ["path"],
        ),
        _tool(
            "python_eval",
            "Execute Python code and return the output. Good for calculations, data processing, etc.",
            {"code": {"type": "string", "description": "Python code to execute"}},
            ["code"],
        ),
    ]

    async def execute(self, tool_name: str, args: dict) -> str:
        loop = asyncio.get_event_loop()
        try:
            if tool_name == "run_shell":
                return await loop.run_in_executor(None, self._run_shell, args.get("cmd", ""))
            if tool_name == "read_file":
                return await loop.run_in_executor(None, self._read_file, args.get("path", ""))
            if tool_name == "write_file":
                return await loop.run_in_executor(None, self._write_file, args.get("path", ""), args.get("content", ""))
            if tool_name == "list_files":
                return await loop.run_in_executor(None, self._list_files, args.get("path", "."))
            if tool_name == "python_eval":
                return await loop.run_in_executor(None, self._python_eval, args.get("code", ""))
            return f"Unknown tool: {tool_name}"
        except Exception as e:
            return f"Error: {e}"

    # ── Implementations ──────────────────────────────────────────────────────

    def _run_shell(self, cmd: str) -> str:
        result = subprocess.run(
            cmd, shell=True, capture_output=True, text=True, timeout=15
        )
        out = result.stdout.strip()
        err = result.stderr.strip()
        if out and err:
            return f"{out}\n[stderr] {err}"
        return out or err or "(no output)"

    def _read_file(self, path: str) -> str:
        path = os.path.expanduser(path)
        if not os.path.exists(path):
            return f"File not found: {path}"
        size = os.path.getsize(path)
        if size > 100_000:
            return f"File too large ({size} bytes). Use run_shell with head/tail."
        with open(path, "r", errors="replace") as f:
            return f.read()

    def _write_file(self, path: str, content: str) -> str:
        path = os.path.expanduser(path)
        os.makedirs(os.path.dirname(os.path.abspath(path)), exist_ok=True)
        with open(path, "w") as f:
            f.write(content)
        return f"Written {len(content)} bytes to {path}"

    def _list_files(self, path: str) -> str:
        path = os.path.expanduser(path)
        if not os.path.isdir(path):
            return f"Not a directory: {path}"
        entries = os.listdir(path)
        lines = []
        for e in sorted(entries):
            full = os.path.join(path, e)
            suffix = "/" if os.path.isdir(full) else ""
            lines.append(f"{e}{suffix}")
        return "\n".join(lines) if lines else "(empty)"

    def _python_eval(self, code: str) -> str:
        result = subprocess.run(
            ["python3", "-c", code],
            capture_output=True,
            text=True,
            timeout=10,
        )
        out = result.stdout.strip()
        err = result.stderr.strip()
        if out and err:
            return f"{out}\n[stderr] {err}"
        return out or err or "(no output)"
