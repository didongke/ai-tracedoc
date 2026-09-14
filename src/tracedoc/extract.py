"""Transcript parsing and Q&A extraction (core layer, agent-agnostic —
no Claude Code-specific concepts beyond the transcript record shape).

Extraction rules follow design doc §4.3: human questions are recorded
verbatim; the answer is the text blocks of the last assistant record in
the question's chain. Conversation CONTENT is kept exactly as written,
in whatever language it was written.
"""
import datetime
import json


def iter_records_from(path, start_offset):
    """Read the JSONL records of path after start_offset.

    Returns (new_offset, records, ends). ends[i] is the byte offset after
    the line holding records[i], so callers can advance the offset by
    "consumed record count". new_offset advances only past successfully
    parsed lines; an unterminated trailing line that fails to parse is
    not advanced (it will be re-read once completed). Corrupt complete
    lines are skipped and advanced, so they never wedge the pipeline.
    """
    records = []
    ends = []
    offset = start_offset
    with open(path, "rb") as fh:
        fh.seek(start_offset)
        for raw in fh:
            try:
                records.append(json.loads(raw.decode("utf-8")))
                offset += len(raw)
                ends.append(offset)
            except (ValueError, UnicodeDecodeError):
                if raw.endswith(b"\n"):
                    offset += len(raw)   # corrupt complete line: skip it
                    continue
                break                     # trailing partial line: leave it
    return offset, records, ends


def _is_sidechain(record):
    return bool(record.get("isSidechain")) or "agentId" in record


def is_human_question(record):
    """True when the record is a typed human message (design doc §4.3
    predicate)."""
    if record.get("type") != "user":
        return False
    if _is_sidechain(record):
        return False
    if record.get("origin", {}).get("kind") != "human":
        return False
    message = record.get("message") or {}
    return isinstance(message.get("content"), str)


def _text_of(assistant_record):
    blocks = (assistant_record.get("message") or {}).get("content") or []
    texts = []
    for block in blocks:
        if isinstance(block, dict) and block.get("type") == "text":
            text = (block.get("text") or "").strip()
            if text:
                texts.append(text)
    return "\n\n".join(texts) or None


def _fallback_title(entries):
    first = (entries[0]["q"] or "").strip().replace("\n", " ")
    if len(first) > 40:
        return first[:40] + "…"
    return first


def _today():
    return datetime.date.today().strftime("%Y-%m-%d")


def _format_local_time(timestamp, tz=None):
    """Convert a transcript UTC timestamp to local time, formatted
    %Y-%m-%d %H:%M.

    tz is injectable for tests; defaults to the system's local timezone.
    Returns "" for missing or unparseable timestamps.
    """
    if not timestamp:
        return ""
    try:
        parsed = datetime.datetime.fromisoformat(
            str(timestamp).replace("Z", "+00:00"))
    except ValueError:
        return ""
    if tz is not None:
        parsed = parsed.astimezone(tz)
    else:
        parsed = parsed.astimezone()
    return parsed.strftime("%Y-%m-%d %H:%M")


def extract_sessions(records, tz=None, complete_only=True):
    """Group a record stream into per-session Q&A.

    Returns (sessions, consumed_count).
    - sessions: [{"session_id","title","date","entries":[{"q","a","t"}]}]
      in order of appearance; a is None when the question got no text
      answer; t is the question's local time (%Y-%m-%d %H:%M), "" when
      the timestamp is missing.
    - consumed_count: number of records fully consumed — callers advance
      the offset by exactly this many records.

    Chain-completeness rule: a chain closed by the next human question is
    recorded immediately; the trailing chain is complete only when its
    last assistant record carries text. With complete_only=True (Stop
    mode) an incomplete trailing chain is neither extracted nor consumed
    — it waits for the next trigger. With complete_only=False (SessionEnd
    mode) it is recorded question-only and fully consumed.
    """
    session_map = {}
    titles = {}
    dates = {}
    chains = []              # closed chains: {"sid","q","a","t"}
    pending = None           # current chain: {"sid","q","a","t"}
    last_assistant = None    # last assistant record of the current chain
    consumed = 0

    def close_chain():
        if pending is None:
            return
        if last_assistant is not None:
            pending["a"] = _text_of(last_assistant)
        chains.append(pending)
        bucket = session_map.setdefault(
            pending["sid"],
            {"session_id": pending["sid"], "title": "", "date": "",
             "entries": []})
        bucket["entries"].append(
            {"q": pending["q"], "a": pending["a"], "t": pending["t"]})

    for index, record in enumerate(records):
        kind = record.get("type")
        sid = record.get("sessionId")
        if kind == "ai-title":
            title = (record.get("aiTitle") or "").strip()
            if title and sid:
                titles[sid] = title
        elif (kind == "assistant" and not _is_sidechain(record)
              and pending is not None and sid == pending["sid"]):
            last_assistant = record
        elif is_human_question(record):
            if pending is not None:      # new question closes the previous chain
                close_chain()
                consumed = index
            if sid:
                dates.setdefault(sid, (record.get("timestamp") or "")[:10])
            pending = {"sid": sid,
                       "q": record["message"]["content"], "a": None,
                       "t": _format_local_time(record.get("timestamp"), tz)}
            last_assistant = None

    if pending is not None:              # trailing chain
        last_has_text = (last_assistant is not None
                         and _text_of(last_assistant) is not None)
        if last_has_text or not complete_only:
            close_chain()
            consumed = len(records)

    sessions = []
    for sid in session_map:
        session = session_map[sid]
        session["title"] = titles.get(sid) or _fallback_title(session["entries"])
        first_t = session["entries"][0]["t"]
        session["date"] = first_t[:10] or dates.get(sid) or _today()
        sessions.append(session)
    return sessions, consumed
