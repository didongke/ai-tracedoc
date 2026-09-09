"""转录解析与提取规则的单元测试（设计文档 §4.3）。"""
import json
import os
import sys
import tempfile
import unittest
from datetime import timedelta, timezone

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "src"))  # 让 tests.test_extract 从仓库根目录可运行

import tracedoc.extract as extract
from tracedoc.extract import extract_sessions, is_human_question, iter_records_from

TZ8 = timezone(timedelta(hours=8))


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
        offset, records, ends = iter_records_from(self.path, 0)
        self.assertEqual(len(records), 2)
        self.assertEqual(len(ends), 2)
        self.assertEqual(offset, os.path.getsize(self.path))
        self.assertEqual(ends[-1], offset)

    def test_incomplete_tail_not_advanced(self):
        write_file(self.path, join(human("q1")) + '{"type":"us')
        offset, records, ends = iter_records_from(self.path, 0)
        self.assertEqual(len(records), 1)
        self.assertEqual(len(ends), 1)
        self.assertLess(offset, os.path.getsize(self.path))

    def test_corrupt_mid_line_skipped_and_advanced(self):
        write_file(self.path, "not-json\n" + join(human("q1")))
        offset, records, ends = iter_records_from(self.path, 0)
        self.assertEqual(len(records), 1)
        self.assertEqual(offset, os.path.getsize(self.path))
        self.assertEqual(ends[0], offset)   # 坏行无 ends 记录，好行结束即文件尾

    def test_start_offset_skips_already_read(self):
        content = join(human("q1"), human("q2"))
        write_file(self.path, content)
        first_end = content.index("\n") + 1
        offset, records, ends = iter_records_from(self.path, first_end)
        self.assertEqual(len(records), 1)
        self.assertEqual(records[0]["message"]["content"], "q2")
        self.assertEqual(ends[0], os.path.getsize(self.path))


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


class TestFormatLocalTime(unittest.TestCase):
    def test_utc_z_converted_to_given_tz(self):
        self.assertEqual(
            extract._format_local_time("2026-09-06T10:00:00.000Z", TZ8),
            "2026-09-06 18:00")

    def test_crosses_midnight(self):
        self.assertEqual(
            extract._format_local_time("2026-09-06T23:30:00Z", TZ8),
            "2026-09-07 07:30")

    def test_invalid_or_missing_returns_empty(self):
        self.assertEqual(extract._format_local_time(""), "")
        self.assertEqual(extract._format_local_time("not-a-time"), "")


