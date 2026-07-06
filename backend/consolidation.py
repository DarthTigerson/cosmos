import asyncio
import logging
import os
import time

from openai import AsyncOpenAI
from memory import get_unprocessed_exchanges, mark_exchanges_consolidated, store_insight

log = logging.getLogger("cosmos.consolidation")

VMLX_BASE_URL = os.getenv("VMLX_BASE_URL", "http://localhost:8080")
VMLX_MODEL = os.getenv("VMLX_MODEL", "local-model")
VMLX_API_KEY = os.getenv("VMLX_API_KEY", "local")

# How often to check (seconds). Only consolidates if idle long enough.
CONSOLIDATION_INTERVAL = int(os.getenv("CONSOLIDATION_INTERVAL", "600"))   # check every 10 min
IDLE_THRESHOLD = int(os.getenv("CONSOLIDATION_IDLE_THRESHOLD", "1800"))    # require 30 min idle
MIN_EXCHANGES = int(os.getenv("CONSOLIDATION_MIN_EXCHANGES", "5"))

_client = AsyncOpenAI(base_url=f"{VMLX_BASE_URL}/v1", api_key=VMLX_API_KEY)
_last_activity: float = 0.0


def touch_activity():
    """Called by chat.py after each completed exchange."""
    global _last_activity
    _last_activity = time.time()


async def consolidate_memories():
    """Extract durable insights from unconsolidated conversation exchanges."""
    exchanges = await get_unprocessed_exchanges(limit=15)
    if len(exchanges) < MIN_EXCHANGES:
        log.debug(f"[consolidation] {len(exchanges)} exchanges — below threshold, skipping")
        return

    log.info(f"[consolidation] processing {len(exchanges)} exchanges")
    exchange_text = "\n\n---\n\n".join(e["content"] for e in exchanges)

    prompt = (
        "From the conversation exchanges below, extract durable facts, preferences, and insights "
        "that are worth remembering long-term. Focus on:\n"
        "- Things Thomas prefers, values, or dislikes\n"
        "- Facts Thomas has shared about himself, his work, or his life\n"
        "- Patterns or recurring themes\n"
        "- Things Cosmos has learned, decided, or noticed about herself or Thomas\n\n"
        "Return a plain list of concise statements, one per line, no bullet points or numbering. "
        "Each statement should be self-contained and factual. "
        "Skip trivial chitchat or greetings. "
        "If there is genuinely nothing durable to extract, return only the single word NOTHING.\n\n"
        f"Exchanges:\n{exchange_text}"
    )

    try:
        resp = await _client.chat.completions.create(
            model=VMLX_MODEL,
            messages=[{"role": "user", "content": prompt}],
            max_tokens=512,
            timeout=90,
        )
        text = (resp.choices[0].message.content or "").strip()
    except Exception as e:
        log.error(f"[consolidation] LLM call failed: {e}")
        return

    if not text or text.upper() == "NOTHING":
        log.info("[consolidation] no durable insights found")
    else:
        insights = [line.strip() for line in text.splitlines() if line.strip()]
        for insight in insights:
            await store_insight(insight)
        log.info(f"[consolidation] stored {len(insights)} insights from {len(exchanges)} exchanges")

    await mark_exchanges_consolidated([e["id"] for e in exchanges])


async def consolidation_loop():
    """Background loop: runs consolidation when the system has been idle long enough."""
    log.info(f"[consolidation] loop started (check every {CONSOLIDATION_INTERVAL}s, idle threshold {IDLE_THRESHOLD}s)")
    await asyncio.sleep(60)  # warm-up delay after startup
    while True:
        await asyncio.sleep(CONSOLIDATION_INTERVAL)
        idle = time.time() - _last_activity
        if _last_activity > 0 and idle < IDLE_THRESHOLD:
            log.debug(f"[consolidation] system active ({idle:.0f}s ago), skipping")
            continue
        try:
            await consolidate_memories()
        except Exception as e:
            log.error(f"[consolidation] unhandled error: {e}")
