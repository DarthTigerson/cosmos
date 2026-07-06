import asyncio
import email.utils
import hashlib
import json
import logging
import os
import re
import tempfile
import urllib.parse
import urllib.request
import uuid
import xml.etree.ElementTree as ET
from concurrent.futures import ThreadPoolExecutor

from fastapi import APIRouter
from fastapi.responses import StreamingResponse
from pydantic import BaseModel
import podcasts_db

log = logging.getLogger("cosmos.podcasts")
router = APIRouter()

WHISPER_MODEL = os.getenv("WHISPER_MODEL", "mlx-community/distil-whisper-large-v3")
CHUNK_WORDS = int(os.getenv("CHUNK_WORDS", "800"))
CHUNK_OVERLAP = int(os.getenv("CHUNK_OVERLAP", "80"))
_transcribe_executor = ThreadPoolExecutor(max_workers=1, thread_name_prefix="whisper")

ITUNES_NS = "http://www.itunes.com/dtds/podcast-1.0.dtd"
PODCAST_NS = "https://podcastindex.org/namespace/1.0"
HEADERS = {"User-Agent": "Cosmos/1.0 (local podcast client)"}

_TIMING_RE = re.compile(r'\d+:\d{2}:\d{2}[.,]\d{3}\s*-->\s*\d+:\d{2}:\d{2}[.,]\d{3}')


# ── Helpers ───────────────────────────────────────────────────────────────────

def _ns(tag: str, ns: str) -> str:
    return f"{{{ns}}}{tag}"


def _parse_date(raw: str) -> str:
    """Normalise any RSS/RFC-2822 date string to ISO-8601 so ORDER BY works."""
    if not raw:
        return ""
    try:
        dt = email.utils.parsedate_to_datetime(raw.strip())
        return dt.isoformat()
    except Exception:
        return raw


def _fetch_raw(url: str, timeout: int = 15) -> bytes:
    req = urllib.request.Request(url, headers=HEADERS)
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.read()


def _fetch_xml(url: str) -> ET.Element:
    return ET.fromstring(_fetch_raw(url))


def _parse_feed_meta(rss_url: str) -> dict:
    try:
        root = _fetch_xml(rss_url)
        channel = root.find("channel") or root
        title = (channel.findtext("title") or "").strip()
        author = (
            channel.findtext(_ns("author", ITUNES_NS))
            or channel.findtext("author") or ""
        ).strip()
        description = (channel.findtext("description") or "").strip()[:400]
        artwork_url = ""
        img = channel.find("image")
        if img is not None:
            artwork_url = img.findtext("url") or ""
        if not artwork_url:
            itunes_img = channel.find(_ns("image", ITUNES_NS))
            if itunes_img is not None:
                artwork_url = itunes_img.get("href", "")
        return {"title": title or rss_url, "author": author,
                "artwork_url": artwork_url, "description": description}
    except Exception:
        return {"title": rss_url, "author": "", "artwork_url": "", "description": ""}


def _parse_episodes(rss_url: str, podcast_id: str) -> list[dict]:
    try:
        root = _fetch_xml(rss_url)
    except Exception:
        return []
    channel = root.find("channel") or root
    episodes = []
    for item in channel.findall("item"):
        title = (item.findtext("title") or "Untitled").strip()
        description = (item.findtext("description") or "").strip()[:400]
        pub_date = _parse_date(item.findtext("pubDate") or "")
        guid = (item.findtext("guid") or title).strip()
        duration = (item.findtext(_ns("duration", ITUNES_NS)) or "").strip()
        audio_url = ""
        enc = item.find("enclosure")
        if enc is not None:
            audio_url = enc.get("url", "")
        transcript_url = ""
        transcript_content_type = ""
        for t in item.findall(_ns("transcript", PODCAST_NS)):
            url = t.get("url", "")
            if url:
                transcript_url = url
                transcript_content_type = t.get("type", "")
                break
        ep_id = hashlib.md5(f"{podcast_id}:{guid}".encode()).hexdigest()[:12]
        episodes.append({
            "id": ep_id, "podcast_id": podcast_id, "title": title,
            "description": description, "pub_date": pub_date, "duration": duration,
            "audio_url": audio_url, "guid": guid,
            "transcript_url": transcript_url,
            "transcript_content_type": transcript_content_type,
        })
    return episodes