class TestExtractSessions(unittest.TestCase):
    def test_basic_qa(self):
        records = [human("怎么设计缓存？"),
                   assistant([{"type": "thinking", "thinking": "内部思考"},
                              text_block("建议用 LRU。")])]
        sessions, consumed = extract_sessions(records, tz=TZ8)
        self.assertEqual(len(sessions), 1)
        self.assertEqual(
            sessions[0]["entries"],
            [{"q": "怎么设计缓存？", "a": "建议用 LRU。",
              "t": "2026-09-06 18:00"}])
        self.assertEqual(consumed, len(records))

    def test_answer_takes_last_assistant_only(self):
        records = [human("q"),
                   assistant([text_block("我先看看。"), tool_use_block("Read")]),
                   tool_result_user(),
                   assistant([text_block("最终结论在这里。")])]
        sessions, consumed = extract_sessions(records, tz=TZ8)
        self.assertEqual(sessions[0]["entries"][0]["a"], "最终结论在这里。")
        self.assertEqual(consumed, len(records))

    def test_answer_empty_when_ends_with_tool_use(self):
        """SessionEnd 模式（complete_only=False）：以工具调用收尾的链记问不记答，全部消费。"""
        records = [human("q"),
                   assistant([text_block("我先看看。"), tool_use_block("Read")]),
                   tool_result_user(),
                   assistant([tool_use_block("Edit")])]
        sessions, consumed = extract_sessions(records, tz=TZ8, complete_only=False)
        self.assertIsNone(sessions[0]["entries"][0]["a"])
        self.assertEqual(consumed, len(records))

    def test_multiple_text_blocks_joined(self):
        records = [human("q"),
                   assistant([text_block("第一段。"), text_block("第二段。")])]
        sessions, consumed = extract_sessions(records, tz=TZ8)
        self.assertEqual(sessions[0]["entries"][0]["a"], "第一段。\n\n第二段。")
        self.assertEqual(consumed, len(records))

    def test_consecutive_questions_each_get_entry(self):
        """被新提问闭合的链立即入账；SessionEnd 模式下末链无回复也入账（答留空）。"""
        records = [human("q1"),
                   assistant([text_block("a1")]),
                   human("q2")]
        sessions, consumed = extract_sessions(records, tz=TZ8, complete_only=False)
        self.assertEqual([e["q"] for e in sessions[0]["entries"]], ["q1", "q2"])
        self.assertEqual(sessions[0]["entries"][1]["a"], None)
        self.assertEqual(sessions[0]["entries"][1]["t"], "2026-09-06 18:00")
        self.assertEqual(consumed, 3)

    def test_sidechain_assistant_ignored(self):
        side = assistant([text_block("子代理的话")])
        side["isSidechain"] = True
        records = [human("q"), side,
                   assistant([text_block("真正的回答。")])]
        sessions, consumed = extract_sessions(records, tz=TZ8)
        self.assertEqual(sessions[0]["entries"][0]["a"], "真正的回答。")
        self.assertEqual(consumed, len(records))

    def test_title_last_ai_title_wins(self):
        records = [{"type": "ai-title", "aiTitle": "旧标题", "sessionId": "s1"},
                   human("q"),
                   assistant([text_block("a")]),
                   {"type": "ai-title", "aiTitle": "新标题", "sessionId": "s1"}]
        sessions, consumed = extract_sessions(records, tz=TZ8)
        self.assertEqual(sessions[0]["title"], "新标题")
        self.assertEqual(consumed, len(records))

    def test_title_fallback_to_first_question(self):
        records = [human("这是一段超过四十个字符的长问题一二三四五六七八九十甲乙丙丁"),
                   assistant([text_block("a")])]
        sessions, consumed = extract_sessions(records, tz=TZ8)
        title = sessions[0]["title"]
        self.assertLessEqual(len(title), 41)
        self.assertTrue(title.startswith("这是一段超过四十"))
        self.assertEqual(consumed, len(records))

    def test_session_date_from_first_question(self):
        records = [human("q", ts="2026-09-06T10:00:00.000Z"),
                   assistant([text_block("a")])]
        sessions, consumed = extract_sessions(records, tz=TZ8)
        self.assertEqual(sessions[0]["date"], "2026-09-06")
        self.assertEqual(consumed, len(records))

    def test_session_date_follows_local_time(self):
        """本地时间跨天时，会话头日期取本地日期（UTC 23:30 → 本地次日 07:30）。"""
        records = [human("q", ts="2026-09-06T23:30:00Z"),
                   assistant([text_block("a")])]
        sessions, consumed = extract_sessions(records, tz=TZ8)
        self.assertEqual(sessions[0]["date"], "2026-09-07")
        self.assertEqual(consumed, len(records))

    def test_missing_timestamp_yields_empty_time(self):
        r = human("q")
        del r["timestamp"]
        sessions, consumed = extract_sessions([r, assistant([text_block("a")])], tz=TZ8)
        self.assertEqual(sessions[0]["entries"][0]["t"], "")
        self.assertEqual(consumed, 2)

    def test_multiple_sessions_grouped_in_order(self):
        records = [human("q1", sid="s1"), assistant([text_block("a1")], sid="s1"),
                   human("q2", sid="s2"), assistant([text_block("a2")], sid="s2")]
        sessions, consumed = extract_sessions(records, tz=TZ8)
        self.assertEqual([s["session_id"] for s in sessions], ["s1", "s2"])
        self.assertEqual(consumed, len(records))

    def test_incomplete_final_chain_excluded_by_default(self):
        """Stop 模式默认：末链以工具调用收尾 → 不提取、不消费。"""
        records = [human("q1"),
                   assistant([text_block("a1")]),
                   human("q2"),
                   assistant([tool_use_block("Edit")])]
        sessions, consumed = extract_sessions(records, tz=TZ8)
        self.assertEqual([e["q"] for e in sessions[0]["entries"]], ["q1"])
        self.assertEqual(consumed, 2)   # q1 链（前 2 条记录）已消费

    def test_question_without_assistant_excluded_by_default(self):
        records = [human("q")]
        sessions, consumed = extract_sessions(records, tz=TZ8)
        self.assertEqual(sessions, [])
        self.assertEqual(consumed, 0)

    def test_complete_only_false_consumes_question_only(self):
        """SessionEnd 模式：无 AI 回复的提问也入账（答留空），全部消费。"""
        records = [human("q")]
        sessions, consumed = extract_sessions(records, tz=TZ8, complete_only=False)
        self.assertEqual(sessions[0]["entries"][0]["q"], "q")
        self.assertIsNone(sessions[0]["entries"][0]["a"])
        self.assertEqual(consumed, 1)


if __name__ == "__main__":
    unittest.main()
