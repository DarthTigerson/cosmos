from .base import Plugin
from .superpowers import SuperpowersPlugin
from .wikisearch import WikiSearchPlugin
from .ask_thomas import AskThomasPlugin
from .remember import RememberPlugin
from .knowledge import KnowledgePlugin
from .compress import CompressPlugin
from .followup import FollowUpPlugin

REGISTRY: dict[str, Plugin] = {
    "superpowers": SuperpowersPlugin(),
    "wikisearch": WikiSearchPlugin(),
    "ask_thomas": AskThomasPlugin(),
    "remember": RememberPlugin(),
    "knowledge": KnowledgePlugin(),
    "compress": CompressPlugin(),
    "followup": FollowUpPlugin(),
}


def get_tools_for_plugins(plugin_ids: list[str]) -> list[dict]:
    tools = []
    for pid in plugin_ids:
        if pid in REGISTRY:
            tools.extend(REGISTRY[pid].tools)
    return tools


async def execute_tool(plugin_ids: list[str], tool_name: str, args: dict, context: dict = None) -> str:
    for pid in plugin_ids:
        plugin = REGISTRY.get(pid)
        if plugin is None:
            continue
        for t in plugin.tools:
            if t["function"]["name"] == tool_name:
                return await plugin.execute(tool_name, args, context)
    return f"No plugin handles tool: {tool_name}"
