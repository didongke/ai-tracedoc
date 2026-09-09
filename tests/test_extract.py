"""转录解析与提取规则的单元测试（设计文档 §4.3）。"""
import json
import os
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "src"))  # 让 tests.test_extract 从仓库根目录可运行

from tracedoc.extract import extract_sessions, is_human_question, iter_records_from


def dumps(obj):
    return json.dumps(obj, ensure_ascii=False)


def write_file(path, text):
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(text)


def human(question, sid="s1", ts="2026-09-06T10:00:00.000Z"):
    return {"type": "user", "origin": {"kind": "human"},
            "message": {"role": "user", "content": question},
            "isSidechain": False, "sessionId": sid, "timestamp": ts}


def assistant(blocks, sid="s1"):
    return {"type": "assistant",
            "message": {"role": "assistant", "content": blocks},
            "sessionId": sid}


def text_block(text):
    return {"type": "text", "text": text}


def tool_use_block(name):
    return {"type": "tool_use", "name": name, "id": "call_1", "input": {}}


def tool_result_user(sid="s1"):
    return {"type": "user",
            "message": {"role": "user",
                        "content": [{"type": "tool_result", "tool_use_id": "call_1",
                                     "content": "ok"}]},
            "sessionId": sid}


def join(*records):
    return "".join(dumps(r) + "\n" for r in records)


class TestIterRecordsFrom(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.path = os.path.join(self.tmp, "t.jsonl")

    def test_reads_complete_lines_and_advances_offset(self):
        write_file(self.path, join(human("q1"), human("q2")))
        offset, records = iter_records_from(self.path, 0)
        self.assertEqual(len(records), 2)
        self.assertEqual(offset, os.path.getsize(self.path))

    def test_incomplete_tail_not_advanced(self):
        write_file(self.path, join(human("q1")) + '{"type":"us')
        offset, records = iter_records_from(self.path, 0)
        self.assertEqual(len(records), 1)
        self.assertLess(offset, os.path.getsize(self.path))

    def test_corrupt_mid_line_skipped_and_advanced(self):
        write_file(self.path, "not-json\n" + join(human("q1")))
        offset, records = iter_records_from(self.path, 0)
        self.assertEqual(len(records), 1)
        self.assertEqual(offset, os.path.getsize(self.path))

    def test_start_offset_skips_already_read(self):
        content = join(human("q1"), human("q2"))
        write_file(self.path, content)
        first_end = content.index("\n") + 1
        offset, records = iter_records_from(self.path, first_end)
        self.assertEqual(len(records), 1)
        self.assertEqual(records[0]["message"]["content"], "q2")


class TestIsHumanQuestion(unittest.TestCase):
    def test_typed_human_question(self):
        self.assertTrue(is_human_question(human("你好")))

    def test_tool_result_is_not_human(self):
        self.assertFalse(is_human_question(tool_result_user()))

    def test_sidechain_is_not_human(self):
        r = human("你好")
        r["isSidechain"] = True
        self.assertFalse(is_human_question(r))

    def test_agent_record_is_not_human(self):
        r = human("你好")
        r["agentId"] = "a123"
        self.assertFalse(is_human_question(r))

    def test_missing_origin_is_not_human(self):
        r = human("你好")
        del r["origin"]
        self.assertFalse(is_human_question(r))

    def test_non_user_type(self):
        self.assertFalse(is_human_question(assistant([text_block("hi")])))


class TestExtractSessions(unittest.TestCase):
    def test_basic_qa(self):
        records = [human("怎么设计缓存？"),
                   assistant([{"type": "thinking", "thinking": "内部思考"},
                              text_block("建议用 LRU。")])]
        sessions = extract_sessions(records)
        self.assertEqual(len(sessions), 1)
        self.assertEqual(sessions[0]["entries"],
                         [{"q": "怎么设计缓存？", "a": "建议用 LRU。"}])

    def test_answer_takes_last_assistant_only(self):
        records = [human("q"),
                   assistant([text_block("我先看看。"), tool_use_block("Read")]),
                   tool_result_user(),
                   assistant([text_block("最终结论在这里。")])]
        sessions = extract_sessions(records)
        self.assertEqual(sessions[0]["entries"][0]["a"], "最终结论在这里。")

    def test_answer_empty_when_ends_with_tool_use(self):
        records = [human("q"),
                   assistant([text_block("我先看看。"), tool_use_block("Read")]),
                   tool_result_user(),
                   assistant([tool_use_block("Edit")])]
        sessions = extract_sessions(records)
        self.assertIsNone(sessions[0]["entries"][0]["a"])

    def test_multiple_text_blocks_joined(self):
        records = [human("q"),
                   assistant([text_block("第一段。"), text_block("第二段。")])]
        sessions = extract_sessions(records)
        self.assertEqual(sessions[0]["entries"][0]["a"], "第一段。\n\n第二段。")

    def test_consecutive_questions_each_get_entry(self):
        records = [human("q1"),
                   assistant([text_block("a1")]),
                   human("q2")]
        sessions = extract_sessions(records)
        self.assertEqual([e["q"] for e in sessions[0]["entries"]], ["q1", "q2"])
        self.assertEqual(sessions[0]["entries"][1]["a"], None)

    def test_sidechain_assistant_ignored(self):
        side = assistant([text_block("子代理的话")])
        side["isSidechain"] = True
        records = [human("q"), side,
                   assistant([text_block("真正的回答。")])]
        sessions = extract_sessions(records)
        self.assertEqual(sessions[0]["entries"][0]["a"], "真正的回答。")

    def test_title_last_ai_title_wins(self):
        records = [{"type": "ai-title", "aiTitle": "旧标题", "sessionId": "s1"},
                   human("q"),
                   assistant([text_block("a")]),
                   {"type": "ai-title", "aiTitle": "新标题", "sessionId": "s1"}]
        sessions = extract_sessions(records)
        self.assertEqual(sessions[0]["title"], "新标题")

    def test_title_fallback_to_first_question(self):
        records = [human("这是一段超过四十个字符的长问题一二三四五六七八九十甲乙丙丁"),
                   assistant([text_block("a")])]
        sessions = extract_sessions(records)
        title = sessions[0]["title"]
        self.assertLessEqual(len(title), 41)
        self.assertTrue(title.startswith("这是一段超过四十"))

    def test_session_date_from_first_question(self):
        records = [human("q", ts="2026-09-06T10:00:00.000Z"),
                   assistant([text_block("a")])]
        sessions = extract_sessions(records)
        self.assertEqual(sessions[0]["date"], "2026-09-06")

    def test_multiple_sessions_grouped_in_order(self):
        records = [human("q1", sid="s1"), assistant([text_block("a1")], sid="s1"),
                   human("q2", sid="s2"), assistant([text_block("a2")], sid="s2")]
        sessions = extract_sessions(records)
        self.assertEqual([s["session_id"] for s in sessions], ["s1", "s2"])


if __name__ == "__main__":
    unittest.main()
