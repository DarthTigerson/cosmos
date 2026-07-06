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

router = APIRouter()

VMLX_BASE_URL = os.getenv("VMLX_BASE_URL", "http://localhost:8080")
VMLX_MODEL = os.getenv("VMLX_MODEL", "local-model")
VMLX_API_KEY = os.getenv("VMLX_API_KEY", "local")
MAX_CONTEXT_MESSAGES = int(os.getenv("MAX_CONTEXT_MESSAGES", "20"))
MAX_MEMORY_RESULTS = int(os.getenv("MAX_MEMORY_RESULTS", "5"))
MAX_TOKENS = int(os.getenv("MAX_TOKENS", "3072"))
MAX_TOOL_ITERATIONS = 5

# Match both closed and unclosed tool call tags
TOOL_CALL_RE = _re.compile(r'<tool_call>(.*?)(?:</tool_call>|$)', _re.DOTALL)

client = AsyncOpenAI(base_url=f"{VMLX_BASE_URL}/v1", api_key=VMLX_API_KEY)

_sessions: dict[str, list[dict]] = defaultdict(list)


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
    character_context = await get_character_context()
    memories = await retrieve_memories(query, n_results=MAX_MEMORY_RESULTS)
    tool_prompt = _build_tool_prompt(tools)
    base = f"Today is {now}.\n\n" + character_context + tool_prompt

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

    # Inject recent loop closures so Cosmos knows when Thomas has answered her questions
    recent_learnings = questions_db.get_recent_resolutions(days=7)
    if recent_learnings:
        lines = [f"- {q['question']} → {q['thomas_answer']}" for q in recent_learnings[:5]]
        sections.append("Recent learnings (Thomas answered these for you this week):\n" + "\n".join(lines))

    # Surface open follow-ups at the start of every session
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


@router.get("/sessions")
async def list_sessions():
    return sessions_db.list_sessions()


@router.post("/chat/stream")
async def chat_stream(req: ChatRequest):
    history = _get_history(req.session_id)
    tools = get_tools_for_plugins(req.enabled_plugins)
    system = await _build_system(req.message, tools)

    base_messages = [{"role": "system", "content": system}]
    base_messages += history[-MAX_CONTEXT_MESSAGES:]
    base_messages.append({"role": "user", "content": req.message})

    sessions_db.ensure_session(req.session_id, req.message)
    sessions_db.append_message(req.session_id, "user", req.message)
    history.append({"role": "user", "content": req.message})

    async def generate():
        full_content = ""
        full_thinking = ""
        full_tool_calls = []
        working_messages = list(base_messages)

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
                        yield f"data: {json.dumps({'reasoning_delta': reasoning})}\n\n"

                    if delta.content:
                        content_buf += delta.content

                print(f"[chat] iteration={_iteration} content_len={len(content_buf)}")

                # Check if the model embedded a tool call in its content
                tool_match = TOOL_CALL_RE.search(content_buf)
                if tool_match and tool_match.group(1).strip() and tools:
                    # Yield any text before the tool call tag
                    before = content_buf[:content_buf.index("<tool_call>")].strip()
                    if before:
                        full_content += before
                        yield f"data: {json.dumps({'delta': before})}\n\n"

                    for match in TOOL_CALL_RE.finditer(content_buf):
                        try:
                            tc = json.loads(match.group(1).strip())
                            name = tc.get("name", "")
                            args = tc.get("args", tc.get("arguments", {}))
                        except Exception as parse_err:
                            print(f"[chat] tool call parse error: {parse_err} raw={match.group(1)!r}")
                            continue

                        yield f"data: {json.dumps({'tool_call': {'name': name, 'args': args}})}\n\n"
                        yield ": heartbeat\n\n"

                        result = await execute_tool(req.enabled_plugins, name, args, context={"session_id": req.session_id, "sessions": _sessions})
                        full_tool_calls.append({"name": name, "args": args, "result": result})

                        yield f"data: {json.dumps({'tool_result': {'name': name, 'output': result}})}\n\n"

                        working_messages.append({"role": "assistant", "content": content_buf})
                        working_messages.append({
                            "role": "user",
                            "content": f"[Tool result: {name}]\n{result}\n\nNow respond based on this."
                        })

                    continue  # next iteration → model responds with the result

                # No tool call — yield buffered content and finish
                if content_buf:
                    full_content += content_buf
                    yield f"data: {json.dumps({'delta': content_buf})}\n\n"
                break

        except Exception as e:
            yield f"data: {json.dumps({'error': str(e)})}\n\n"
            return
        finally:
            tool_calls_json = json.dumps(full_tool_calls) if full_tool_calls else None
            sessions_db.append_message(req.session_id, "assistant", full_content, full_thinking or None, tool_calls_json)
            sessions_db.touch_session(req.session_id)
            if full_content:
                history.append({"role": "assistant", "content": full_content})

        await maybe_store_exchange(req.message, full_content)
        await extract_and_store_traits(req.message, full_content)
        consolidation.touch_activity()
        yield f"data: {json.dumps({'done': True})}\n\n"

    return StreamingResponse(
        generate(),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


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
    sessions_db.delete_session(session_id)
    _sessions.pop(session_id, None)
    return {"cleared": session_id}
