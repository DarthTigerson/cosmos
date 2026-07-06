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
        _tool("run_shell", "Run a shell command. Returns stdout/stderr.",
              {"cmd": {"type": "string"}}, ["cmd"]),
        _tool("read_file", "Read a file.",
              {"path": {"type": "string"}}, ["path"]),
        _tool("write_file", "Write content to a file.",
              {"path": {"type": "string"}, "content": {"type": "string"}}, ["path", "content"]),
        _tool("list_files", "List directory contents.",
              {"path": {"type": "string"}}, ["path"]),
        _tool("python_eval", "Execute Python code. Returns output.",
              {"code": {"type": "string"}}, ["code"]),
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
