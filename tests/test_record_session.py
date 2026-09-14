"""Adapter end-to-end tests: invoke record-session.py via subprocess as the
hook would. Chinese fixture content simulates real users and verifies that
content is recorded verbatim."""
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


def run_hook(cwd, transcript, session_id="s1", hook_event_name="SessionEnd"):
    payload = {"session_id": session_id, "transcript_path": transcript,
               "cwd": cwd, "reason": "other", "hook_event_name": hook_event_name}
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
        # An ai-title record first, as real transcripts carry one. Without
        # it the fallback title (the first question) would make
        # test_hook_continues_new_records_only's count(...) == 1 assertion
        # unsatisfiable (header line + body line = 2).
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
        return "%s-%s-%s" % (today, project, "tracedoc.md")

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
        self.assertNotIn("Read", proc.stdout)   # tool calls are not recorded
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
        # the question that ends in a tool call is recorded question-only
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

    def test_stop_hook_incomplete_chain_deferred(self):
        """Stop mode: a trailing chain ending in a tool call is deferred;
        once completed it is appended exactly once."""
        self.enable_marker()
        with open(self.transcript, "w", encoding="utf-8") as fh:
            fh.write(dumps(human("q1")) + "\n")
            fh.write(dumps(assistant("a1")) + "\n")
            fh.write(dumps(human("q2")) + "\n")
            fh.write(dumps(tool_use("Edit")) + "\n")
        run_hook(self.tmp, self.transcript, hook_event_name="Stop")
        content = self.ledger_content()
        self.assertIn("q1", content)
        self.assertIn("a1", content)
        self.assertNotIn("q2", content)   # incomplete chain not recorded yet
        # second run: q2's answer has arrived (same transcript, appended)
        with open(self.transcript, "a", encoding="utf-8") as fh:
            fh.write(dumps(assistant("a2")) + "\n")
        run_hook(self.tmp, self.transcript, hook_event_name="Stop")
        content = self.ledger_content()
        self.assertIn("q2", content)
        self.assertIn("a2", content)
        # entries carry a time prefix; count entry markers (the fallback
        # session title also contains q1)
        self.assertEqual(content.count("**Question:** "), 2)   # q1+q2, no dupes

    def test_sessionend_records_incomplete_chain_question_only(self):
        """SessionEnd mode: a trailing chain ending in a tool call is
        recorded question-only and fully consumed."""
        self.enable_marker()
        with open(self.transcript, "w", encoding="utf-8") as fh:
            fh.write(dumps(human("q1")) + "\n")
            fh.write(dumps(assistant("a1")) + "\n")
            fh.write(dumps(human("q2")) + "\n")
            fh.write(dumps(tool_use("Edit")) + "\n")
        run_hook(self.tmp, self.transcript, hook_event_name="SessionEnd")
        content = self.ledger_content()
        self.assertEqual(content.count("**Question:**"), 2)
        self.assertEqual(content.count("**Answer:**"), 1)   # only q1 answered
        self.assertIn("q2", content)

    def test_no_marker_no_ledger(self):
        proc = run_hook(self.tmp, self.transcript)
        self.assertEqual(proc.returncode, 0)
        self.assertFalse(os.path.exists(
            os.path.join(self.tmp, self.expected_ledger_name())))

    def test_bad_stdin_exits_zero(self):
        self.enable_marker()
        # universal_newlines=True, matching run_hook: text mode is required
        # to send a str on stdin, otherwise Python 3.7's subprocess raises
        # TypeError in _communicate (see the task report)
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
