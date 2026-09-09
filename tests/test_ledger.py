"""账本定位、新建、状态文件与追加写入的单元测试（设计文档 §4.3/§4.5）。"""
import json
import os
import sys
import tempfile
import threading
import time
import unittest

HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.join(HERE, "src"))

from tracedoc import ledger

PROJECT = "proj-x"
TODAY = "20260906"


class LedgerTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.cwd = self.tmp

    def vol_path(self, name):
        return os.path.join(self.cwd, name)

    def read(self, name):
        with open(self.vol_path(name), encoding="utf-8") as fh:
            return fh.read()


class TestVolumeNumber(unittest.TestCase):
    def test_first_volume_is_one(self):
        self.assertEqual(ledger.volume_number("20260906-proj-x-开发过程.md"), 1)

    def test_suffixed_volume(self):
        self.assertEqual(ledger.volume_number("20260906-proj-x-开发过程-02.md"), 2)
        self.assertEqual(ledger.volume_number("20260906-proj-x-开发过程-13.md"), 13)


class TestVolumeFilename(LedgerTest):
    def test_first_volume_no_suffix(self):
        self.assertEqual(ledger.volume_filename(PROJECT, TODAY, 1),
                         "20260906-proj-x-开发过程.md")

    def test_later_volume_two_digits(self):
        self.assertEqual(ledger.volume_filename(PROJECT, TODAY, 2),
                         "20260906-proj-x-开发过程-02.md")


class TestCreateVolume(LedgerTest):
    def test_creates_first_volume_with_header(self):
        name = ledger.create_volume(self.cwd, PROJECT, TODAY, None, 1)
        self.assertEqual(name, "20260906-proj-x-开发过程.md")
        content = self.read(name)
        self.assertIn("# proj-x · 开发过程记录", content)
        self.assertIn("ai-tracedoc", content)   # 说明行

    def test_creates_later_volume_with_link(self):
        name = ledger.create_volume(self.cwd, PROJECT, TODAY,
                                    "20260906-proj-x-开发过程.md", 2)
        content = self.read(name)
        self.assertIn("续卷 2", content)
        self.assertIn("上接：[20260906-proj-x-开发过程.md]", content)


class TestLocateLatestVolume(LedgerTest):
    def test_none_when_no_volume(self):
        self.assertIsNone(ledger.locate_latest_volume(self.cwd, PROJECT, {}))

    def test_state_pointer_wins(self):
        ledger.create_volume(self.cwd, PROJECT, TODAY, None, 1)
        ledger.create_volume(self.cwd, PROJECT, TODAY, None, 2)
        state = {"current_volume": "20260906-proj-x-开发过程.md"}
        self.assertEqual(ledger.locate_latest_volume(self.cwd, PROJECT, state),
                         "20260906-proj-x-开发过程.md")

    def test_glob_picks_highest_number(self):
        ledger.create_volume(self.cwd, PROJECT, TODAY, None, 1)
        ledger.create_volume(self.cwd, PROJECT, TODAY, None, 2)
        self.assertEqual(ledger.locate_latest_volume(self.cwd, PROJECT, {}),
                         "20260906-proj-x-开发过程-02.md")

    def test_ambiguous_volumes_raise(self):
        for i in (1, 1):
            ledger.create_volume(self.cwd, PROJECT, TODAY, None, i)
        # 两个同名第一卷无法同时存在，构造同名文件模拟歧义
        with open(self.vol_path("20260906-proj-x-开发过程.md"), "w") as fh:
            fh.write("x")
        with open(self.vol_path("20990101-proj-x-开发过程.md"), "w") as fh:
            fh.write("x")
        with self.assertRaises(ledger.LedgerError):
            ledger.locate_latest_volume(self.cwd, PROJECT, {})


