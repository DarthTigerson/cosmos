import asyncio
import json
import os
import re as _re
from collections import defaultdict
from datetime import datetime as _dt
from fastapi import APIRouter
from fastapi.responses import StreamingResponse
from openai import AsyncOpenAI
from models import ChatRequest, ChatResponse
from memory import retrieve_memories, maybe_store_exchange
from character import get_character_context, extract_and_store_traits
from plugins import get_tools_for_plugins, execute_tool
import sessions_db
import questions_db
import follow_ups_db
import consolidation
from system_info import get_battery

router = APIRouter()

VMLX_BASE_URL = os.getenv("VMLX_BASE_URL", "http://localhost:8080")
VMLX_MODEL = os.getenv("VMLX_MODEL", "local-model")
VMLX_API_KEY = os.getenv("VMLX_API_KEY", "local")
MAX_CONTEXT_MESSAGES = int(os.getenv("MAX_CONTEXT_MESSAGES", "20"))
MAX_MEMORY_RESULTS = int(os.getenv("MAX_MEMORY_RESULTS", "5"))
MAX_TOKENS = int(os.getenv("MAX_TOKENS", "3072"))
MAX_TOOL_ITERATIONS = 5

TOOL_CALL_RE = _re.compile(r'<tool_call>(.*?)(?:</tool_call>|$)', _re.DOTALL)

client = AsyncOpenAI(base_url=f"{VMLX_BASE_URL}/v1", api_key=VMLX_API_KEY)

_sessions: dict[str, list[dict]] = defaultdict(list)


# ── Background generation state ───────────────────────────────────────────────

class _GenState:
    """Holds in-flight generation events. Lives until the task completes."""

    def __init__(self):
        self.events: list[dict] = []
        self.task: asyncio.Task | None = None
        self._subs: list[asyncio.Queue] = []

    def push(self, ev: dict):
        self.events.append(ev)
        for q in self._subs:
            q.put_nowait(ev)

    def subscribe(self, offset: int = 0) -> asyncio.Queue:
        """Return a queue pre-filled with events[offset:] plus future events."""
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


# ── SSE helpers ────────────────────────────────────────────────────────────────

async def _follow_state(state: _GenState):
    """Async generator that yields SSE lines from a _GenState."""
    q = state.subscribe(0)
    try:
        while True:
            try:
                ev = await asyncio.wait_for(q.get(), timeout=30)
            except asyncio.TimeoutError:
                yield ": heartbeat\n\n"
                continue
            yield f"data: {json.dumps(ev)}\n\n"
            if ev.get('done') or ev.get('error'):
                return
    finally:
        state.unsubscribe(q)


