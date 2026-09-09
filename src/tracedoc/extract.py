"""转录解析与问答提取（核心层，跨 agent 通用，无 Claude Code 特定概念）。

提取规则见设计文档 §4.3：人类提问原文照录；答取该提问之后
最后一条 assistant 记录的 text 块；无 text（以工具调用收尾）则答留空。
"""
import datetime
import json


def iter_records_from(path, start_offset):
    """读取 path 中 start_offset 之后的 JSONL 记录。

    返回 (new_offset, records)。new_offset 只推进到成功解析的记录之后；
    末尾无换行且解析失败的残行不推进，待下次写入补全后再解析。
    中间损坏的完整行跳过并推进，避免永久卡住。
    """
    records = []
    offset = start_offset
    with open(path, "rb") as fh:
        fh.seek(start_offset)
        for raw in fh:
            try:
                records.append(json.loads(raw.decode("utf-8")))
                offset += len(raw)
            except (ValueError, UnicodeDecodeError):
                if raw.endswith(b"\n"):
                    offset += len(raw)   # 完整的坏行：跳过并推进
                    continue
                break                     # 末尾残行：不推进
    return offset, records


def _is_sidechain(record):
    return bool(record.get("isSidechain")) or "agentId" in record


def is_human_question(record):
    """判定记录是否为人类输入的文本提问（设计文档 §4.3 判定条件）。"""
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


def extract_sessions(records):
    """把记录流按 sessionId 分组为会话问答。

    返回 [{"session_id","title","date","entries":[{"q","a"}]}]，按出现顺序。
    a 为 None 表示该提问没有文字总结。
    """
    session_map = {}
    titles = {}
    dates = {}
    pending = None          # {"session_id","q","a"}
    last_assistant = None   # 当前提问之后出现的最后一条 assistant 记录

    def flush():
        if pending is None:
            return
        if last_assistant is not None:
            pending["a"] = _text_of(last_assistant)
        sid = pending["session_id"]
        bucket = session_map.setdefault(
            sid, {"session_id": sid, "title": "", "date": "", "entries": []})
        bucket["entries"].append({"q": pending["q"], "a": pending["a"]})

    for record in records:
        kind = record.get("type")
        sid = record.get("sessionId")
        if kind == "ai-title":
            title = (record.get("aiTitle") or "").strip()
            if title and sid:
                titles[sid] = title
        elif (kind == "assistant" and not _is_sidechain(record)
              and pending is not None and sid == pending["session_id"]):
            last_assistant = record
        elif is_human_question(record):
            flush()
            if sid:
                dates.setdefault(sid, (record.get("timestamp") or "")[:10])
            pending = {"session_id": sid,
                       "q": record["message"]["content"], "a": None}
            last_assistant = None
    flush()

    sessions = []
    for sid in session_map:
        session = session_map[sid]
        session["title"] = titles.get(sid) or _fallback_title(session["entries"])
        session["date"] = dates.get(sid) or _today()
        sessions.append(session)
    return sessions
