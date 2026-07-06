import os
import uuid
import asyncio
from datetime import datetime
from typing import Optional
import chromadb
from chromadb.config import Settings
from sentence_transformers import SentenceTransformer

_client: Optional[chromadb.ClientAPI] = None
_collection = None
_embedder: Optional[SentenceTransformer] = None

CHROMA_PATH = os.getenv("CHROMA_PATH", "./data/chroma")
EMBEDDING_MODEL = os.getenv("EMBEDDING_MODEL", "all-MiniLM-L6-v2")


async def init_memory():
    global _client, _collection, _embedder
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
    _collection = _client.get_or_create_collection(
        name="cosmos_memory",
        metadata={"hnsw:space": "cosine"},
    )
    print(f"[memory] Ready. {_collection.count()} memories loaded.")

    # Share embedder and client with character module
    import character
    await character.init_character(_embedder, _client)


def _embed(text: str) -> list[float]:
    return _embedder.encode(text, normalize_embeddings=True).tolist()


async def store_memory(content: str, metadata: dict = None) -> str:
    loop = asyncio.get_event_loop()
    embedding = await loop.run_in_executor(None, lambda: _embed(content))
    mem_id = str(uuid.uuid4())
    _collection.add(
        ids=[mem_id],
        embeddings=[embedding],
        documents=[content],
        metadatas=[{
            "timestamp": datetime.utcnow().isoformat(),
            **(metadata or {}),
        }],
    )
    return mem_id


async def retrieve_memories(query: str, n_results: int = 5) -> list[dict]:
    if _collection.count() == 0:
        return []
    loop = asyncio.get_event_loop()
    embedding = await loop.run_in_executor(None, lambda: _embed(query))
    results = _collection.query(
        query_embeddings=[embedding],
        n_results=min(n_results, _collection.count()),
        include=["documents", "metadatas", "distances"],
    )
    memories = []
    for doc, meta, dist in zip(
        results["documents"][0],
        results["metadatas"][0],
        results["distances"][0],
    ):
        memories.append({
            "content": doc,
            "metadata": meta,
            "relevance": round(1 - dist, 3),
        })
    return memories


async def maybe_store_exchange(user_msg: str, assistant_msg: str):
    # Store the exchange as a single memory — keeps context together
    content = f"User: {user_msg}\nCosmos: {assistant_msg}"
    await store_memory(content, {"type": "exchange"})
