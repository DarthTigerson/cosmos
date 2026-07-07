import asyncio
import fcntl
import json
import logging
import os
import pty
import re as _re
import select
import struct
import subprocess
import termios
from collections import defaultdict
from pathlib import Path
from typing import Optional

from fastapi import APIRouter, WebSocket, WebSocketDisconnect
from fastapi.responses import StreamingResponse
from openai import AsyncOpenAI
from pydantic import BaseModel

import sessions_db

log = logging.getLogger(__name__)
router = APIRouter(prefix="/code")

VMLX_BASE_URL = os.getenv("VMLX_BASE_URL", "http://localhost:8080")
VMLX_MODEL = os.getenv("VMLX_MODEL", "local-model")
VMLX_API_KEY = os.getenv("VMLX_API_KEY", "local")
MAX_TOKENS = int(os.getenv("MAX_TOKENS", "4096"))
MAX_TOOL_ITERATIONS = 10
TOOL_CALL_RE = _re.compile(r"<tool_call>(.*?)(?:</tool_call>|$)", _re.DOTALL)

PROJECTS_FILE = os.getenv("CODE_PROJECTS_FILE", "./data/code_projects.json")

client = AsyncOpenAI(base_url=f"{VMLX_BASE_URL}/v1", api_key=VMLX_API_KEY)
_code_histories: dict[str, list[dict]] = defaultdict(list)


# ── Projects ──────────────────────────────────────────────────────────────────

def _load_projects() -> list:
    try:
        if os.path.exists(PROJECTS_FILE):
            with open(PROJECTS_FILE) as f:
                return json.load(f)
    except Exception:
        pass
    return []


def _save_projects(projects: list):
    os.makedirs(os.path.dirname(os.path.abspath(PROJECTS_FILE)), exist_ok=True)
    with open(PROJECTS_FILE, "w") as f:
        json.dump(projects, f, indent=2)


@router.get("/projects")
async def list_projects():
    return _load_projects()


@router.get("/pick-folder")
async def pick_folder():
    """Open a native macOS folder picker via AppleScript and return the selected path."""
    try:
        result = subprocess.run(
            ["osascript", "-e", 'POSIX path of (choose folder with prompt "Select project folder")'],
            capture_output=True, text=True, timeout=60,
        )
        if result.returncode != 0:
            return {"path": None, "cancelled": True}
        path = result.stdout.strip().rstrip("/")
        return {"path": path, "cancelled": False}
    except Exception as e:
        return {"error": str(e), "path": None}


class ProjectIn(BaseModel):
    name: str
    path: str


@router.post("/projects")
async def add_project(p: ProjectIn):
    projects = _load_projects()
    if not any(pr["path"] == p.path for pr in projects):
        projects.append({"name": p.name, "path": p.path})
        _save_projects(projects)
    return {"ok": True}


@router.delete("/projects/{idx}")
async def remove_project(idx: int):
    projects = _load_projects()
    if 0 <= idx < len(projects):
        projects.pop(idx)
        _save_projects(projects)
    return {"ok": True}


# ── File system ───────────────────────────────────────────────────────────────

_IGNORE = {
    ".git", "node_modules", "__pycache__", ".venv", "venv",
    ".DS_Store", "dist", "build", ".next", ".nuxt", ".cache",
    "coverage", ".pytest_cache", ".mypy_cache", "target", ".tox",
}
_SHOW_HIDDEN = {".env", ".env.local", ".gitignore", ".github"}


def _build_tree(path: str, depth: int = 0):
    if depth > 6:
        return None
    p = Path(path)
    if not p.exists():
        return None
    if p.name in _IGNORE:
        return None
    if p.name.startswith(".") and p.name not in _SHOW_HIDDEN:
        return None
    if p.is_file():
        return {"type": "file", "name": p.name, "path": str(p)}
    if p.is_dir():
        children = []
        try:
            for child in sorted(p.iterdir(), key=lambda x: (x.is_file(), x.name.lower())):
                node = _build_tree(str(child), depth + 1)
                if node:
                    children.append(node)
        except PermissionError:
            pass
        return {"type": "dir", "name": p.name, "path": str(p), "children": children}
    return None


