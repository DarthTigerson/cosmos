import sqlite3
from db import conn as _conn


def init_db():
    with _conn() as c:
        c.execute("""
            CREATE TABLE IF NOT EXISTS podcasts (
                id TEXT PRIMARY KEY,
                title TEXT NOT NULL,
                author TEXT,
                rss_url TEXT NOT NULL UNIQUE,
                artwork_url TEXT,
                description TEXT,
                added_at TEXT DEFAULT (datetime('now'))
            )
        """)
        c.execute("""
            CREATE TABLE IF NOT EXISTS episodes (
                id TEXT PRIMARY KEY,
                podcast_id TEXT NOT NULL,
                title TEXT NOT NULL,
                description TEXT,
                pub_date TEXT,
                duration TEXT,
                audio_url TEXT,
                guid TEXT,
                transcript_url TEXT,
                transcript_content_type TEXT,
                transcript_status TEXT DEFAULT 'none',
                transcript_text TEXT,
                chunks_count INTEGER DEFAULT 0,
                ingested_at TEXT,
                FOREIGN KEY (podcast_id) REFERENCES podcasts(id) ON DELETE CASCADE
            )
        """)
        c.execute("CREATE INDEX IF NOT EXISTS idx_ep_podcast ON episodes(podcast_id)")
        c.execute("CREATE UNIQUE INDEX IF NOT EXISTS idx_ep_guid ON episodes(guid)")
        # Migrations for existing tables
        for col, definition in [
            ("chunks_count", "INTEGER DEFAULT 0"),
            ("transcript_content_type", "TEXT"),
        ]:
            try:
                c.execute(f"ALTER TABLE episodes ADD COLUMN {col} {definition}")
            except Exception:
                pass


def add_podcast(id, title, author, rss_url, artwork_url, description):
    with _conn() as c:
        c.execute(
            "INSERT OR IGNORE INTO podcasts (id,title,author,rss_url,artwork_url,description) VALUES (?,?,?,?,?,?)",
            (id, title, author, rss_url, artwork_url, description),
        )


def get_podcasts():
    with _conn() as c:
        return [dict(r) for r in c.execute("SELECT * FROM podcasts ORDER BY added_at DESC")]


def get_podcast(podcast_id):
    with _conn() as c:
        row = c.execute("SELECT * FROM podcasts WHERE id=?", (podcast_id,)).fetchone()
        return dict(row) if row else None


def remove_podcast(podcast_id):
    with _conn() as c:
        c.execute("DELETE FROM podcasts WHERE id=?", (podcast_id,))


def upsert_episodes(podcast_id, episodes: list[dict]):
    with _conn() as c:
        for ep in episodes:
            c.execute("""
                INSERT INTO episodes
                    (id, podcast_id, title, description, pub_date, duration,
                     audio_url, guid, transcript_url, transcript_content_type)
                VALUES (?,?,?,?,?,?,?,?,?,?)
                ON CONFLICT(guid) DO UPDATE SET
                    title=excluded.title,
                    pub_date=excluded.pub_date,
                    duration=excluded.duration,
                    transcript_url=excluded.transcript_url,
                    transcript_content_type=excluded.transcript_content_type
            """, (
                ep["id"], podcast_id, ep["title"], ep.get("description", ""),
                ep.get("pub_date", ""), ep.get("duration", ""),
                ep.get("audio_url", ""), ep.get("guid", ""),
                ep.get("transcript_url", ""), ep.get("transcript_content_type", ""),
            ))


def get_episodes(podcast_id):
    with _conn() as c:
        return [dict(r) for r in c.execute(
            "SELECT * FROM episodes WHERE podcast_id=? ORDER BY pub_date DESC",
            (podcast_id,),
        )]


def get_episode(episode_id):
    with _conn() as c:
        row = c.execute("SELECT * FROM episodes WHERE id=?", (episode_id,)).fetchone()
        return dict(row) if row else None


def update_episode_status(episode_id, status, transcript_text=None):
    with _conn() as c:
        if transcript_text is not None:
            c.execute(
                "UPDATE episodes SET transcript_status=?, transcript_text=?, ingested_at=datetime('now') WHERE id=?",
                (status, transcript_text, episode_id),
            )
        else:
            c.execute(
                "UPDATE episodes SET transcript_status=? WHERE id=?",
                (status, episode_id),
            )


def update_episode_ingested(episode_id, chunks_count: int):
    with _conn() as c:
        c.execute(
            "UPDATE episodes SET transcript_status='ingested', chunks_count=?, ingested_at=datetime('now') WHERE id=?",
            (chunks_count, episode_id),
        )
