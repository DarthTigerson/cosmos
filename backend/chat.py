import json
import os
from collections import defaultdict
from fastapi import APIRouter
from fastapi.responses import StreamingResponse
from openai import AsyncOpenAI
from models import ChatRequest, ChatResponse
from memory import retrieve_memories, maybe_store_exchange
from character import get_character_context, extract_and_store_traits
import sessions_db

router = APIRouter()

VMLX_BASE_URL = os.getenv("VMLX_BASE_URL", "http://localhost:8080")
VMLX_MODEL = os.getenv("VMLX_MODEL", "local-model")
VMLX_API_KEY = os.getenv("VMLX_API_KEY", "local")
MAX_CONTEXT_MESSAGES = int(os.getenv("MAX_CONTEXT_MESSAGES", "20"))
MAX_MEMORY_RESULTS = int(os.getenv("MAX_MEMORY_RESULTS", "5"))
MAX_TOKENS = int(os.getenv("MAX_TOKENS", "3072"))

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

    messages = [{"role": "system", "content": system}]
    messages += history[-MAX_CONTEXT_MESSAGES:]
    messages.append({"role": "user", "content": req.message})

    # Persist user message immediately so it survives client disconnects
    sessions_db.ensure_session(req.session_id, req.message)
    sessions_db.append_message(req.session_id, "user", req.message)
    history.append({"role": "user", "content": req.message})

    async def generate():
        full_content = ""
        full_thinking = ""
        try:
            stream = await client.chat.completions.create(
                model=VMLX_MODEL,
                messages=messages,
                stream=True,
                max_tokens=MAX_TOKENS,
            )
            async for chunk in stream:
                delta = chunk.choices[0].delta
                reasoning = _get_reasoning(delta)
                content = delta.content or ""
                if reasoning:
                    full_thinking += reasoning
                    yield f"data: {json.dumps({'reasoning_delta': reasoning})}\n\n"
                if content:
                    full_content += content
                    yield f"data: {json.dumps({'delta': content})}\n\n"
        except Exception as e:
            yield f"data: {json.dumps({'error': str(e)})}\n\n"
            return
        finally:
            # Runs even on client disconnect (GeneratorExit) — sync DB calls only
            sessions_db.append_message(req.session_id, "assistant", full_content, full_thinking or None)
            sessions_db.touch_session(req.session_id)
            if full_content:
                history.append({"role": "assistant", "content": full_content})

        # Only reached on clean completion (not on disconnect or error)
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
    history = [{"role": r["role"], "content": r["content"], "thinking": r["thinking"]} for r in rows]
    return {"session_id": session_id, "history": history}


@router.delete("/sessions/{session_id}")
async def delete_session(session_id: str):
    sessions_db.delete_session(session_id)
    _sessions.pop(session_id, None)
    return {"cleared": session_id}
