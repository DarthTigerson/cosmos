from .base import Plugin
from memory import list_knowledge_sources, search_knowledge_chunks


def _tool(name, description, properties, required):
    return {
        "type": "function",
        "function": {
            "name": name,
            "description": description,
            "parameters": {"type": "object", "properties": properties, "required": required},
        },
    }


class KnowledgePlugin(Plugin):
    name = "knowledge"

    tools = [
        _tool(
            "list_knowledge",
            "List everything you've ingested — podcasts, shows, episodes. Use this to know what "
            "you've heard and can draw on. Returns each source with chunk count and date.",
            {},
            [],
        ),
        _tool(
            "search_knowledge",
            "Search your ingested knowledge for a specific topic. Returns relevant excerpts from "
            "podcasts and other ingested content, with source labels.",
            {
                "query": {"type": "string", "description": "What to search for in your ingested content"},
            },
            ["query"],
        ),
    ]

    async def execute(self, tool_name: str, args: dict, context: dict = None) -> str:
        if tool_name == "list_knowledge":
            sources = await list_knowledge_sources()
            if not sources:
                return "No knowledge ingested yet."
            lines = []
            for s in sources:
                label = s["show"]
                if s["episode"]:
                    label += f" — {s['episode']}"
                if s["pub_date"]:
                    label += f" ({s['pub_date']})"
                lines.append(f"• {label}  [{s['chunks']} chunks]")
            return f"Ingested knowledge ({len(sources)} sources):\n" + "\n".join(lines)

        if tool_name == "search_knowledge":
            query = args.get("query", "").strip()
            if not query:
                return "Please provide a search query."
            results = await search_knowledge_chunks(query)
            if not results:
                return f"No knowledge found for: {query!r}"
            lines = []
            for r in results:
                meta = r.get("metadata", {})
                source = meta.get("show", "Podcast")
                episode = meta.get("episode", "")
                pub = meta.get("pub_date", "")[:10]
                label = source
                if episode:
                    label += f" — {episode}"
                if pub:
                    label += f" ({pub})"
                snippet = r["content"][:500].rsplit(" ", 1)[0] + "…"
                lines.append(f"[{label}]\n{snippet}")
            return "\n\n".join(lines)

        return f"Unknown tool: {tool_name}"
