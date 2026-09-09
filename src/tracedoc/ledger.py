"""开发过程账本管理（核心层，跨 agent 通用）。

命名规则、状态文件、项目级排他锁与卷管理见设计文档 §2/§4.3。
分卷编排函数 append_session 在 Task 4 加入。
"""
import contextlib
import datetime
import fcntl
import glob
import json
import os
import re

LEDGER_SUFFIX = "开发过程.md"
VOLUME_THRESHOLD_BYTES = 200 * 1024
STATE_FILENAME = ".tracedoc-state.json"
LOCK_FILENAME = ".tracedoc.lock"
MARKER_FILENAME = ".tracedoc-on"


class LedgerError(Exception):
    """账本定位歧义等无法安全继续的情况；调用方应记录后静默退出。"""


def _today():
    return datetime.date.today().strftime("%Y%m%d")


def volume_number(filename):
    """账本文件名 → 卷号。无 -NN 后缀视为第 1 卷。"""
    match = re.search(r"-(\d+)\.md$", filename)
    if match:
        return int(match.group(1))
    return 1


def volume_filename(project, today, number):
    """卷号 → 文件名。第 1 卷不加序号（锁定命名），续卷加两位序号。"""
    if number == 1:
        return "%s-%s-%s" % (today, project, LEDGER_SUFFIX)
    return "%s-%s-开发过程-%02d.md" % (today, project, number)


def ledger_pattern(project):
    return "*-%s-开发过程*.md" % project


def create_volume(cwd, project, today, prev_fn, number):
    """新建账本卷，写入标题头（续卷写"上接"链接），返回文件名。"""
    name = volume_filename(project, today, number)
    path = os.path.join(cwd, name)
    if number == 1:
        header = "# %s · 开发过程记录\n\n" % project
        header += ("本文件由 ai-tracedoc 插件自动提取自 Claude Code "
                   "会话转录，未经人工整理。\n\n")
    else:
        header = "# %s · 开发过程记录（续卷 %d）\n\n" % (project, number)
        header += "上接：[%s](./%s)\n\n" % (prev_fn, prev_fn)
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(header)
    return name


def locate_latest_volume(cwd, project, state):
    """定位当前应写入的账本卷。

    优先状态文件中的 current_volume 指针；否则 glob 取卷号最大者。
    存在多个同号卷（歧义）时抛 LedgerError。
    """
    current = (state or {}).get("current_volume")
    if current and os.path.isfile(os.path.join(cwd, current)):
        return current
    matches = glob.glob(os.path.join(cwd, ledger_pattern(project)))
    if not matches:
        return None
    names = [os.path.basename(m) for m in matches]
    names.sort(key=volume_number)
    best = names[-1]
    if sum(1 for n in names if volume_number(n) == volume_number(best)) > 1:
        raise LedgerError("账本卷号歧义: %s" % ", ".join(sorted(names)))
    return best


def format_session_header(date_str, title, session_id):
    return "\n## %s · %s\n\n<!-- session: %s -->\n\n" % (date_str, title, session_id)


def format_entries(entries):
    """问答条目 → Markdown。a 为 None 时只写问、不写答行。"""
    parts = []
    for entry in entries:
        parts.append("**问：** %s\n" % entry["q"])
        if entry.get("a"):
            parts.append("\n**答：** %s\n" % entry["a"])
        parts.append("")
    return "\n".join(parts)


def load_state(cwd):
    """读取状态文件；缺失或损坏返回空 dict。"""
    path = os.path.join(cwd, STATE_FILENAME)
    try:
        with open(path, encoding="utf-8") as fh:
            data = json.load(fh)
        if isinstance(data, dict):
            return data
    except (IOError, ValueError):
        pass
    return {}


def save_state(cwd, state):
    """原子写状态文件（临时文件 + replace）。"""
    path = os.path.join(cwd, STATE_FILENAME)
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as fh:
        json.dump(state, fh, ensure_ascii=False, indent=2)
    os.replace(tmp, path)


@contextlib.contextmanager
def project_lock(cwd):
    """项目级排他锁：包住"读状态→写账本→写状态"的整段临界区。

    POSIX 平台（Linux/macOS）用 fcntl.flock；Windows 暂不支持（设计文档 §8）。
    适配器必须先取锁，再 load_state / append_session / save_state。
    """
    path = os.path.join(cwd, LOCK_FILENAME)
    with open(path, "a") as lock_fh:
        fcntl.flock(lock_fh.fileno(), fcntl.LOCK_EX)
        try:
            yield
        finally:
            fcntl.flock(lock_fh.fileno(), fcntl.LOCK_UN)


def _append_text(volume_path, text):
    """纯追加（单次 write）。调用方必须已持有 project_lock。"""
    with open(volume_path, "a", encoding="utf-8") as fh:
        fh.write(text)


def append_footer_link(cwd, prev_fn, next_fn):
    """旧卷末尾追加"下接"衔接标注。调用方必须已持有 project_lock。"""
    _append_text(os.path.join(cwd, prev_fn),
                 "\n\n---\n\n下接：[%s](./%s)\n" % (next_fn, next_fn))


def should_start_new_volume(volume_path, session_is_new, threshold):
    """只有新会话且当前卷超阈值时才开新卷；老会话续写永不跨卷。"""
    if not session_is_new:
        return False
    try:
        size = os.path.getsize(volume_path)
    except OSError:
        return False
    return size > threshold


def append_session(cwd, project, session, state, today=None,
                   threshold=VOLUME_THRESHOLD_BYTES):
    """把一次会话的问答追加进账本（分卷编排，设计文档 §4.3 第 4 步）。

    session: {"session_id","title","date","entries":[{"q","a"}]}
    调用方必须已持有 project_lock(cwd)（锁内完成读状态→写账本→写状态）。
    就地更新并返回 state；entries 为空时调用方不应传入。
    """
    sid = session["session_id"]
    known = (state.get("sessions") or {}).get(sid)
    if known and known.get("volume"):
        _append_text(os.path.join(cwd, known["volume"]),
                     format_entries(session["entries"]))
        return state

    current = locate_latest_volume(cwd, project, state)
    if current is not None:
        if should_start_new_volume(os.path.join(cwd, current), True, threshold):
            number = volume_number(current) + 1
            new_fn = create_volume(cwd, project, today or _today(),
                                   current, number)
            append_footer_link(cwd, current, new_fn)
            current = new_fn
    else:
        current = create_volume(cwd, project, today or _today(), None, 1)

    text = format_session_header(session["date"], session["title"], sid)
    text += format_entries(session["entries"])
    _append_text(os.path.join(cwd, current), text)

    state.setdefault("sessions", {})[sid] = {"offset": 0, "volume": current}
    state["current_volume"] = current
    return state