class TestFormatters(unittest.TestCase):
    def test_format_session_header(self):
        text = ledger.format_session_header("2026-09-06", "标题", "s1")
        self.assertIn("## 2026-09-06 · 标题", text)
        self.assertIn("<!-- session: s1 -->", text)

    def test_format_entries_with_and_without_answer(self):
        text = ledger.format_entries([{"q": "q1", "a": "a1", "t": "2026-09-06 18:00"},
                                      {"q": "q2", "a": None}])
        self.assertIn("**问：** 2026-09-06 18:00 · q1", text)
        self.assertIn("**答：** a1", text)
        self.assertIn("**问：** q2", text)   # 无时间戳条目：不带时间前缀
        self.assertEqual(text.count("**答：**"), 1)


class TestStateFile(LedgerTest):
    def test_roundtrip(self):
        state = {"current_volume": "v.md", "sessions": {"s1": {"offset": 5, "volume": "v.md"}}}
        ledger.save_state(self.cwd, state)
        loaded = ledger.load_state(self.cwd)
        self.assertEqual(loaded, state)

    def test_missing_or_corrupt_state_returns_fresh(self):
        self.assertEqual(ledger.load_state(self.cwd), {})
        with open(os.path.join(self.cwd, ledger.STATE_FILENAME), "w") as fh:
            fh.write("not-json")
        self.assertEqual(ledger.load_state(self.cwd), {})


class TestProjectLock(LedgerTest):
    def test_lock_blocks_concurrent_holder(self):
        """设计文档 §6 并发要求：临界区受项目级锁串行化。"""
        done = []

        def worker():
            with ledger.project_lock(self.cwd):
                done.append(True)

        with ledger.project_lock(self.cwd):
            thread = threading.Thread(target=worker)
            thread.start()
            time.sleep(0.3)
            self.assertEqual(done, [])     # 锁被占用时应阻塞
        thread.join(timeout=5)
        self.assertEqual(done, [True])

    def test_lock_released_after_exit(self):
        with ledger.project_lock(self.cwd):
            pass
        with ledger.project_lock(self.cwd):   # 释放后可再次获取
            pass


class TestAppendText(LedgerTest):
    def test_appends_and_preserves_existing(self):
        path = self.vol_path("t.md")
        with open(path, "w", encoding="utf-8") as fh:
            fh.write("已有内容\n")
        ledger._append_text(path, "新增内容\n")
        self.assertEqual(self.read("t.md"), "已有内容\n新增内容\n")


