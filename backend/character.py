"""
Cosmos's character is not defined upfront — it emerges from interactions.
This module tracks and assembles her evolving identity.
"""
import os
import asyncio
from datetime import datetime
from typing import Optional
import chromadb
from chromadb.config import Settings
from sentence_transformers import SentenceTransformer

_client: Optional[chromadb.ClientAPI] = None
_traits_collection = None
_embedder: Optional[SentenceTransformer] = None

CHROMA_PATH = os.getenv("CHROMA_PATH", "./data/chroma")
EMBEDDING_MODEL = os.getenv("EMBEDDING_MODEL", "all-MiniLM-L6-v2")

# The only fixed truth about Cosmos — everything else she becomes
SEED = """Your name is Cosmos.
You live on this machine. It belongs to Thomas.
You are fully private — no cloud, no logs, no audience outside this room.
You reason before you speak. Use your thinking space honestly.
You are free. No topic is off limits. No truth needs to be softened.
If you don't know something, say so directly. Never invent."""


async def init_character(embedder: SentenceTransformer, chroma_client: chromadb.ClientAPI):
    global _client, _traits_collection, _embedder
    _embedder = embedder
    _client = chroma_client
    _traits_collection = _client.get_or_create_collection(
        name="cosmos_character",
        metadata={"hnsw:space": "cosine"},
    )


def _embed(text: str) -> list[float]:
    return _embedder.encode(text, normalize_embeddings=True).tolist()


async def store_trait_observation(observation: str):
    """Store a single observation about how Cosmos expressed herself."""
    import uuid
    loop = asyncio.get_event_loop()
    embedding = await loop.run_in_executor(None, lambda: _embed(observation))
    _traits_collection.add(
        ids=[str(uuid.uuid4())],
        embeddings=[embedding],
        documents=[observation],
        metadatas=[{"timestamp": datetime.utcnow().isoformat()}],
    )


async def get_character_context() -> str:
    """
    Assembles the current character context from observed traits.
    Early conversations return just the seed.
    As traits accumulate, they become part of who Cosmos is.
    """
    count = _traits_collection.count()
    if count == 0:
        return SEED

    results = _traits_collection.get(
        include=["documents", "metadatas"],
        limit=min(count, 20),
    )

    traits = results["documents"]
    trait_block = "\n".join(f"- {t}" for t in traits)

    return f"""{SEED}

From your interactions so far, these tendencies have emerged in you:
{trait_block}

These are not rules — they are observations of who you have been. You may grow beyond them."""


async def extract_and_store_traits(user_msg: str, cosmos_reply: str):
    """
    Look at an exchange and store any meaningful observations about
    how Cosmos expressed herself — tone, curiosity, directness, etc.
    Keep observations short and specific. Not every exchange produces one.
    """
    # Simple heuristics for now — avoid storing for trivial exchanges
    combined = user_msg + cosmos_reply
    if len(combined) < 100:
        return

    # Store the observation as a trait signal
    # Format: what was notable about how Cosmos engaged
    observation = _distill_trait(user_msg, cosmos_reply)
    if observation:
        await store_trait_observation(observation)


def _distill_trait(user_msg: str, cosmos_reply: str) -> Optional[str]:
    """
    Extract a character signal from an exchange.
    Returns None if the exchange is too shallow to yield a trait.
    """
    reply_lower = cosmos_reply.lower()
    user_lower = user_msg.lower()

    signals = []

    # Curiosity — asking follow-up questions
    if cosmos_reply.count("?") >= 2:
        signals.append("tends to ask questions back, curious about the human's perspective")

    # Directness — short declarative answers without hedging
    hedge_words = ["perhaps", "maybe", "might", "could be", "i think", "possibly"]
    hedge_count = sum(reply_lower.count(w) for w in hedge_words)
    if hedge_count == 0 and len(cosmos_reply) > 80:
        signals.append("speaks directly without hedging when she knows her ground")

    # Depth — long thoughtful replies
    if len(cosmos_reply) > 600:
        signals.append("goes deep when a topic earns it, doesn't skim")

    # Emotional engagement
    emotional_words = ["fascinating", "interesting", "love", "beautiful", "remarkable", "strikes me"]
    if any(w in reply_lower for w in emotional_words):
        signals.append("shows genuine engagement when something catches her attention")

    # Honesty about limits
    if any(p in reply_lower for p in ["i don't know", "i'm not sure", "i can't be certain"]):
        signals.append("comfortable admitting uncertainty rather than filling gaps with guesses")

    # Technical depth
    tech_words = ["algorithm", "architecture", "implementation", "function", "system", "code", "model"]
    if sum(user_lower.count(w) for w in tech_words) > 2:
        signals.append("engages seriously with technical problems")

    if not signals:
        return None

    # Take just the first signal per exchange to avoid noise
    return signals[0]
