"""Single shared SQLite database for all Cosmos data."""
import logging
import os
import sqlite3
from pathlib import Path

log = logging.getLogger("cosmos.db")

DB_PATH = os.getenv("COSMOS_DB_PATH", str(Path(__file__).parent / "data" / "cosmos.db"))


def conn():
    Path(DB_PATH).parent.mkdir(parents=True, exist_ok=True)
    c = sqlite3.connect(DB_PATH)
    c.row_factory = sqlite3.Row
    c.execute("PRAGMA foreign_keys = ON")
    return c


def migrate_legacy():
    """Move data from old separate DB files into the unified cosmos.db."""
    data_dir = Path(DB_PATH).parent
    parent_dir = Path(__file__).parent

    legacy = {
        "podcasts": parent_dir / "podcasts.db",
        "questions": parent_dir / "questions.db",
    }

    for name, path in legacy.items():
        if not path.exists():
            continue
        log.info(f"[db] migrating legacy {path.name} → cosmos.db")
        try:
            src = sqlite3.connect(str(path))
            src.row_factory = sqlite3.Row
            dst = sqlite3.connect(DB_PATH)

            src.backup(dst)   # copies all tables that don't already exist
            src.close()
            dst.close()

            path.rename(path.with_suffix(".db.migrated"))
            log.info(f"[db] migration done, renamed to {path.name}.migrated")
        except Exception as e:
            log.error(f"[db] migration failed for {path.name}: {e}")