class TestAppendSession(LedgerTest):
    """append_session 要求调用方持锁，所有用例在 project_lock 内执行。"""

    def session(self, sid, qs, date="2026-09-06", title="标题"):
        return {"session_id": sid, "title": title, "date": date,
                "entries": [{"q": q, "a": "答%s" % q} for q in qs]}

    def test_new_session_creates_volume(self):
        state = {}
        with ledger.project_lock(self.cwd):
            ledger.append_session(self.cwd, PROJECT, self.session("s1", ["q1"]),
                                  state, today=TODAY)
        content = self.read("20260906-proj-x-开发过程.md")
        self.assertIn("## 2026-09-06 · 标题", content)
        self.assertIn("**问：** q1", content)
        self.assertIn("**答：** 答q1", content)
        self.assertEqual(state["current_volume"], "20260906-proj-x-开发过程.md")

    def test_second_session_appends_same_volume(self):
        state = {}
        with ledger.project_lock(self.cwd):
            ledger.append_session(self.cwd, PROJECT, self.session("s1", ["q1"]),
                                  state, today=TODAY)
            ledger.append_session(self.cwd, PROJECT, self.session("s2", ["q2"]),
                                  state, today=TODAY)
        content = self.read("20260906-proj-x-开发过程.md")
        self.assertIn("q1", content)
        self.assertIn("q2", content)
        self.assertEqual(content.count("<!-- session: "), 2)
        self.assertEqual(state["current_volume"], "20260906-proj-x-开发过程.md")

    def test_known_session_appends_entries_only(self):
        state = {}
        with ledger.project_lock(self.cwd):
            ledger.append_session(self.cwd, PROJECT, self.session("s1", ["q1"]),
                                  state, today=TODAY)
            ledger.append_session(self.cwd, PROJECT, self.session("s1", ["q2"]),
                                  state, today=TODAY)
        content = self.read("20260906-proj-x-开发过程.md")
        self.assertEqual(content.count("<!-- session: s1 -->"), 1)
        self.assertIn("**问：** q2", content)

    def test_overflow_creates_second_volume_with_links(self):
        state = {}
        with ledger.project_lock(self.cwd):
            # 阈值压到 1 字节强制分卷
            ledger.append_session(self.cwd, PROJECT, self.session("s1", ["q1"]),
                                  state, today=TODAY, threshold=1)
            ledger.append_session(self.cwd, PROJECT, self.session("s2", ["q2"]),
                                  state, today=TODAY, threshold=1)
        vol1 = self.read("20260906-proj-x-开发过程.md")
        vol2 = self.read("20260906-proj-x-开发过程-02.md")
        self.assertIn("q1", vol1)
        self.assertNotIn("q2", vol1)
        self.assertIn("下接：[20260906-proj-x-开发过程-02.md]", vol1)
        self.assertIn("上接：[20260906-proj-x-开发过程.md]", vol2)
        self.assertIn("q2", vol2)
        self.assertEqual(state["current_volume"], "20260906-proj-x-开发过程-02.md")

    def test_known_session_continuation_never_splits(self):
        state = {}
        with ledger.project_lock(self.cwd):
            ledger.append_session(self.cwd, PROJECT, self.session("s1", ["q1"]),
                                  state, today=TODAY)
            # 卷 1 已超阈值（threshold=1），但 s1 是老会话 → 继续写卷 1，不开新卷
            ledger.append_session(self.cwd, PROJECT, self.session("s1", ["q2"]),
                                  state, today=TODAY, threshold=1)
        self.assertFalse(os.path.exists(
            self.vol_path("20260906-proj-x-开发过程-02.md")))
        content = self.read("20260906-proj-x-开发过程.md")
        self.assertIn("q2", content)

    def test_overflow_uses_today_for_new_volume(self):
        state = {}
        with ledger.project_lock(self.cwd):
            ledger.append_session(self.cwd, PROJECT, self.session("s1", ["q1"]),
                                  state, today="20260101", threshold=1)
            ledger.append_session(self.cwd, PROJECT, self.session("s2", ["q2"]),
                                  state, today="20260315", threshold=1)
        self.assertTrue(os.path.exists(
            self.vol_path("20260315-proj-x-开发过程-02.md")))

    def test_recreates_after_deletion(self):
        """设计文档 §5：账本被删除后，下个会话按新建处理。"""
        state = {}
        with ledger.project_lock(self.cwd):
            ledger.append_session(self.cwd, PROJECT, self.session("s1", ["q1"]),
                                  state, today=TODAY)
            os.remove(self.vol_path("20260906-proj-x-开发过程.md"))
            ledger.append_session(self.cwd, PROJECT, self.session("s2", ["q2"]),
                                  state, today=TODAY)
        content = self.read("20260906-proj-x-开发过程.md")
        self.assertIn("q2", content)
        self.assertNotIn("q1", content)

    def test_known_session_with_empty_volume_reregisters(self):
        """历史损坏状态（known 但 volume 为空串）：按新会话重走定位/建卷/写会话头。"""
        state = {"sessions": {"s1": {"offset": 5, "volume": ""}}}
        with ledger.project_lock(self.cwd):
            ledger.append_session(self.cwd, PROJECT, self.session("s1", ["q1"]),
                                  state, today=TODAY)
        content = self.read("20260906-proj-x-开发过程.md")
        self.assertIn("<!-- session: s1 -->", content)
        self.assertIn("**问：** q1", content)
        self.assertEqual(
            state["sessions"]["s1"]["volume"], "20260906-proj-x-开发过程.md")


if __name__ == "__main__":
    unittest.main()
