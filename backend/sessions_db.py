import os
import sqlite3
from datetime import datetime
from pathlib import Path

DB_PATH = os.getenv("SESSIONS_DB_PATH", "./data/sessions.db")


def _conn():
    Path(DB_PATH).parent.mkdir(parents=True, exist_ok=True)
    c = sqlite3.connect(DB_PATH)
    c.row_factory = sqlite3.Row
    return c


def init_db():
    with _conn() as c:
        c.execute("""
            CREATE TABLE IF NOT EXISTS sessions (
                id          TEXT PRIMARY KEY,
                title       TEXT NOT NULL DEFAULT 'New conversation',
                created_at  TEXT NOT NULL,
                updated_at  TEXT NOT NULL
            )
        """)
        c.execute("""
            CREATE TABLE IF NOT EXISTS messages (
                id          INTEGER PRIMARY KEY AUTOINCREMENT,
                session_id  TEXT NOT NULL,
                role        TEXT NOT NULL,
                content     TEXT NOT NULL,
                thinking    TEXT,
                created_at  TEXT NOT NULL,
                FOREIGN KEY (session_id) REFERENCES sessions(id)
            )
        """)
        c.commit()


def list_sessions():
    with _conn() as c:
        rows = c.execute("""
            SELECT s.id, s.title, s.created_at, s.updated_at,
                   COUNT(m.id) AS message_count
            FROM sessions s
            LEFT JOIN messages m ON m.session_id = s.id
            GROUP BY s.id
            ORDER BY s.updated_at DESC
        """).fetchall()
        return [dict(r) for r in rows]


def get_messages(session_id: str):
    with _conn() as c:
        rows = c.execute(
            "SELECT role, content, thinking FROM messages WHERE session_id = ? ORDER BY id",
            (session_id,),
        ).fetchall()
        return [dict(r) for r in rows]


def ensure_session(session_id: str, title: str):
    now = datetime.utcnow().isoformat()
    with _conn() as c:
        c.execute(
            "INSERT OR IGNORE INTO sessions (id, title, created_at, updated_at) VALUES (?, ?, ?, ?)",
            (session_id, title[:80], now, now),
        )
        c.commit()


def touch_session(session_id: str):
    with _conn() as c:
        c.execute(
            "UPDATE sessions SET updated_at = ? WHERE id = ?",
            (datetime.utcnow().isoformat(), session_id),
        )
        c.commit()


def append_message(session_id: str, role: str, content: str, thinking: str = None):
    with _conn() as c:
        c.execute(
            "INSERT INTO messages (session_id, role, content, thinking, created_at) VALUES (?, ?, ?, ?, ?)",
            (session_id, role, content, thinking, datetime.utcnow().isoformat()),
        )
        c.commit()


def delete_session(session_id: str):
    with _conn() as c:
        c.execute("DELETE FROM messages WHERE session_id = ?", (session_id,))
        c.execute("DELETE FROM sessions WHERE id = ?", (session_id,))
        c.commit()
