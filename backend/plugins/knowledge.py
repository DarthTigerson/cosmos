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
            "List the sources you've ingested (shows, episodes, dates, chunk counts). Use only "
            "when you need an inventory of what exists — NOT to answer questions about content. "
            "For any question about what you know or heard, use search_knowledge instead.",
            {},
            [],
        ),
        _tool(
            "search_knowledge",
            "Search your ingested knowledge (podcasts, transcripts, etc.) by topic and retrieve "
            "the most relevant excerpts. Use this as your default whenever you want to recall "
            "something you've heard or answer a question from your ingested content. "
            "Prefer multiple focused queries over one broad one for better recall.",
            {
                "query": {"type": "string", "description": "Topic or question to search for in your ingested content"},
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
                line = f"• {label}  [{s['chunks']} chunks]"
                if s.get("description"):
                    line += f"\n  {s['description']}"
                lines.append(line)
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
