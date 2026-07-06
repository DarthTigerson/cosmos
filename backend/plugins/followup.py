from .base import Plugin
import follow_ups_db


def _tool(name, description, properties, required):
    return {
        "type": "function",
        "function": {
            "name": name,
            "description": description,
            "parameters": {"type": "object", "properties": properties, "required": required},
        },
    }


class FollowUpPlugin(Plugin):
    name = "followup"

    tools = [
        _tool(
            "note_followup",
            "Create a follow-up reminder for yourself — for when Thomas says he'll try something, "
            "get back to you, or when you want to check in on something next session. "
            "The follow-up will be surfaced at the start of future sessions until closed. "
            "Returns an ID to use with close_followup when it's been addressed.",
            {
                "topic": {"type": "string", "description": "What to follow up on"},
                "context": {"type": "string", "description": "Why you're noting this — what was said"},
            },
            ["topic"],
        ),
        _tool(
            "close_followup",
            "Mark a follow-up as resolved — when you've checked in and it's been addressed.",
            {
                "id": {"type": "string", "description": "The follow-up ID returned by note_followup"},
            },
            ["id"],
        ),
    ]

    async def execute(self, tool_name: str, args: dict, context: dict = None) -> str:
        if tool_name == "note_followup":
            topic = args.get("topic", "").strip()
            if not topic:
                return "Topic is required."
            ctx = args.get("context", "")
            session_id = (context or {}).get("session_id", "")
            fid = follow_ups_db.create_followup(topic, ctx, session_id)
            return f"Follow-up noted (id: {fid}). I'll surface this at the start of future sessions."
        if tool_name == "close_followup":
            fid = args.get("id", "").strip()
            if not fid:
                return "Follow-up ID required."
            follow_ups_db.close_followup(fid)
            return f"Follow-up {fid} closed."
        return f"Unknown tool: {tool_name}"