# ── Transcript parsers ────────────────────────────────────────────────────────

def _strip_cue_lines(lines: list[str]) -> list[str]:
    out = []
    for line in lines:
        line = line.strip()
        if (not line or _TIMING_RE.match(line) or line.isdigit()
                or line.startswith("WEBVTT") or line.startswith("NOTE")
                or line.startswith("STYLE") or line.startswith("REGION")):
            continue
        line = re.sub(r'<[^>]+>', '', line).strip()
        if line:
            out.append(line)
    # Deduplicate consecutive identical lines (overlapping VTT cues)
    deduped: list[str] = []
    for line in out:
        if not deduped or line != deduped[-1]:
            deduped.append(line)
    return deduped


def _parse_vtt(text: str) -> str:
    return '\n'.join(_strip_cue_lines(text.splitlines()))


def _parse_srt(text: str) -> str:
    return '\n'.join(_strip_cue_lines(text.splitlines()))


def _parse_transcript_json(text: str) -> str:
    try:
        data = json.loads(text)
        if isinstance(data, dict) and "segments" in data:
            return " ".join(s.get("text", "") for s in data["segments"]).strip()
        if isinstance(data, list):
            return " ".join(
                item.get("text", item.get("body", ""))
                for item in data if isinstance(item, dict)
            ).strip()
    except Exception:
        pass
    return text.strip()


# ── Routes ────────────────────────────────────────────────────────────────────

@router.get("/podcasts/search")
async def search_podcasts(q: str):
    log.info(f"[search] query={q!r}")
    url = (
        "https://itunes.apple.com/search?"
        + urllib.parse.urlencode({"term": q, "media": "podcast", "entity": "podcast", "limit": "8"})
    )
    try:
        req = urllib.request.Request(url, headers=HEADERS)
        with urllib.request.urlopen(req, timeout=10) as r:
            data = json.loads(r.read())
    except Exception as e:
        log.error(f"[search] iTunes API error: {e}")
        return []
    results = [
        {
            "title": item.get("trackName", ""),
            "author": item.get("artistName", ""),
            "rss_url": item.get("feedUrl", ""),
            "artwork_url": item.get("artworkUrl600") or item.get("artworkUrl100", ""),
        }
        for item in data.get("results", [])
        if item.get("feedUrl")
    ]
    log.info(f"[search] returned {len(results)} results")
    return results


class AddPodcastRequest(BaseModel):
    rss_url: str


@router.get("/podcasts")
async def list_podcasts():
    return podcasts_db.get_podcasts()


@router.post("/podcasts")
async def add_podcast(req: AddPodcastRequest):
    log.info(f"[add] fetching feed meta for {req.rss_url}")
    meta = _parse_feed_meta(req.rss_url)
    pod_id = uuid.uuid4().hex[:8]
    podcasts_db.add_podcast(
        id=pod_id, title=meta["title"], author=meta["author"],
        rss_url=req.rss_url, artwork_url=meta["artwork_url"],
        description=meta["description"],
    )
    log.info(f"[add] subscribed: {meta['title']!r} id={pod_id}")
    return {"id": pod_id, **meta}


@router.delete("/podcasts/{podcast_id}")
async def delete_podcast(podcast_id: str):
    podcasts_db.remove_podcast(podcast_id)
    log.info(f"[delete] removed podcast {podcast_id}")
    return {"deleted": podcast_id}


@router.get("/podcasts/{podcast_id}/episodes")
async def get_episodes(podcast_id: str):
    podcast = podcasts_db.get_podcast(podcast_id)
    if not podcast:
        log.warning(f"[episodes] podcast {podcast_id} not found")
        return []
    log.info(f"[episodes] refreshing feed for {podcast['title']!r}")
    episodes = _parse_episodes(podcast["rss_url"], podcast_id)
    podcasts_db.upsert_episodes(podcast_id, episodes)
    stored = podcasts_db.get_episodes(podcast_id)
    log.info(f"[episodes] {len(stored)} episodes loaded")
    return stored


@router.get("/episodes/{episode_id}")
async def get_episode(episode_id: str):
    ep = podcasts_db.get_episode(episode_id)
    return ep or {}