def _sse_response(gen):
    return StreamingResponse(
        gen,
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


# ── Core generation task ───────────────────────────────────────────────────────

async def _do_generate(
    session_id: str,
    state: _GenState,
    working_messages: list,
    tools: list,
    user_message: str,
    enabled_plugins: list,
):
    """Runs generation independently of any HTTP connection."""
    full_content = ""
    full_thinking = ""
    full_tool_calls = []

    try:
        for _iteration in range(MAX_TOOL_ITERATIONS):
            stream = await client.chat.completions.create(
                model=VMLX_MODEL,
                messages=working_messages,
                stream=True,
                max_tokens=MAX_TOKENS,
                timeout=120,
            )

            content_buf = ""

            async for chunk in stream:
                if not chunk.choices:
                    continue
                choice = chunk.choices[0]
                delta = choice.delta

                reasoning = _get_reasoning(delta)
                if reasoning:
                    full_thinking += reasoning
                    state.push({'reasoning_delta': reasoning})

                if delta.content:
                    content_buf += delta.content

            print(f"[chat] iteration={_iteration} content_len={len(content_buf)}")

            tool_match = TOOL_CALL_RE.search(content_buf)
            if tool_match and tool_match.group(1).strip() and tools:
                before = content_buf[:content_buf.index("<tool_call>")].strip()
                if before:
                    full_content += before
                    state.push({'delta': before})

                for match in TOOL_CALL_RE.finditer(content_buf):
                    try:
                        tc = json.loads(match.group(1).strip())
                        name = tc.get("name", "")
                        args = tc.get("args", tc.get("arguments", {}))
                    except Exception as parse_err:
                        print(f"[chat] tool call parse error: {parse_err} raw={match.group(1)!r}")
                        continue

                    state.push({'tool_call': {'name': name, 'args': args}})

                    result = await execute_tool(
                        enabled_plugins, name, args,
                        context={"session_id": session_id, "sessions": _sessions},
                    )
                    full_tool_calls.append({"name": name, "args": args, "result": result})
                    state.push({'tool_result': {'name': name, 'output': result}})

                    working_messages.append({"role": "assistant", "content": content_buf})
                    working_messages.append({
                        "role": "user",
                        "content": f"[Tool result: {name}]\n{result}\n\nNow respond based on this."
                    })

                continue

            if content_buf:
                full_content += content_buf
                state.push({'delta': content_buf})
            break

    except asyncio.CancelledError:
        pass  # stopped by user — fall through to save what we have
    except Exception as e:
        state.push({'error': str(e)})
    finally:
        tool_calls_json = json.dumps(full_tool_calls) if full_tool_calls else None
        sessions_db.append_message(session_id, "assistant", full_content, full_thinking or None, tool_calls_json)
        sessions_db.touch_session(session_id)
        history = _sessions[session_id]
        if full_content:
            history.append({"role": "assistant", "content": full_content})
        _gen_states.pop(session_id, None)
        state.push({'done': True})

    if full_content:
        await maybe_store_exchange(user_message, full_content)
        await extract_and_store_traits(user_message, full_content)
        consolidation.touch_activity()


# ── Misc helpers (unchanged) ───────────────────────────────────────────────────

def _get_reasoning(delta_or_message) -> str:
    return (
        getattr(delta_or_message, 'reasoning_content', None)
        or (getattr(delta_or_message, 'model_extra', None) or {}).get('reasoning_content')
        or ''
    )


def _build_tool_prompt(tools: list[dict]) -> str:
    if not tools:
        return ""
    lines = [
        "\n\nYou have tools available. To call one, output exactly:",
        '<tool_call>{"name": "TOOL_NAME", "args": {ARGS_JSON}}</tool_call>',
        "Stop after the closing tag and wait for the result before continuing.",
        "\nTools:",
    ]
    for t in tools:
        fn = t["function"]
        params = ", ".join(fn["parameters"]["properties"].keys())
        lines.append(f'- {fn["name"]}({params}): {fn["description"]}')
    return "\n".join(lines)


async def _build_system(query: str, tools: list[dict]) -> str:
    now = _dt.now().strftime("%A, %B %-d %Y · %I:%M %p")
    battery = get_battery()
    if battery and battery["level"] is not None:
        if battery["charging"]:
            batt_str = f"charging at {battery['level']}%"
        elif battery["time_remaining"]:
            batt_str = f"{battery['level']}% battery ({battery['time_remaining']} remaining)"
        else:
            batt_str = f"{battery['level']}% battery"
        context_line = f"Today is {now}. Thomas's machine is {batt_str}."
    else:
        context_line = f"Today is {now}."
    character_context = await get_character_context()
    memories = await retrieve_memories(query, n_results=MAX_MEMORY_RESULTS)
    tool_prompt = _build_tool_prompt(tools)
    base = context_line + "\n\n" + character_context + tool_prompt

    if not memories:
        return base

    personal = [m for m in memories if m.get("collection") != "knowledge"]
    knowledge = [m for m in memories if m.get("collection") == "knowledge"]

    sections = []
    if personal:
        lines = "\n".join(f"- {m['content']}" for m in personal)
        sections.append(f"Relevant memories:\n{lines}")
    if knowledge:
        lines = []
        for m in knowledge:
            meta = m.get("metadata", {})
            source = meta.get("show", "Podcast")
            episode = meta.get("episode", "")
            pub = meta.get("pub_date", "")[:10]
            label = source
            if episode:
                label += f" — {episode}"
            if pub:
                label += f" ({pub})"
            snippet = m['content'][:400].rsplit(' ', 1)[0] + "…"
            lines.append(f"- [{label}] {snippet}")
        sections.append("Relevant knowledge from ingested content:\n" + "\n".join(lines))

    recent_learnings = questions_db.get_recent_resolutions(days=7)
    if recent_learnings:
        lines = [f"- {q['question']} → {q['thomas_answer']}" for q in recent_learnings[:5]]
        sections.append("Recent learnings (Thomas answered these for you this week):\n" + "\n".join(lines))

    open_followups = follow_ups_db.get_open()
    if open_followups:
        lines = [f"- [{f['id']}] {f['content']}" for f in open_followups[:5]]
        sections.append("Open follow-ups (check if any have been addressed):\n" + "\n".join(lines))

    system = base + "\n\n" + "\n\n".join(sections)
    if knowledge:
        print(f"[chat] injecting {len(knowledge)} knowledge chunks into system prompt")
    return system


def _load_session_from_db(session_id: str) -> list[dict]:
    rows = sessions_db.get_messages(session_id)
    return [{"role": r["role"], "content": r["content"]} for r in rows]


def _get_history(session_id: str) -> list[dict]:
    if not _sessions[session_id]:
        loaded = _load_session_from_db(session_id)
        if loaded:
            _sessions[session_id] = loaded
    return _sessions[session_id]


# ── Routes ─────────────────────────────────────────────────────────────────────

@router.get("/sessions")
async def list_sessions():
    return sessions_db.list_sessions()


@router.post("/chat/stream")
async def chat_stream(req: ChatRequest):
    session_id = req.session_id
    history = _get_history(session_id)
    tools = get_tools_for_plugins(req.enabled_plugins)
    system = await _build_system(req.message, tools)

    base_messages = [{"role": "system", "content": system}]
    base_messages += history[-MAX_CONTEXT_MESSAGES:]
    base_messages.append({"role": "user", "content": req.message})

    sessions_db.ensure_session(session_id, req.message)
    sessions_db.append_message(session_id, "user", req.message)
    history.append({"role": "user", "content": req.message})

    # Cancel any previous generation for this session
    if session_id in _gen_states:
        existing = _gen_states.pop(session_id)
        if existing.task and not existing.task.done():
            existing.task.cancel()

    state = _GenState()
    _gen_states[session_id] = state
    state.task = asyncio.create_task(
        _do_generate(session_id, state, base_messages, tools, req.message, req.enabled_plugins)
    )

    return _sse_response(_follow_state(state))


@router.get("/sessions/{session_id}/active")
async def session_active(session_id: str):
    return {"active": session_id in _gen_states}


@router.get("/sessions/{session_id}/stream")
async def session_stream(session_id: str):
    """Reconnect to an in-progress generation."""
    if session_id not in _gen_states:
        async def _done():
            yield f"data: {json.dumps({'done': True})}\n\n"
        return _sse_response(_done())
    return _sse_response(_follow_state(_gen_states[session_id]))


@router.delete("/sessions/{session_id}/generation")
async def cancel_generation(session_id: str):
    """Stop an in-progress generation (Stop button)."""
    if session_id in _gen_states:
        state = _gen_states[session_id]
        if state.task and not state.task.done():
            state.task.cancel()
    return {"cancelled": True}


@router.post("/chat", response_model=ChatResponse)
async def chat(req: ChatRequest):
    history = _get_history(req.session_id)
    system = await _build_system(req.message, [])

    messages = [{"role": "system", "content": system}]
    messages += history[-MAX_CONTEXT_MESSAGES:]
    messages.append({"role": "user", "content": req.message})

    sessions_db.ensure_session(req.session_id, req.message)

    response = await client.chat.completions.create(
        model=VMLX_MODEL,
        messages=messages,
        max_tokens=MAX_TOKENS,
    )

    msg = response.choices[0].message
    answer = msg.content or ""
    thinking = _get_reasoning(msg) or None

    sessions_db.append_message(req.session_id, "user", req.message)
    sessions_db.append_message(req.session_id, "assistant", answer, thinking)
    sessions_db.touch_session(req.session_id)

    history.append({"role": "user", "content": req.message})
    history.append({"role": "assistant", "content": answer})

    await maybe_store_exchange(req.message, answer)
    await extract_and_store_traits(req.message, answer)

    return ChatResponse(message=answer, thinking=thinking, session_id=req.session_id)


@router.get("/sessions/{session_id}/history")
async def get_history(session_id: str):
    rows = sessions_db.get_messages(session_id)
    history = [
        {
            "role": r["role"],
            "content": r["content"],
            "thinking": r["thinking"],
            "tool_calls": json.loads(r["tool_calls"]) if r["tool_calls"] else None,
        }
        for r in rows
    ]
    return {"session_id": session_id, "history": history}


@router.delete("/sessions/{session_id}")
async def delete_session(session_id: str):
    if session_id in _gen_states:
        state = _gen_states.pop(session_id)
        if state.task and not state.task.done():
            state.task.cancel()
    sessions_db.delete_session(session_id)
    _sessions.pop(session_id, None)
    return {"cleared": session_id}