def _safe_path(project_root: str, path: str) -> Optional[str]:
    if os.path.isabs(path):
        resolved = os.path.realpath(path)
    else:
        resolved = os.path.realpath(os.path.join(project_root, path))
    root_real = os.path.realpath(project_root)
    if resolved == root_real or resolved.startswith(root_real + os.sep):
        return resolved
    return None


@router.get("/fs/files")
async def file_list(root: str):
    """Flat sorted list of all file paths relative to project root, for @ mention picker."""
    root = os.path.realpath(root)
    if not os.path.isdir(root):
        return {"files": []}
    _IGNORE = {'.git', 'node_modules', '__pycache__', '.DS_Store', 'dist', 'build',
               '.next', '.venv', 'venv', '.mypy_cache', '.ruff_cache', '.pytest_cache'}
    results = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in _IGNORE and not d.startswith('.')]
        for fname in filenames:
            rel = os.path.relpath(os.path.join(dirpath, fname), root)
            results.append(rel)
    results.sort()
    return {"files": results}


@router.get("/fs/tree")
async def file_tree(root: str):
    tree = _build_tree(root)
    return tree or {"error": "path not found"}


@router.get("/fs/file")
async def read_file_route(path: str):
    try:
        return {"content": Path(path).read_text(errors="replace")}
    except Exception as e:
        return {"error": str(e)}


class FileWrite(BaseModel):
    path: str
    content: str


@router.put("/fs/file")
async def write_file_route(body: FileWrite):
    try:
        p = Path(body.path)
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(body.content)
        return {"ok": True}
    except Exception as e:
        return {"error": str(e)}


# ── Git ───────────────────────────────────────────────────────────────────────

def _git(args: list, cwd: str, timeout: int = 30) -> tuple[str, str, int]:
    r = subprocess.run(
        ["git"] + args, cwd=cwd,
        capture_output=True, text=True, timeout=timeout,
    )
    return r.stdout, r.stderr, r.returncode


@router.get("/git/status")
async def git_status(root: str):
    out, err, code = _git(["status", "--porcelain=v1", "-b"], root)
    if code != 0:
        return {"error": err, "branch": "", "files": []}
    lines = out.splitlines()
    branch = ""
    if lines and lines[0].startswith("##"):
        branch = lines[0][3:].split("...")[0].strip()
    files = []
    for line in lines[1:]:
        if len(line) >= 3:
            files.append({"status": line[:2].strip(), "file": line[3:]})
    return {"branch": branch, "files": files}


@router.get("/git/diff")
async def git_diff(root: str, staged: bool = False, file: Optional[str] = None):
    args = ["diff"]
    if staged:
        args.append("--staged")
    if file:
        args += ["--", file]
    out, err, code = _git(args, root)
    return {"diff": out, "error": err if code != 0 else ""}


@router.get("/git/original")
async def git_original(root: str, path: str):
    rel = os.path.relpath(path, root)
    out, err, code = _git(["show", f"HEAD:{rel}"], root)
    if code != 0:
        return {"content": "", "error": err}
    return {"content": out}


@router.get("/git/log")
async def git_log(root: str, limit: int = 20):
    out, err, code = _git(
        ["log", f"-{limit}", "--pretty=format:%H|%h|%s|%an|%ar"], root
    )
    if code != 0:
        return {"commits": [], "error": err}
    commits = []
    for line in out.splitlines():
        parts = line.split("|", 4)
        if len(parts) == 5:
            commits.append({
                "hash": parts[0], "short": parts[1],
                "message": parts[2], "author": parts[3], "ago": parts[4],
            })
    return {"commits": commits}


class StageBody(BaseModel):
    root: str
    files: list[str]


@router.post("/git/stage")
async def git_stage(body: StageBody):
    _, err, code = _git(["add"] + body.files, body.root)
    return {"ok": code == 0, "error": err}


class CommitBody(BaseModel):
    root: str
    message: str


@router.post("/git/commit")
async def git_commit_route(body: CommitBody):
    out, err, code = _git(["commit", "-m", body.message], body.root)
    return {"ok": code == 0, "output": out, "error": err}


class PushBody(BaseModel):
    root: str