@router.get("/episodes/{episode_id}/fetch")
async def fetch_transcript(episode_id: str):
    async def stream():
        ep = podcasts_db.get_episode(episode_id)
        if not ep:
            log.error(f"[fetch] episode {episode_id} not found")
            yield _sse(status="error", message="Episode not found", pct=100)
            return

        log.info(f"[fetch] starting for {ep['title']!r}")
        yield _sse(status="checking", pct=5)
        podcasts_db.update_episode_status(episode_id, "fetching")

        transcript_url = ep.get("transcript_url") or ""
        audio_url = ep.get("audio_url") or ""

        # ── Fast path: RSS transcript tag ──────────────────────────────────
        if transcript_url:
            log.info(f"[fetch] RSS transcript → {transcript_url}")
            yield _sse(status="downloading", pct=30)
            try:
                raw = _fetch_raw(transcript_url, timeout=30).decode("utf-8", errors="replace")
            except Exception as e:
                log.error(f"[fetch] transcript download failed: {e}")
                podcasts_db.update_episode_status(episode_id, "error")
                yield _sse(status="error", message=str(e), pct=100)
                return

            yield _sse(status="parsing", pct=80)
            ct = (ep.get("transcript_content_type") or "").lower()
            ul = transcript_url.lower()
            if "vtt" in ct or ul.endswith(".vtt"):
                text = _parse_vtt(raw)
            elif "srt" in ct or ul.endswith(".srt"):
                text = _parse_srt(raw)
            elif "json" in ct or ul.endswith(".json"):
                text = _parse_transcript_json(raw)
            else:
                text = raw.strip()

            podcasts_db.update_episode_status(episode_id, "ready", text)
            log.info(f"[fetch] RSS transcript done — {len(text):,} chars")
            yield _sse(status="done", pct=100, chars=len(text))
            return

        # ── Slow path: mlx-whisper ─────────────────────────────────────────
        if not audio_url:
            log.warning(f"[fetch] no audio URL for {ep['title']!r}")
            podcasts_db.update_episode_status(episode_id, "no_transcript")
            yield _sse(status="no_transcript", pct=100)
            return

        log.info(f"[fetch] no RSS transcript → mlx-whisper ({WHISPER_MODEL.split('/')[-1]})")
        loop = asyncio.get_event_loop()
        q: asyncio.Queue = asyncio.Queue()
        tmp_path = None

        def _download():
            nonlocal tmp_path
            try:
                fd, tmp_path = tempfile.mkstemp(suffix=".mp3")
                os.close(fd)
                req = urllib.request.Request(audio_url, headers=HEADERS)
                with urllib.request.urlopen(req, timeout=60) as r:
                    total = int(r.headers.get("Content-Length", 0) or 0)
                    downloaded = 0
                    with open(tmp_path, "wb") as f:
                        while True:
                            chunk = r.read(65536)
                            if not chunk:
                                break
                            f.write(chunk)
                            downloaded += len(chunk)
                            if total:
                                pct = 5 + int(downloaded / total * 30)
                                loop.call_soon_threadsafe(q.put_nowait, ("pct", pct))
                log.info(f"[fetch] audio downloaded {downloaded/1e6:.1f} MB → {tmp_path}")
                loop.call_soon_threadsafe(q.put_nowait, ("download_done", None))
            except Exception as e:
                loop.call_soon_threadsafe(q.put_nowait, ("error", str(e)))

        def _transcribe():
            try:
                import mlx_whisper
                log.info(f"[fetch] transcribing with {WHISPER_MODEL}")
                result = mlx_whisper.transcribe(
                    tmp_path,
                    path_or_hf_repo=WHISPER_MODEL,
                    verbose=False,
                )
                text = (result.get("text") or "").strip()
                log.info(f"[fetch] transcription done — {len(text):,} chars")
                loop.call_soon_threadsafe(q.put_nowait, ("transcribe_done", text))
            except Exception as e:
                log.error(f"[fetch] transcription error: {e}")
                loop.call_soon_threadsafe(q.put_nowait, ("error", str(e)))

        try:
            loop.run_in_executor(_transcribe_executor, _download)
            transcribing_pct = 40

            while True:
                try:
                    event, data = await asyncio.wait_for(q.get(), timeout=8)
                except asyncio.TimeoutError:
                    # Heartbeat: slowly walk the bar from 40→90 while Whisper runs
                    transcribing_pct = min(transcribing_pct + 1, 90)
                    yield _sse(status="transcribing", pct=transcribing_pct,
                               model=WHISPER_MODEL.split("/")[-1])
                    continue

                if event == "pct":
                    yield _sse(status="downloading", pct=data)

                elif event == "download_done":
                    yield _sse(status="transcribing", pct=40,
                               model=WHISPER_MODEL.split("/")[-1])
                    loop.run_in_executor(_transcribe_executor, _transcribe)

                elif event == "transcribe_done":
                    podcasts_db.update_episode_status(episode_id, "ready", data)
                    yield _sse(status="done", pct=100, chars=len(data))
                    break

                elif event == "error":
                    podcasts_db.update_episode_status(episode_id, "error")
                    yield _sse(status="error", message=data, pct=100)
                    break

        finally:
            if tmp_path and os.path.exists(tmp_path):
                os.unlink(tmp_path)
                log.info(f"[fetch] temp audio deleted")

    return StreamingResponse(
        stream(),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )


