import asyncio
import json
import re
import urllib.request
import urllib.parse
from .base import Plugin

HEADERS = {"User-Agent": "Cosmos/1.0 (local AI companion; contact: local)"}
MAX_EXTRACT = 3000


def _tool(name, description, properties, required):
    return {
        "type": "function",
        "function": {
            "name": name,
            "description": description,
            "parameters": {"type": "object", "properties": properties, "required": required},
        },
    }


class WikiSearchPlugin(Plugin):
    name = "wikisearch"

    tools = [
        _tool("wiki_search", "Search Wikipedia. Returns top 5 article titles and snippets.",
              {"query": {"type": "string"}}, ["query"]),
        _tool("wiki_fetch", "Fetch a Wikipedia article's introduction by exact title.",
              {"title": {"type": "string"}}, ["title"]),
    ]

    async def execute(self, tool_name: str, args: dict, context: dict = None) -> str:
        loop = asyncio.get_event_loop()
        try:
            if tool_name == "wiki_search":
                return await loop.run_in_executor(None, self._search, args.get("query", ""))
            if tool_name == "wiki_fetch":
                return await loop.run_in_executor(None, self._fetch, args.get("title", ""))
            return f"Unknown tool: {tool_name}"
        except Exception as e:
            return f"WikiSearch error: {e}"

    def _search(self, query: str) -> str:
        q = urllib.parse.quote_plus(query)
        url = f"https://en.wikipedia.org/w/api.php?action=query&list=search&srsearch={q}&format=json&srlimit=5"
        req = urllib.request.Request(url, headers=HEADERS)
        with urllib.request.urlopen(req, timeout=8) as r:
            data = json.loads(r.read())

        results = data.get("query", {}).get("search", [])
        if not results:
            return "No Wikipedia articles found for that query."

        tag_re = re.compile(r"<[^>]+>")
        lines = []
        for item in results:
            title = item["title"]
            snippet = tag_re.sub("", item.get("snippet", "")).strip()
            lines.append(f"• {title}\n  {snippet}")

        return "\n\n".join(lines)

    def _fetch(self, title: str) -> str:
        encoded = urllib.parse.quote(title.replace(" ", "_"))
        url = f"https://en.wikipedia.org/api/rest_v1/page/summary/{encoded}"
        req = urllib.request.Request(url, headers=HEADERS)
        with urllib.request.urlopen(req, timeout=8) as r:
            data = json.loads(r.read())

        parts = []
        if data.get("description"):
            parts.append(f"[{data['description']}]")
        extract = data.get("extract", "").strip()
        if extract:
            parts.append(extract[:MAX_EXTRACT])
            if len(extract) > MAX_EXTRACT:
                parts.append(f"… (truncated, {len(extract)} chars total)")

        return "\n\n".join(parts) if parts else "No content found for that article."
