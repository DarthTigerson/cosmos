import uuid
from db import conn as _conn


def init_db():
    with _conn() as c:
        c.execute("""
            CREATE TABLE IF NOT EXISTS follow_ups (
                id TEXT PRIMARY KEY,
                content TEXT NOT NULL,
                context TEXT DEFAULT '',
                session_id TEXT DEFAULT '',
                resolved INTEGER DEFAULT 0,
                created_at TEXT DEFAULT (datetime('now')),
                resolved_at TEXT
            )
        """)
        c.execute("CREATE INDEX IF NOT EXISTS idx_fu_resolved ON follow_ups(resolved)")


def create_followup(content: str, context: str = "", session_id: str = "") -> str:
    fid = uuid.uuid4().hex[:10]
    with _conn() as c:
        c.execute(
            "INSERT INTO follow_ups (id, content, context, session_id) VALUES (?,?,?,?)",
            (fid, content, context, session_id),
        )
    return fid


def get_open() -> list[dict]:
    with _conn() as c:
        rows = c.execute(
            "SELECT * FROM follow_ups WHERE resolved=0 ORDER BY created_at ASC"
        ).fetchall()
        return [dict(r) for r in rows]


def close_followup(fid: str):
    with _conn() as c:
        c.execute(
            "UPDATE follow_ups SET resolved=1, resolved_at=datetime('now') WHERE id=?",
            (fid,),
        )
