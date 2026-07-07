import logging
import os
from contextlib import asynccontextmanager
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from dotenv import load_dotenv

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(name)s] %(levelname)s %(message)s",
    datefmt="%H:%M:%S",
)

load_dotenv(dotenv_path=os.path.join(os.path.dirname(__file__), "../.env"))

from db import migrate_legacy
from chat import router as chat_router
from apps.podcasts import router as podcasts_router
from apps.questions import router as questions_router
from memory import init_memory
from sessions_db import init_db
import podcasts_db
import questions_db
import follow_ups_db
import consolidation
from system_info import get_battery, get_network, get_memory, _load_readings


@asynccontextmanager
async def lifespan(app: FastAPI):
    migrate_legacy()       # move podcasts.db / questions.db → cosmos.db if they exist
    init_db()
    podcasts_db.init_db()
    questions_db.init_db()
    follow_ups_db.init_db()
    _load_readings()
    await init_memory()
    import asyncio
    asyncio.get_event_loop().create_task(consolidation.consolidation_loop())
    yield


app = FastAPI(title="Cosmos", lifespan=lifespan)

app.add_middleware(
    CORSMiddleware,
    allow_origins=["http://localhost:5173", "http://127.0.0.1:5173"],
    allow_methods=["*"],
    allow_headers=["*"],
)

app.include_router(chat_router)
app.include_router(podcasts_router)
app.include_router(questions_router)


@app.get("/health")
async def health():
    return {"status": "ok", "name": "Cosmos"}


@app.get("/battery")
async def battery():
    return get_battery() or {"level": None, "charging": None, "discharging": None, "time_remaining": None}


@app.get("/network")
async def network():
    return get_network()


@app.get("/memory")
async def memory():
    return get_memory()


@app.get("/model/status")
async def model_status():
    import httpx
    try:
        async with httpx.AsyncClient() as client:
            r = await client.get("http://localhost:8080/health", timeout=1.5)
            data = r.json()
            backends = data.get("backends", [])
            available = any(b.get("status") == "running" for b in backends)
            return {"available": available}
    except Exception:
        return {"available": False}
