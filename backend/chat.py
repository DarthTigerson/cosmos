import json
import os
from collections import defaultdict
from fastapi import APIRouter
from fastapi.responses import StreamingResponse
from openai import AsyncOpenAI
from models import ChatRequest, ChatResponse
from memory import retrieve_memories, maybe_store_exchange
from character import get_character_context, extract_and_store_traits
from plugins import get_tools_for_plugins, execute_tool
import sessions_db

router = APIRouter()

VMLX_BASE_URL = os.getenv("VMLX_BASE_URL", "http://localhost:8080")
VMLX_MODEL = os.getenv("VMLX_MODEL", "local-model")
VMLX_API_KEY = os.getenv("VMLX_API_KEY", "local")
MAX_CONTEXT_MESSAGES = int(os.getenv("MAX_CONTEXT_MESSAGES", "20"))
MAX_MEMORY_RESULTS = int(os.getenv("MAX_MEMORY_RESULTS", "5"))
MAX_TOKENS = int(os.getenv("MAX_TOKENS", "3072"))
MAX_TOOL_ITERATIONS = 5

client = AsyncOpenAI(base_url=f"{VMLX_BASE_URL}/v1", api_key=VMLX_API_KEY)

# In-memory cache — DB is the source of truth
_sessions: dict[str, list[dict]] = defaultdict(list)


def _get_reasoning(delta_or_message) -> str:
    return (
        getattr(delta_or_message, 'reasoning_content', None)
        or (getattr(delta_or_message, 'model_extra', None) or {}).get('reasoning_content')
        or ''
    )


async def _build_system(query: str) -> str:
    character_context = await get_character_context()
    memories = await retrieve_memories(query, n_results=MAX_MEMORY_RESULTS)
    if not memories:
        return character_context
    memory_block = "\n".join(f"- {m['content']}" for m in memories)
    return f"{character_context}\n\nRelevant memories:\n{memory_block}"


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
    system = await _build_system(req.message)
    tools = get_tools_for_plugins(req.enabled_plugins)

    base_messages = [{"role": "system", "content": system}]
    base_messages += history[-MAX_CONTEXT_MESSAGES:]
    base_messages.append({"role": "user", "content": req.message})

    # Persist user message immediately so it survives client disconnects
    sessions_db.ensure_session(req.session_id, req.message)
    sessions_db.append_message(req.session_id, "user", req.message)
    history.append({"role": "user", "content": req.message})

    async def generate():
        full_content = ""
        full_thinking = ""
        full_tool_calls = []  # [{name, args, result}] accumulated across all iterations
        # working_messages grows as tool results are appended
        working_messages = list(base_messages)

        try:
            for _iteration in range(MAX_TOOL_ITERATIONS):
                create_kwargs = dict(
                    model=VMLX_MODEL,
                    messages=working_messages,
                    stream=True,
                    max_tokens=MAX_TOKENS,
                )
                if tools:
                    create_kwargs["tools"] = tools

                stream = await client.chat.completions.create(**create_kwargs)

                tool_calls_buf: dict[int, dict] = {}
                finish_reason = None

                async for chunk in stream:
                    choice = chunk.choices[0]
                    delta = choice.delta
                    finish_reason = choice.finish_reason or finish_reason

                    # Reasoning (thinking)
                    reasoning = _get_reasoning(delta)
                    if reasoning:
                        full_thinking += reasoning
                        yield f"data: {json.dumps({'reasoning_delta': reasoning})}\n\n"

                    # Content tokens
                    if delta.content:
                        full_content += delta.content
                        yield f"data: {json.dumps({'delta': delta.content})}\n\n"

                    # Tool call chunks — accumulate across multiple chunks
                    tc_list = getattr(delta, 'tool_calls', None) or []
                    for tc in tc_list:
                        idx = tc.index
                        if idx not in tool_calls_buf:
                            tool_calls_buf[idx] = {"id": "", "name": "", "args": ""}
                        if tc.id:
                            tool_calls_buf[idx]["id"] = tc.id
                        if tc.function and tc.function.name:
                            tool_calls_buf[idx]["name"] = tc.function.name
                        if tc.function and tc.function.arguments:
                            tool_calls_buf[idx]["args"] += tc.function.arguments

                # ── Tool calls to execute ──────────────────────────────────
                if finish_reason == "tool_calls" and tool_calls_buf:
                    assistant_tool_calls = []
                    for idx in sorted(tool_calls_buf.keys()):
                        tc = tool_calls_buf[idx]
                        try:
                            args = json.loads(tc["args"]) if tc["args"] else {}
                        except Exception:
                            args = {}

                        yield f"data: {json.dumps({'tool_call': {'name': tc['name'], 'args': args}})}\n\n"

                        result = await execute_tool(req.enabled_plugins, tc["name"], args)
                        full_tool_calls.append({"name": tc["name"], "args": args, "result": result})

                        yield f"data: {json.dumps({'tool_result': {'name': tc['name'], 'output': result}})}\n\n"

                        assistant_tool_calls.append({
                            "id": tc["id"],
                            "type": "function",
                            "function": {"name": tc["name"], "arguments": tc["args"]},
                        })
                        working_messages.append({
                            "role": "tool",
                            "tool_call_id": tc["id"],
                            "content": result,
                        })

                    # Inject assistant's tool_calls message before the tool results
                    working_messages.insert(
                        len(working_messages) - len(tool_calls_buf),
                        {"role": "assistant", "tool_calls": assistant_tool_calls},
                    )
                    continue  # next LLM call with tool results

                break  # finish_reason == "stop" or no tool calls

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
        yield f"data: {json.dumps({'done': True})}\n\n"

    return StreamingResponse(
        generate(),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


@router.post("/chat", response_model=ChatResponse)
async def chat(req: ChatRequest):
    history = _get_history(req.session_id)
    system = await _build_system(req.message)

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
