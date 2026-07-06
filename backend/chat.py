import json
import os
import re
from collections import defaultdict
from fastapi import APIRouter
from fastapi.responses import StreamingResponse
from openai import AsyncOpenAI
from models import ChatRequest, ChatResponse
from memory import retrieve_memories, maybe_store_exchange
from character import get_character_context, extract_and_store_traits

router = APIRouter()

VMLX_BASE_URL = os.getenv("VMLX_BASE_URL", "http://localhost:8080")
VMLX_MODEL = os.getenv("VMLX_MODEL", "local-model")
VMLX_API_KEY = os.getenv("VMLX_API_KEY", "local")
MAX_CONTEXT_MESSAGES = int(os.getenv("MAX_CONTEXT_MESSAGES", "20"))
MAX_MEMORY_RESULTS = int(os.getenv("MAX_MEMORY_RESULTS", "5"))
MAX_TOKENS = int(os.getenv("MAX_TOKENS", "3072"))

client = AsyncOpenAI(base_url=f"{VMLX_BASE_URL}/v1", api_key=VMLX_API_KEY)

_sessions: dict[str, list[dict]] = defaultdict(list)


def _extract_thinking(content: str) -> tuple[str, str | None]:
    match = re.search(r"<think>(.*?)</think>", content, re.DOTALL)
    if match:
        thinking = match.group(1).strip()
        answer = content[match.end():].strip()
        return answer, thinking
    return content.strip(), None


async def _build_system(query: str) -> str:
    character_context = await get_character_context()

    memories = await retrieve_memories(query, n_results=MAX_MEMORY_RESULTS)
    if not memories:
        return character_context

    memory_block = "\n".join(f"- {m['content']}" for m in memories)
    return f"{character_context}\n\nRelevant memories:\n{memory_block}"


@router.post("/chat", response_model=ChatResponse)
async def chat(req: ChatRequest):
    history = _sessions[req.session_id]
    system = await _build_system(req.message)

    messages = [{"role": "system", "content": system}]
    messages += history[-MAX_CONTEXT_MESSAGES:]
    messages.append({"role": "user", "content": req.message})

    response = await client.chat.completions.create(
        model=VMLX_MODEL,
        messages=messages,
    )

    raw = response.choices[0].message.content
    answer, thinking = _extract_thinking(raw)

    history.append({"role": "user", "content": req.message})
    history.append({"role": "assistant", "content": answer})

    await maybe_store_exchange(req.message, answer)
    await extract_and_store_traits(req.message, answer)

    return ChatResponse(
        message=answer,
        thinking=thinking,
        session_id=req.session_id,
    )


@router.post("/chat/stream")
async def chat_stream(req: ChatRequest):
    history = _sessions[req.session_id]
    system = await _build_system(req.message)

    messages = [{"role": "system", "content": system}]
    messages += history[-MAX_CONTEXT_MESSAGES:]
    messages.append({"role": "user", "content": req.message})

    async def generate():
        full_text = ""
        try:
            stream = await client.chat.completions.create(
                model=VMLX_MODEL,
                messages=messages,
                stream=True,
                max_tokens=MAX_TOKENS,
            )
            async for chunk in stream:
                delta = chunk.choices[0].delta.content or ""
                if delta:
                    full_text += delta
                    yield f"data: {json.dumps({'delta': delta})}\n\n"
        except Exception as e:
            yield f"data: {json.dumps({'error': str(e)})}\n\n"
            return

        answer, thinking = _extract_thinking(full_text)
        history.append({"role": "user", "content": req.message})
        history.append({"role": "assistant", "content": answer})
        await maybe_store_exchange(req.message, answer)
        await extract_and_store_traits(req.message, answer)
        yield f"data: {json.dumps({'done': True})}\n\n"

    return StreamingResponse(
        generate(),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


@router.get("/sessions/{session_id}/history")
async def get_history(session_id: str):
    return {"session_id": session_id, "history": _sessions.get(session_id, [])}


@router.delete("/sessions/{session_id}")
async def clear_session(session_id: str):
    _sessions.pop(session_id, None)
    return {"cleared": session_id}
