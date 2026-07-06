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


class CompressPlugin(Plugin):
    name = "compress"

    tools = [
        _tool(
            "compress_context",
            "Summarise the current conversation and compress it. Call this when you feel context "
            "pressure — when the conversation has grown long and you want to preserve what matters "
            "without carrying every message forward. Write your own summary of what's been covered, "
            "what was decided, and what's important to remember. The raw history will be truncated "
            "and your summary stored to memory so you can recall it in future sessions.",
            {
                "summary": {
                    "type": "string",
                    "description": "Your summary of the conversation — what was discussed, what matters, what was decided.",
                },
            },
            ["summary"],
        ),
    ]

    async def execute(self, tool_name: str, args: dict, context: dict = None) -> str:
        if tool_name == "compress_context":
            summary = args.get("summary", "").strip()
            if not summary:
                return "Please provide a summary of the conversation."

            mem_id = await store_memory(
                f"Session summary: {summary}",
                {"type": "session_summary"},
            )

            # Truncate the in-memory session history so the next turn starts lighter
            session_id = context and context.get("session_id")
            sessions = context and context.get("sessions")
            if sessions is not None and session_id and session_id in sessions:
                kept = sessions[session_id][-2:]
                sessions[session_id] = kept

            return (
                f"Context compressed. Summary stored to memory (id: {mem_id[:8]}). "
                "History truncated to last 2 messages. The summary will be available in future sessions."
            )
        return f"Unknown tool: {tool_name}"
