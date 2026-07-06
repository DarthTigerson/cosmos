from pydantic import BaseModel
from typing import Optional


class Message(BaseModel):
    role: str  # "user" | "assistant"
    content: str
    thinking: Optional[str] = None


class ChatRequest(BaseModel):
    message: str
    session_id: str = "default"


class ChatResponse(BaseModel):
    message: str
    thinking: Optional[str] = None
    session_id: str


class MemoryEntry(BaseModel):
    id: str
    content: str
    timestamp: str
    relevance: Optional[float] = None
