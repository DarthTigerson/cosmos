import uuid
from db import conn as _conn


def init_db():
    with _conn() as c:
        c.execute("""
            CREATE TABLE IF NOT EXISTS questions (
                id TEXT PRIMARY KEY,
                question TEXT NOT NULL,
                source_text TEXT,
                source_type TEXT,
                source_ref TEXT,
                source_episode_id TEXT,
                cosmos_thinking TEXT,
                status TEXT DEFAULT 'pending',
                thomas_answer TEXT,
                created_at TEXT DEFAULT (datetime('now')),
                resolved_at TEXT,
                FOREIGN KEY (source_episode_id) REFERENCES episodes(id) ON DELETE SET NULL
            )
        """)
        c.execute("CREATE INDEX IF NOT EXISTS idx_q_status ON questions(status)")
        # Migrations
        try:
            c.execute("ALTER TABLE questions ADD COLUMN source_episode_id TEXT")
        except Exception:
            pass


def create_question(question, source_text="", source_type="chat",
                    source_ref="", cosmos_thinking="", source_episode_id=None):
    qid = uuid.uuid4().hex[:10]
    with _conn() as c:
        c.execute("""
            INSERT INTO questions
                (id, question, source_text, source_type, source_ref,
                 source_episode_id, cosmos_thinking)
            VALUES (?,?,?,?,?,?,?)
        """, (qid, question, source_text, source_type, source_ref,
              source_episode_id, cosmos_thinking))
    return qid


def get_questions(status=None):
    with _conn() as c:
        if status:
            rows = c.execute(
                "SELECT * FROM questions WHERE status=? ORDER BY created_at DESC", (status,)
            ).fetchall()
        else:
            rows = c.execute(
                "SELECT * FROM questions ORDER BY created_at DESC"
            ).fetchall()
        return [dict(r) for r in rows]


def get_question(qid):
    with _conn() as c:
        row = c.execute("SELECT * FROM questions WHERE id=?", (qid,)).fetchone()
        return dict(row) if row else None


def answer_question(qid, answer):
    with _conn() as c:
        c.execute(
            "UPDATE questions SET thomas_answer=?, status='answered' WHERE id=?",
            (answer, qid),
        )


def resolve_question(qid):
    with _conn() as c:
        c.execute(
            "UPDATE questions SET status='resolved', resolved_at=datetime('now') WHERE id=?",
            (qid,),
        )


def delete_question(qid):
    with _conn() as c:
        c.execute("DELETE FROM questions WHERE id=?", (qid,))


def pending_count():
    with _conn() as c:
        return c.execute(
            "SELECT COUNT(*) FROM questions WHERE status='pending'"
        ).fetchone()[0]


def get_recent_resolutions(days: int = 7):
    """Return questions resolved within the last N days that have an answer."""
    with _conn() as c:
        rows = c.execute(
            """
            SELECT * FROM questions
            WHERE status='resolved'
              AND thomas_answer IS NOT NULL
              AND thomas_answer != ''
              AND resolved_at >= datetime('now', ?)
            ORDER BY resolved_at DESC
            """,
            (f"-{days} days",),
        ).fetchall()
        return [dict(r) for r in rows]