def _sse(**kwargs) -> str:
    return f"data: {json.dumps(kwargs)}\n\n"


# ── Chunking ──────────────────────────────────────────────────────────────────

def _chunk_text(text: str, target_words: int = CHUNK_WORDS,
                overlap_words: int = CHUNK_OVERLAP) -> list[str]:
    """Split transcript into overlapping sentence-boundary-respecting chunks."""
    sentences = re.split(r'(?<=[.!?])\s+', text.strip())
    chunks: list[str] = []
    current: list[str] = []
    current_count = 0

    for sentence in sentences:
        words = sentence.split()
        if current_count + len(words) > target_words and current:
            chunks.append(" ".join(current))
            # carry overlap from end of current chunk
            overlap = current[-overlap_words:] if len(current) > overlap_words else current[:]
            current = overlap + words
            current_count = len(current)
        else:
            current.extend(words)
            current_count += len(words)

    if current:
        chunks.append(" ".join(current))

    return [c for c in chunks if c.strip()]


# ── Ingest endpoint ───────────────────────────────────────────────────────────

@router.get("/episodes/{episode_id}/ingest")
async def ingest_episode(episode_id: str):
    async def stream():
        ep = podcasts_db.get_episode(episode_id)
        if not ep:
            yield _sse(status="error", message="Episode not found", pct=100)
            return

        transcript = (ep.get("transcript_text") or "").strip()
        if not transcript:
            yield _sse(status="error", message="No transcript — fetch first", pct=100)
            return

        podcast = podcasts_db.get_podcast(ep["podcast_id"])
        show_name = podcast["title"] if podcast else "Unknown Show"
        episode_title = ep["title"]
        pub_date = (ep.get("pub_date") or "")[:10]

        log.info(f"[ingest] chunking {episode_title!r}")
        yield _sse(status="chunking", pct=5)

        chunks = _chunk_text(transcript)
        total = len(chunks)
        log.info(f"[ingest] {total} chunks from {len(transcript):,} chars")
        yield _sse(status="chunking", pct=10, chunks=total)

        from memory import store_knowledge_chunks

        batch_size = 8
        embedded = 0
        knowledge_chunks = []

        for i, chunk_text in enumerate(chunks):
            chunk_id = f"ep:{episode_id}:c{i}"
            knowledge_chunks.append({
                "id": chunk_id,
                "text": chunk_text,
                "metadata": {
                    "type": "podcast_chunk",
                    "show": show_name,
                    "episode": episode_title,
                    "pub_date": pub_date,
                    "episode_id": episode_id,
                    "podcast_id": ep["podcast_id"],
                    "chunk_index": i,
                    "total_chunks": total,
                },
            })

            if len(knowledge_chunks) >= batch_size or i == total - 1:
                await store_knowledge_chunks(knowledge_chunks)
                embedded += len(knowledge_chunks)
                knowledge_chunks = []
                pct = 10 + int((embedded / total) * 88)
                log.info(f"[ingest] embedded {embedded}/{total}")
                yield _sse(status="embedding", pct=pct, chunk=embedded, total=total)

        podcasts_db.update_episode_ingested(episode_id, total)
        log.info(f"[ingest] done — {total} chunks in cosmos_knowledge")
        yield _sse(status="done", pct=100, chunks=total)

    return StreamingResponse(
        stream(),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )
