from abc import ABC, abstractmethod


class Plugin(ABC):
    name: str
    tools: list[dict]  # OpenAI-format tool schemas

    @abstractmethod
    async def execute(self, tool_name: str, args: dict) -> str:
        ...
