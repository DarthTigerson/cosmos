from .base import Plugin
import questions_db


def _tool(name, description, properties, required):
    return {
        "type": "function",
        "function": {
            "name": name,
            "description": description,
            "parameters": {"type": "object", "properties": properties, "required": required},
        },
    }


class AskThomasPlugin(Plugin):
    name = "ask_thomas"

    tools = [
        _tool(
            "create_question",
            "Park a question for Thomas when you are uncertain about something. "
            "Include the source context that caused the uncertainty and your current thinking. "
            "Thomas will answer it and you can update your knowledge.",
            {
                "question": {"type": "string", "description": "The question you want to ask Thomas"},
                "context": {"type": "string", "description": "The source text or clip that raised this question"},
                "thinking": {"type": "string", "description": "Your current hypothesis or reasoning"},
                "source_ref": {"type": "string", "description": "Where this came from, e.g. episode title or topic"},
            },
            ["question", "context", "thinking"],
        ),
    ]

    async def execute(self, tool_name: str, args: dict) -> str:
        if tool_name == "create_question":
            qid = questions_db.create_question(
                question=args.get("question", ""),
                source_text=args.get("context", ""),
                source_type="chat",
                source_ref=args.get("source_ref", ""),
                cosmos_thinking=args.get("thinking", ""),
            )
            return f"Question saved (id: {qid}). Thomas will see it in the Questions app and answer when he can."
        return f"Unknown tool: {tool_name}"
