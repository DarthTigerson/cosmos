"""Global token usage stats, updated after every generation."""
import time

_stats = {
    "prompt_tokens": 0,
    "completion_tokens": 0,
    "context_limit": 0,
    "tps": 0.0,
    "generating": False,
    "disk_mb": 0,
    "disk_budget_mb": 0,
}

# Timing state for live TPS
_gen_start: float = 0.0
_first_token: float = 0.0
_token_count: int = 0


def generation_start():
    global _gen_start, _first_token, _token_count
    _gen_start = time.monotonic()
    _first_token = 0.0
    _token_count = 0
    _stats["generating"] = True
    _stats["tps"] = 0.0


def token_produced():
    global _first_token, _token_count
    now = time.monotonic()
    if _first_token == 0.0:
        _first_token = now
    _token_count += 1
    elapsed = now - _first_token
    if elapsed > 0:
        _stats["tps"] = round(_token_count / elapsed, 1)


def generation_done(prompt_tokens: int, completion_tokens: int):
    _stats["prompt_tokens"] = prompt_tokens
    _stats["completion_tokens"] = completion_tokens
    _stats["generating"] = False


def set_context_limit(limit: int):
    _stats["context_limit"] = limit


def set_disk_cache(used_mb: int, budget_mb: int):
    _stats["disk_mb"] = used_mb
    _stats["disk_budget_mb"] = budget_mb


def get() -> dict:
    return dict(_stats)
