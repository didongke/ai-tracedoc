"""适配器端到端测试：通过子进程以 hook 身份调用 record-session.py。"""
import datetime
import json
import os
import subprocess
import sys
import tempfile
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SCRIPT = os.path.join(ROOT, "hooks", "record-session.py")


def dumps(obj):
    return json.dumps(obj, ensure_ascii=False)


def human(q, sid="s1"):
    return {"type": "user", "origin": {"kind": "human"},
            "message": {"role": "user", "content": q},
            "isSidechain": False, "sessionId": sid,
            "timestamp": "2026-09-06T10:00:00.000Z"}


def assistant(text, sid="s1"):
    return {"type": "assistant",
            "message": {"role": "assistant", "content": [{"type": "text", "text": text}]},
            "sessionId": sid}


def tool_use(name):
    return {"type": "assistant",
            "message": {"role": "assistant",
                        "content": [{"type": "tool_use", "name": name,
                                     "id": "call_1", "input": {}}]},
            "sessionId": "s1"}


def write_transcript(path, records):
    with open(path, "w", encoding="utf-8") as fh:
        for r in records:
            fh.write(dumps(r) + "\n")


def run_hook(cwd, transcript, session_id="s1"):
    payload = {"session_id": session_id, "transcript_path": transcript,
               "cwd": cwd, "reason": "other"}
    proc = subprocess.run(
        [sys.executable, SCRIPT], input=json.dumps(payload),
        stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        universal_newlines=True)
    return proc


def run_self_test(transcript):
    proc = subprocess.run(
        [sys.executable, SCRIPT, "--self-test", transcript],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        universal_newlines=True)
    return proc


class AdapterTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.transcript = os.path.join(self.tmp, "session.jsonl")
        # ai-title 先行：真实转录常带会话标题。缺省时标题回退为首个提问，
        # 会把 "怎么设计缓存？" 写进会话头，导致 test_hook_continues_new_records_only
        # 的 count(...) == 1 断言永远失败（标题行 + 正文行 = 2），见报告。
        write_transcript(self.transcript, [
            {"type": "ai-title", "aiTitle": "缓存设计讨论", "sessionId": "s1"},
            human("怎么设计缓存？"),
            assistant("建议用 LRU。"),
            human("为什么不用 LFU？"),
            tool_use("Read"),
        ])

    def expected_ledger_name(self):
        today = datetime.date.today().strftime("%Y%m%d")
        project = os.path.basename(self.tmp)
        return "%s-%s-%s" % (today, project, "开发过程.md")

    def ledger_content(self):
        path = os.path.join(self.tmp, self.expected_ledger_name())
        with open(path, encoding="utf-8") as fh:
            return fh.read()


class TestSelfTest(AdapterTest):
    def test_self_test_prints_entries_without_writing(self):
        proc = run_self_test(self.transcript)
        self.assertEqual(proc.returncode, 0)
        self.assertIn("怎么设计缓存？", proc.stdout)
        self.assertIn("建议用 LRU。", proc.stdout)
        self.assertIn("为什么不用 LFU？", proc.stdout)
        self.assertNotIn("Read", proc.stdout)   # 工具调用不进账
        self.assertFalse(os.path.exists(
            os.path.join(self.tmp, self.expected_ledger_name())))

    def test_self_test_usage_error(self):
        proc = subprocess.run([sys.executable, SCRIPT, "--self-test"],
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.assertEqual(proc.returncode, 1)


class TestHookRun(AdapterTest):
    def enable_marker(self):
        open(os.path.join(self.tmp, ".tracedoc-on"), "w").close()

    def test_hook_creates_ledger(self):
        self.enable_marker()
        proc = run_hook(self.tmp, self.transcript)
        self.assertEqual(proc.returncode, 0)
        content = self.ledger_content()
        self.assertIn("怎么设计缓存？", content)
        self.assertIn("建议用 LRU。", content)
        # 以工具调用收尾的提问：只记问、无答行
        self.assertIn("为什么不用 LFU？", content)

    def test_hook_runs_twice_no_duplicate(self):
        self.enable_marker()
        run_hook(self.tmp, self.transcript)
        first = self.ledger_content()
        run_hook(self.tmp, self.transcript)
        self.assertEqual(self.ledger_content(), first)

    def test_hook_continues_new_records_only(self):
        self.enable_marker()
        run_hook(self.tmp, self.transcript)
        with open(self.transcript, "a", encoding="utf-8") as fh:
            fh.write(dumps(human("新问题")) + "\n")
            fh.write(dumps(assistant("新回答")) + "\n")
        run_hook(self.tmp, self.transcript)
        content = self.ledger_content()
        self.assertIn("新问题", content)
        self.assertEqual(content.count("怎么设计缓存？"), 1)

    def test_no_marker_no_ledger(self):
        proc = run_hook(self.tmp, self.transcript)
        self.assertEqual(proc.returncode, 0)
        self.assertFalse(os.path.exists(
            os.path.join(self.tmp, self.expected_ledger_name())))

    def test_bad_stdin_exits_zero(self):
        self.enable_marker()
        # universal_newlines=True：与 run_hook 一致（文本模式才能送 str 输入，
        # 否则 Python 3.7 的 subprocess 在 _communicate 直接抛 TypeError，见报告）
        proc = subprocess.run(
            [sys.executable, SCRIPT], input="not-json",
            stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            universal_newlines=True)
        self.assertEqual(proc.returncode, 0)

    def test_no_question_run_then_resume(self):
        self.enable_marker()
        with open(self.transcript, "w", encoding="utf-8") as fh:
            fh.write(dumps({"type": "ai-title", "aiTitle": "缓存设计讨论",
                            "sessionId": "s1"}) + "\n")
        run_hook(self.tmp, self.transcript)
        self.assertFalse(os.path.exists(
            os.path.join(self.tmp, self.expected_ledger_name())))
        with open(self.transcript, "a", encoding="utf-8") as fh:
            fh.write(dumps(human("怎么设计缓存？")) + "\n")
            fh.write(dumps(assistant("建议用 LRU。")) + "\n")
        run_hook(self.tmp, self.transcript)
        content = self.ledger_content()
        self.assertIn("怎么设计缓存？", content)
        self.assertIn("建议用 LRU。", content)
        self.assertIn("<!-- session: s1 -->", content)


if __name__ == "__main__":
    unittest.main()