@router.post("/git/push")
async def git_push_route(body: PushBody):
    out, err, code = _git(["push"], body.root, timeout=60)
    return {"ok": code == 0, "output": out + err}


# ── Terminal WebSocket ────────────────────────────────────────────────────────

@router.websocket("/terminal")
async def terminal_ws(ws: WebSocket, cwd: str = "."):
    await ws.accept()

    master_fd, slave_fd = pty.openpty()
    safe_cwd = cwd if os.path.isdir(cwd) else os.path.expanduser("~")

    proc = subprocess.Popen(
        ["/bin/zsh"],
        stdin=slave_fd, stdout=slave_fd, stderr=slave_fd,
        start_new_session=True,
        cwd=safe_cwd,
        env={**os.environ, "TERM": "xterm-256color", "HOME": os.path.expanduser("~")},
    )
    os.close(slave_fd)
    loop = asyncio.get_event_loop()

    async def read_pty():
        try:
            while proc.poll() is None:
                r, _, _ = await loop.run_in_executor(
                    None, lambda: select.select([master_fd], [], [], 0.2)
                )
                if r:
                    try:
                        data = os.read(master_fd, 4096)
                        if data:
                            await ws.send_bytes(data)
                    except OSError:
                        break
        except Exception as e:
            log.debug("[terminal] read ended: %s", e)

    async def recv_ws():
        try:
            while True:
                msg = await ws.receive()
                if msg.get("type") == "websocket.disconnect":
                    break
                if msg.get("bytes"):
                    try:
                        os.write(master_fd, msg["bytes"])
                    except OSError:
                        break
                elif msg.get("text"):
                    text = msg["text"]
                    try:
                        d = json.loads(text)
                        if d.get("type") == "resize":
                            winsize = struct.pack("HHHH", d["rows"], d["cols"], 0, 0)
                            fcntl.ioctl(master_fd, termios.TIOCSWINSZ, winsize)
                    except (json.JSONDecodeError, KeyError):
                        # Regular keystrokes from xterm.js arrive as text frames
                        try:
                            os.write(master_fd, text.encode())
                        except OSError:
                            break
        except (WebSocketDisconnect, Exception) as e:
            log.debug("[terminal] ws recv ended: %s", e)

    read_task = asyncio.create_task(read_pty())
    recv_task = asyncio.create_task(recv_ws())
    await asyncio.wait([read_task, recv_task], return_when=asyncio.FIRST_COMPLETED)
    read_task.cancel()
    recv_task.cancel()
    proc.terminate()
    try:
        os.close(master_fd)
    except OSError:
        pass


# ── Code chat ─────────────────────────────────────────────────────────────────

_TOOL_PROMPT = """

You have tools. To call one, output exactly:
<tool_call>{"name": "TOOL_NAME", "args": {ARGS_JSON}}</tool_call>
Stop after the closing tag and wait for the result before continuing.

Tools:
- read_file(path): Read a file. path is relative to project root, e.g. "frontend/src/App.jsx"
- write_file(path, content): Write content to a file (creates or overwrites). path relative to project root.
- list_files(path): List a directory. path is relative to project root, e.g. "frontend/src". Use "" or "." for root. Examples:
    list_files root:    <tool_call>{"name": "list_files", "args": {"path": "."}}</tool_call>
    list_files subdir:  <tool_call>{"name": "list_files", "args": {"path": "frontend/src"}}</tool_call>
- run_command(command): Run a shell command in the project root
- git_status(): Show git status
- git_diff(file?, staged?): Show git diff
- git_add(files): Stage files (use ["."] for all changed)
- git_commit(message): Commit staged changes
- git_push(): Push commits to remote"""


def _read_claude_md(root: str) -> str:
    for candidate in [
        os.path.join(root, "CLAUDE.md"),
        os.path.join(root, ".claude", "CLAUDE.md"),
    ]:
        if os.path.isfile(candidate):
            try:
                return Path(candidate).read_text()[:10000]
            except Exception:
                pass
    return ""


