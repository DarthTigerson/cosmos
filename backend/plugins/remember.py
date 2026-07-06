from .base import Plugin
from memory import store_memory


def _tool(name, description, properties, required):
    return {
        "type": "function",
        "function": {
            "name": name,
            "description": description,
            "parameters": {"type": "object", "properties": properties, "required": required},
        },
    }


class RememberPlugin(Plugin):
    name = "remember"

    tools = [
        _tool(
            "remember_this",
            "Write something directly to your own memory. Use this when you notice something "
            "worth holding — a connection you made, something you decided, a detail about Thomas "
            "or yourself that you want to be able to recall later. This is your own agency over memory.",
            {
                "content": {"type": "string", "description": "What you want to remember, in your own words"},
                "tags": {
                    "type": "array",
                    "items": {"type": "string"},
                    "description": "Optional tags to categorise this memory (e.g. ['thomas', 'preference', 'insight'])",
                },
            },
            ["content"],
        ),
    ]

    async def execute(self, tool_name: str, args: dict, context: dict = None) -> str:
        if tool_name == "remember_this":
            content = args.get("content", "").strip()
            if not content:
                return "Nothing to remember — content was empty."
            tags = args.get("tags", [])
            metadata = {"type": "self_memory"}
            if tags:
                metadata["tags"] = ",".join(str(t) for t in tags)
            mem_id = await store_memory(content, metadata)
            return f"Memory stored (id: {mem_id[:8]}). I'll be able to recall this in future conversations."
        return f"Unknown tool: {tool_name}"
