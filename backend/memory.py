import os
import uuid
import asyncio
from datetime import datetime
from typing import Optional
import chromadb
from chromadb.config import Settings
from sentence_transformers import SentenceTransformer

_client: Optional[chromadb.ClientAPI] = None
_memories = None      # personal memories + conversation exchanges
_knowledge = None     # podcast chunks + verified Q&As
_embedder: Optional[SentenceTransformer] = None

CHROMA_PATH = os.getenv("CHROMA_PATH", "./data/chroma")
EMBEDDING_MODEL = os.getenv("EMBEDDING_MODEL", "all-MiniLM-L6-v2")

# Only surface knowledge chunks above this cosine similarity
KNOWLEDGE_THRESHOLD = float(os.getenv("KNOWLEDGE_THRESHOLD", "0.35"))


async def init_memory():
    global _client, _memories, _knowledge, _embedder
    print(f"[memory] Loading embedding model: {EMBEDDING_MODEL}")
    loop = asyncio.get_event_loop()
    _embedder = await loop.run_in_executor(
        None, lambda: SentenceTransformer(EMBEDDING_MODEL)
    )
    os.makedirs(CHROMA_PATH, exist_ok=True)
    _client = chromadb.PersistentClient(
        path=CHROMA_PATH,
        settings=Settings(anonymized_telemetry=False),
    )
    _memories = _client.get_or_create_collection(
        name="cosmos_memory",
        metadata={"hnsw:space": "cosine"},
    )
    _knowledge = _client.get_or_create_collection(
        name="cosmos_knowledge",
        metadata={"hnsw:space": "cosine"},
    )
    print(f"[memory] Ready. {_memories.count()} memories, {_knowledge.count()} knowledge chunks.")

    import character
    await character.init_character(_embedder, _client)


def _embed(text: str) -> list[float]:
    return _embedder.encode(text, normalize_embeddings=True).tolist()


# ── Store ─────────────────────────────────────────────────────────────────────

async def store_memory(content: str, metadata: dict = None) -> str:
    """Store a personal memory or conversation exchange."""
    loop = asyncio.get_event_loop()
    embedding = await loop.run_in_executor(None, lambda: _embed(content))
    mem_id = str(uuid.uuid4())
    _memories.add(
        ids=[mem_id],
        embeddings=[embedding],
        documents=[content],
        metadatas=[{"timestamp": datetime.utcnow().isoformat(), **(metadata or {})}],
    )
    return mem_id


async def store_knowledge_chunks(chunks: list[dict]) -> int:
    """
    Embed and store podcast/verified knowledge chunks.
    Each chunk: { id, text, metadata: { show, episode, pub_date, chunk_index, ... } }
    Returns number of chunks stored.
    """
    loop = asyncio.get_event_loop()
    if not chunks:
        return 0

    texts = [c["text"] for c in chunks]
    embeddings = await loop.run_in_executor(
        None, lambda: _embedder.encode(texts, normalize_embeddings=True).tolist()
    )

    _knowledge.upsert(
        ids=[c["id"] for c in chunks],
        embeddings=embeddings,
        documents=texts,
        metadatas=[{
            "timestamp": datetime.utcnow().isoformat(),
            **c.get("metadata", {}),
        } for c in chunks],
    )
    return len(chunks)


# ── Retrieve ──────────────────────────────────────────────────────────────────

async def retrieve_memories(query: str, n_results: int = 5) -> list[dict]:
    """Query both collections. Returns personal memories + relevant knowledge chunks."""
    loop = asyncio.get_event_loop()
    embedding = await loop.run_in_executor(None, lambda: _embed(query))
    results = []

    # Personal memories — always retrieve up to n_results
    if _memories.count() > 0:
        r = _memories.query(
            query_embeddings=[embedding],
            n_results=min(n_results, _memories.count()),
            include=["documents", "metadatas", "distances"],
        )
        for doc, meta, dist in zip(r["documents"][0], r["metadatas"][0], r["distances"][0]):
            results.append({
                "content": doc,
                "metadata": meta,
                "relevance": round(1 - dist, 3),
                "collection": "memory",
            })

    # Knowledge chunks — only above threshold, max 3
    if _knowledge.count() > 0:
        k = _knowledge.query(
            query_embeddings=[embedding],
            n_results=min(3, _knowledge.count()),
            include=["documents", "metadatas", "distances"],
        )
        for doc, meta, dist in zip(k["documents"][0], k["metadatas"][0], k["distances"][0]):
            relevance = round(1 - dist, 3)
            if relevance >= KNOWLEDGE_THRESHOLD:
                results.append({
                    "content": doc,
                    "metadata": meta,
                    "relevance": relevance,
                    "collection": "knowledge",
                })

    return results


async def maybe_store_exchange(user_msg: str, assistant_msg: str):
    content = f"User: {user_msg}\nCosmos: {assistant_msg}"
    await store_memory(content, {"type": "exchange"})