def _build_system(project_root: str, project_name: str, branch: str) -> str:
    claude_md = _read_claude_md(project_root)
    project_instructions = (
        f"\n\nProject instructions (from CLAUDE.md):\n{claude_md}"
        if claude_md else ""
    )
    return (
        f"You are Cosmos, a coding assistant working in the {project_name} project.\n\n"
        f"Project root: {project_root}\n"
        f"Current branch: {branch or 'unknown'}\n\n"
        "You have full access to the repository via your tools. When starting a task, "
        "use list_files and read_file to understand the codebase. "
        "Write complete, working implementations. File writes appear in the editor immediately."
        f"{project_instructions}{_TOOL_PROMPT}"
    )


async def _exec_tool(name: str, args: dict, root: str) -> tuple[str, Optional[str]]:
    """Returns (result, file_written_path_or_None)."""
    try:
        if name == "read_file":
            path = _safe_path(root, args.get("path", ""))
            if not path:
                return "Error: path escapes project root", None
            try:
                return Path(path).read_text(errors="replace"), None
            except Exception as e:
                return f"Error: {e}", None

        elif name == "write_file":
            path = _safe_path(root, args.get("path", ""))
            if not path:
                return "Error: path escapes project root", None
            p = Path(path)
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text(args.get("content", ""))
            return f"Written: {path}", path

        elif name == "list_files":
            raw_path = args.get("path", ".")
            target = root if raw_path in ("", ".", "/") else raw_path
            path = _safe_path(root, target)
            if not path:
                return "Error: path escapes project root", None
            p = Path(path)
            if not p.exists():
                return f"Error: {path} not found", None
            rel = os.path.relpath(path, root)
            lines = [f"Contents of {rel}:"]
            for item in sorted(p.iterdir(), key=lambda x: (x.is_file(), x.name)):
                if item.name.startswith(".") and item.name not in (".env", ".gitignore"):
                    continue
                lines.append(("[dir] " if item.is_dir() else "      ") + item.name)
            return "\n".join(lines) or f"{rel}: (empty)", None

        elif name == "run_command":
            cmd = args.get("command", "")
            r = subprocess.run(
                cmd, shell=True, cwd=root,
                capture_output=True, text=True, timeout=30,
            )
            return (r.stdout + r.stderr)[:4000] or "(no output)", None

        elif name == "git_status":
            out, err, _ = _git(["status", "--short", "-b"], root)
            return out or err or "nothing to report", None

        elif name == "git_diff":
            gargs = ["diff"]
            if args.get("staged"):
                gargs.append("--staged")
            if args.get("file"):
                gargs += ["--", args["file"]]
            out, _, _ = _git(gargs, root)
            return out[:4000] or "(no diff)", None

        elif name == "git_add":
            files = args.get("files", ["."])
            _, err, code = _git(["add"] + files, root)
            return f"Staged: {', '.join(files)}" if code == 0 else err, None

        elif name == "git_commit":
            out, err, code = _git(["commit", "-m", args.get("message", "update")], root)
            return out or err, None

        elif name == "git_push":
            out, err, code = _git(["push"], root, timeout=60)
            return (out + err) or "pushed", None

        else:
            return f"Unknown tool: {name}", None

    except subprocess.TimeoutExpired:
        return "Error: command timed out", None
    except Exception as e:
        return f"Error: {e}", None


class _GenState:
    def __init__(self):
        self.events: list[dict] = []
        self.task: Optional[asyncio.Task] = None
        self._subs: list[asyncio.Queue] = []

    def push(self, ev: dict):
        self.events.append(ev)
        for q in self._subs:
            q.put_nowait(ev)

    def subscribe(self, offset: int = 0) -> asyncio.Queue:
        q: asyncio.Queue = asyncio.Queue()
        for ev in self.events[offset:]:
            q.put_nowait(ev)
        self._subs.append(q)
        return q

    def unsubscribe(self, q: asyncio.Queue):
        try:
            self._subs.remove(q)
        except ValueError:
            pass


_gen_states: dict[str, _GenState] = {}


async def _follow_state(state: _GenState):
    q = state.subscribe(0)
    try:
        while True:
            try:
                ev = await asyncio.wait_for(q.get(), timeout=30)
            except asyncio.TimeoutError:
                yield ": heartbeat\n\n"
                continue
            yield f"data: {json.dumps(ev)}\n\n"
            if ev.get("done") or ev.get("error"):
                return
    finally:
        state.unsubscribe(q)


