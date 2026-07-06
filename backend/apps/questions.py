import logging

from fastapi import APIRouter
from pydantic import BaseModel
import questions_db

log = logging.getLogger("cosmos.questions")
router = APIRouter()


class CreateQuestionRequest(BaseModel):
    question: str
    source_text: str = ""
    source_type: str = "chat"
    source_ref: str = ""
    cosmos_thinking: str = ""


class AnswerRequest(BaseModel):
    answer: str


class ResolveRequest(BaseModel):
    store_memory: bool = True


@router.get("/questions")
async def list_questions(status: str = ""):
    questions = questions_db.get_questions(status or None)
    log.info(f"[questions] list status={status!r} → {len(questions)} items")
    return questions


@router.get("/questions/count")
async def question_count():
    return {"pending": questions_db.pending_count()}


@router.post("/questions")
async def create_question(req: CreateQuestionRequest):
    qid = questions_db.create_question(
        question=req.question,
        source_text=req.source_text,
        source_type=req.source_type,
        source_ref=req.source_ref,
        cosmos_thinking=req.cosmos_thinking,
    )
    log.info(f"[questions] created {qid!r}: {req.question[:80]!r}")
    return {"id": qid}


@router.patch("/questions/{qid}/answer")
async def answer_question(qid: str, req: AnswerRequest):
    questions_db.answer_question(qid, req.answer)
    log.info(f"[questions] answered {qid!r}")
    return {"ok": True}


@router.post("/questions/{qid}/resolve")
async def resolve_question(qid: str, req: ResolveRequest):
    q = questions_db.get_question(qid)
    if not q:
        return {"error": "not found"}

    if req.store_memory and q.get("thomas_answer"):
        # Build a clean memory from the resolved exchange
        parts = [f"Q: {q['question']}"]
        if q.get("source_text"):
            parts.append(f"Context: {q['source_text'][:600]}")
        parts.append(f"A: {q['thomas_answer']}")
        memory_content = "\n".join(parts)

        from memory import store_memory
        await store_memory(memory_content, {
            "type": "resolved_question",
            "source_type": q.get("source_type", ""),
            "source_ref": q.get("source_ref", ""),
        })
        log.info(f"[questions] stored resolved memory for {qid!r}")

    questions_db.resolve_question(qid)
    log.info(f"[questions] resolved {qid!r}")
    return {"ok": True}


@router.delete("/questions/{qid}")
async def delete_question(qid: str):
    questions_db.delete_question(qid)
    log.info(f"[questions] deleted {qid!r}")
    return {"ok": True}