def _sse(gen):
    return StreamingResponse(
        gen, media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


async def _do_generate(session_id: str, state: _GenState, messages: list, root: str):
    full_content = ""
    try:
        for _iter in range(MAX_TOOL_ITERATIONS):
            stream = await client.chat.completions.create(
                model=VMLX_MODEL,
                messages=messages,
                stream=True,
                max_tokens=MAX_TOKENS,
                timeout=120,
            )

            content_buf = ""
            async for chunk in stream:
                if not chunk.choices:
                    continue
                delta = chunk.choices[0].delta
                reasoning = (
                    getattr(delta, "reasoning_content", None)
                    or (getattr(delta, "model_extra", None) or {}).get("reasoning_content")
                    or ""
                )
                if reasoning:
                    state.push({"reasoning_delta": reasoning})
                if delta.content:
                    content_buf += delta.content

            tool_match = TOOL_CALL_RE.search(content_buf)
            if tool_match and tool_match.group(1).strip():
                before = content_buf[: content_buf.index("<tool_call>")].strip()
                if before:
                    full_content += before
                    state.push({"delta": before})

                for match in TOOL_CALL_RE.finditer(content_buf):
                    try:
                        tc = json.loads(match.group(1).strip())
                        name = tc.get("name", "")
                        args = tc.get("args", tc.get("arguments", {}))
                    except Exception as parse_err:
                        log.warning("[code-chat] tool parse error: %s", parse_err)
                        continue

                    state.push({"tool_call": {"name": name, "args": args}})
                    result, file_written = await _exec_tool(name, args, root)
                    state.push({"tool_result": {"name": name, "output": result}})
                    if file_written:
                        state.push({"file_written": file_written})

                    messages.append({"role": "assistant", "content": content_buf})
                    messages.append({
                        "role": "user",
                        "content": f"[Tool result: {name}]\n{result}\n\nContinue.",
                    })
                continue

            if content_buf:
                full_content += content_buf
                state.push({"delta": content_buf})
            break

    except asyncio.CancelledError:
        pass
    except Exception as e:
        state.push({"error": str(e)})
    finally:
        sessions_db.append_message(session_id, "assistant", full_content)
        sessions_db.touch_session(session_id)
        hist = _code_histories[session_id]
        if full_content:
            hist.append({"role": "assistant", "content": full_content})
        _gen_states.pop(session_id, None)
        state.push({"done": True})


class CodeChatRequest(BaseModel):
    session_id: str
    message: str
    project_root: str
    project_name: str


@router.post("/chat/stream")
async def code_chat_stream(req: CodeChatRequest):
    session_id = req.session_id
    root = req.project_root

    hist = _code_histories[session_id]
    if not hist:
        rows = sessions_db.get_messages(session_id)
        if rows:
            hist.extend([{"role": r["role"], "content": r["content"]} for r in rows])

    branch = ""
    try:
        out, _, _ = _git(["branch", "--show-current"], root)
        branch = out.strip()
    except Exception:
        pass

    system = _build_system(root, req.project_name, branch)
    messages = [{"role": "system", "content": system}]
    messages += hist[-20:]
    messages.append({"role": "user", "content": req.message})

    sessions_db.ensure_session(session_id, req.message)
    sessions_db.append_message(session_id, "user", req.message)
    hist.append({"role": "user", "content": req.message})

    if session_id in _gen_states:
        existing = _gen_states.pop(session_id)
        if existing.task and not existing.task.done():
            existing.task.cancel()

    state = _GenState()
    _gen_states[session_id] = state
    state.task = asyncio.create_task(_do_generate(session_id, state, messages, root))

    return _sse(_follow_state(state))


@router.get("/chat/history")
async def code_chat_history(session_id: str):
    rows = sessions_db.get_messages(session_id)
    return {"history": [{"role": r["role"], "content": r["content"]} for r in rows]}


@router.delete("/chat/{session_id}/generation")
async def cancel_generation(session_id: str):
    if session_id in _gen_states:
        state = _gen_states[session_id]
        if state.task and not state.task.done():
            state.task.cancel()
    return {"cancelled": True}
