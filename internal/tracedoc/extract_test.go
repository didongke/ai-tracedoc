package tracedoc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Several fixtures deliberately use Chinese content: it simulates real users'
// questions and exercises the multibyte byte offsets the incremental reader
// depends on. Content is recorded verbatim in whatever language it was
// written.

const defaultTS = "2026-09-06T10:00:00.000Z"

var tz8 = time.FixedZone("UTC+8", 8*3600)

func mustMarshal(t *testing.T, obj any) []byte {
	t.Helper()
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return raw
}

// toRecord runs a fixture through the real parser, so the tests exercise the
// same decoding path production does rather than a parallel one.
func toRecord(t *testing.T, obj map[string]any) Record {
	t.Helper()
	raw := mustMarshal(t, obj)
	rec, ok := parseRecord(raw)
	if !ok {
		t.Fatalf("fixture did not parse as a record: %s", raw)
	}
	return rec
}

func records(t *testing.T, objs ...map[string]any) []Record {
	t.Helper()
	out := make([]Record, 0, len(objs))
	for _, obj := range objs {
		out = append(out, toRecord(t, obj))
	}
	return out
}

func human(question, sid, ts string) map[string]any {
	return map[string]any{
		"type":        "user",
		"origin":      map[string]any{"kind": "human"},
		"message":     map[string]any{"role": "user", "content": question},
		"isSidechain": false,
		"sessionId":   sid,
		"timestamp":   ts,
	}
}

func assistant(blocks []any, sid string) map[string]any {
	return map[string]any{
		"type":      "assistant",
		"message":   map[string]any{"role": "assistant", "content": blocks},
		"sessionId": sid,
	}
}

func textBlockJSON(text string) map[string]any {
	return map[string]any{"type": "text", "text": text}
}

func toolUseBlock(name string) map[string]any {
	return map[string]any{"type": "tool_use", "name": name, "id": "call_1",
		"input": map[string]any{}}
}

func toolResultUser(sid string) map[string]any {
	return map[string]any{
		"type": "user",
		"message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "call_1",
				"content": "ok"}}},
		"sessionId": sid,
	}
}

func jsonLines(t *testing.T, objs ...map[string]any) string {
	t.Helper()
	var sb strings.Builder
	for _, obj := range objs {
		sb.Write(mustMarshal(t, obj))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func writeFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func questionOf(t *testing.T, r *Record) string {
	t.Helper()
	question, ok := questionText(r)
	if !ok {
		t.Fatalf("record carries no string content")
	}
	return question
}

// --- IterRecordsFrom ----------------------------------------------------

func TestIterRecordsFromReadsCompleteLinesAndAdvancesOffset(t *testing.T) {
	path := writeFixture(t, jsonLines(t,
		human("q1", "s1", defaultTS), human("q2", "s1", defaultTS)))

	offset, recs, ends, err := IterRecordsFrom(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if len(recs) != 2 || len(ends) != 2 {
		t.Fatalf("got %d records, %d ends; want 2 and 2", len(recs), len(ends))
	}
	if offset != int(info.Size()) {
		t.Errorf("offset = %d, want %d", offset, info.Size())
	}
	if ends[len(ends)-1] != offset {
		t.Errorf("last end = %d, want %d", ends[len(ends)-1], offset)
	}
}

func TestIterRecordsFromIncompleteTailNotAdvanced(t *testing.T) {
	path := writeFixture(t,
		jsonLines(t, human("q1", "s1", defaultTS))+`{"type":"us`)

	offset, recs, ends, err := IterRecordsFrom(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if len(recs) != 1 || len(ends) != 1 {
		t.Fatalf("got %d records, %d ends; want 1 and 1", len(recs), len(ends))
	}
	// The partial line must be left for the next trigger, so the offset
	// stays behind the file size.
	if offset >= int(info.Size()) {
		t.Errorf("offset = %d, want < %d", offset, info.Size())
	}
}

func TestIterRecordsFromCorruptMidLineSkippedAndAdvanced(t *testing.T) {
	path := writeFixture(t,
		"not-json\n"+jsonLines(t, human("q1", "s1", defaultTS)))

	offset, recs, ends, err := IterRecordsFrom(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if offset != int(info.Size()) {
		t.Errorf("offset = %d; a corrupt complete line must still advance", offset)
	}
	if ends[0] != offset {
		t.Errorf("ends[0] = %d, want %d: corrupt lines get no ends entry",
			ends[0], offset)
	}
}

func TestIterRecordsFromStartOffsetSkipsAlreadyRead(t *testing.T) {
	content := jsonLines(t, human("q1", "s1", defaultTS), human("q2", "s1", defaultTS))
	path := writeFixture(t, content)
	firstEnd := strings.Index(content, "\n") + 1

	_, recs, ends, err := IterRecordsFrom(path, firstEnd)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if got := questionOf(t, &recs[0]); got != "q2" {
		t.Errorf("content = %q, want %q", got, "q2")
	}
	if ends[0] != int(info.Size()) {
		t.Errorf("ends[0] = %d, want %d", ends[0], info.Size())
	}
}

// --- IsHumanQuestion ----------------------------------------------------

func TestIsHumanQuestionTypedHumanQuestion(t *testing.T) {
	r := toRecord(t, human("你好", "s1", defaultTS))
	if !IsHumanQuestion(&r) {
		t.Error("a typed human question must be recognized")
	}
}

func TestIsHumanQuestionToolResultIsNotHuman(t *testing.T) {
	r := toRecord(t, toolResultUser("s1"))
	if IsHumanQuestion(&r) {
		t.Error("a tool result must not count as a human question")
	}
}

func TestIsHumanQuestionSidechainIsNotHuman(t *testing.T) {
	obj := human("你好", "s1", defaultTS)
	obj["isSidechain"] = true
	r := toRecord(t, obj)
	if IsHumanQuestion(&r) {
		t.Error("a sidechain record must not count as a human question")
	}
}

func TestIsHumanQuestionAgentRecordIsNotHuman(t *testing.T) {
	obj := human("你好", "s1", defaultTS)
	obj["agentId"] = "a123"
	r := toRecord(t, obj)
	if IsHumanQuestion(&r) {
		t.Error("a subagent record must not count as a human question")
	}
}

// The predicate is membership, not truthiness: an empty or null agentId still
// marks the record as a subagent's.
func TestIsHumanQuestionEmptyAgentIDIsNotHuman(t *testing.T) {
	for _, value := range []any{"", nil} {
		obj := human("你好", "s1", defaultTS)
		obj["agentId"] = value
		r := toRecord(t, obj)
		if IsHumanQuestion(&r) {
			t.Errorf("agentId=%#v must still mark the record as a sidechain", value)
		}
	}
}

func TestIsHumanQuestionMissingOriginIsNotHuman(t *testing.T) {
	obj := human("你好", "s1", defaultTS)
	delete(obj, "origin")
	r := toRecord(t, obj)
	if IsHumanQuestion(&r) {
		t.Error("a record with no origin must not count as a human question")
	}
}

func TestIsHumanQuestionNonUserType(t *testing.T) {
	r := toRecord(t, assistant([]any{textBlockJSON("hi")}, "s1"))
	if IsHumanQuestion(&r) {
		t.Error("an assistant record must not count as a human question")
	}
}

// --- FormatLocalTime ----------------------------------------------------

func TestFormatLocalTimeUTCZConvertedToGivenZone(t *testing.T) {
	got := FormatLocalTime("2026-09-06T10:00:00.000Z", tz8)
	if got != "2026-09-06 18:00" {
		t.Errorf("got %q, want %q", got, "2026-09-06 18:00")
	}
}

func TestFormatLocalTimeCrossesMidnight(t *testing.T) {
	got := FormatLocalTime("2026-09-06T23:30:00Z", tz8)
	if got != "2026-09-07 07:30" {
		t.Errorf("got %q, want %q", got, "2026-09-07 07:30")
	}
}

func TestFormatLocalTimeInvalidOrMissingReturnsEmpty(t *testing.T) {
	for _, input := range []string{"", "not-a-time"} {
		if got := FormatLocalTime(input, tz8); got != "" {
			t.Errorf("FormatLocalTime(%q) = %q, want empty", input, got)
		}
	}
}

// A naive timestamp carries no zone; Python reads it as local time, and the
// port has to agree rather than reject it.
func TestFormatLocalTimeNaiveTimestampReadAsLocal(t *testing.T) {
	naive := "2026-09-06T10:00:00"
	got := FormatLocalTime(naive, tz8)
	if got != "2026-09-06 10:00" {
		t.Errorf("got %q, want %q", got, "2026-09-06 10:00")
	}
}

// --- ExtractSessions ----------------------------------------------------

func TestExtractSessionsBasicQA(t *testing.T) {
	recs := records(t,
		human("怎么设计缓存？", "s1", defaultTS),
		assistant([]any{map[string]any{"type": "thinking", "thinking": "内部思考"},
			textBlockJSON("建议用 LRU。")}, "s1"))

	sessions, consumed := ExtractSessions(recs, tz8, true)
	want := []Entry{{Q: "怎么设计缓存？", A: "建议用 LRU。", T: "2026-09-06 18:00"}}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if !reflect.DeepEqual(sessions[0].Entries, want) {
		t.Errorf("entries = %#v, want %#v", sessions[0].Entries, want)
	}
	if consumed != len(recs) {
		t.Errorf("consumed = %d, want %d", consumed, len(recs))
	}
}

func TestExtractSessionsAnswerTakesLastAssistantOnly(t *testing.T) {
	recs := records(t,
		human("q", "s1", defaultTS),
		assistant([]any{textBlockJSON("Let me check first."), toolUseBlock("Read")}, "s1"),
		toolResultUser("s1"),
		assistant([]any{textBlockJSON("Final answer here.")}, "s1"))

	sessions, consumed := ExtractSessions(recs, tz8, true)
	if got := sessions[0].Entries[0].A; got != "Final answer here." {
		t.Errorf("answer = %q, want %q", got, "Final answer here.")
	}
	if consumed != len(recs) {
		t.Errorf("consumed = %d, want %d", consumed, len(recs))
	}
}

func TestExtractSessionsAnswerEmptyWhenEndsWithToolUse(t *testing.T) {
	recs := records(t,
		human("q", "s1", defaultTS),
		assistant([]any{textBlockJSON("Let me check first."), toolUseBlock("Read")}, "s1"),
		toolResultUser("s1"),
		assistant([]any{toolUseBlock("Edit")}, "s1"))

	sessions, consumed := ExtractSessions(recs, tz8, false)
	if got := sessions[0].Entries[0].A; got != "" {
		t.Errorf("answer = %q, want unanswered", got)
	}
	if consumed != len(recs) {
		t.Errorf("consumed = %d, want %d", consumed, len(recs))
	}
}

func TestExtractSessionsMultipleTextBlocksJoined(t *testing.T) {
	recs := records(t,
		human("q", "s1", defaultTS),
		assistant([]any{textBlockJSON("Part one."), textBlockJSON("Part two.")}, "s1"))

	sessions, consumed := ExtractSessions(recs, tz8, true)
	if got := sessions[0].Entries[0].A; got != "Part one.\n\nPart two." {
		t.Errorf("answer = %q, want the blocks joined by a blank line", got)
	}
	if consumed != len(recs) {
		t.Errorf("consumed = %d, want %d", consumed, len(recs))
	}
}

func TestExtractSessionsConsecutiveQuestionsEachGetEntry(t *testing.T) {
	recs := records(t,
		human("q1", "s1", defaultTS),
		assistant([]any{textBlockJSON("a1")}, "s1"),
		human("q2", "s1", defaultTS))

	sessions, consumed := ExtractSessions(recs, tz8, false)
	questions := []string{sessions[0].Entries[0].Q, sessions[0].Entries[1].Q}
	if !reflect.DeepEqual(questions, []string{"q1", "q2"}) {
		t.Errorf("questions = %v, want [q1 q2]", questions)
	}
	if got := sessions[0].Entries[1].A; got != "" {
		t.Errorf("second answer = %q, want unanswered", got)
	}
	if got := sessions[0].Entries[1].T; got != "2026-09-06 18:00" {
		t.Errorf("second time = %q, want %q", got, "2026-09-06 18:00")
	}
	if consumed != 3 {
		t.Errorf("consumed = %d, want 3", consumed)
	}
}

func TestExtractSessionsSidechainAssistantIgnored(t *testing.T) {
	side := assistant([]any{textBlockJSON("子代理的话")}, "s1")
	side["isSidechain"] = true
	recs := records(t,
		human("q", "s1", defaultTS),
		side,
		assistant([]any{textBlockJSON("真正的回答。")}, "s1"))

	sessions, consumed := ExtractSessions(recs, tz8, true)
	if got := sessions[0].Entries[0].A; got != "真正的回答。" {
		t.Errorf("answer = %q, want the non-sidechain assistant's text", got)
	}
	if consumed != len(recs) {
		t.Errorf("consumed = %d, want %d", consumed, len(recs))
	}
}

func TestExtractSessionsTitleLastAiTitleWins(t *testing.T) {
	recs := records(t,
		map[string]any{"type": "ai-title", "aiTitle": "旧标题", "sessionId": "s1"},
		human("q", "s1", defaultTS),
		assistant([]any{textBlockJSON("a")}, "s1"),
		map[string]any{"type": "ai-title", "aiTitle": "新标题", "sessionId": "s1"})

	sessions, consumed := ExtractSessions(recs, tz8, true)
	if sessions[0].Title != "新标题" {
		t.Errorf("title = %q, want %q", sessions[0].Title, "新标题")
	}
	if consumed != len(recs) {
		t.Errorf("consumed = %d, want %d", consumed, len(recs))
	}
}

func TestExtractSessionsTitleFallbackToFirstQuestion(t *testing.T) {
	recs := records(t,
		human("这是一段超过四十个字符的长问题一二三四五六七八九十甲乙丙丁", "s1", defaultTS),
		assistant([]any{textBlockJSON("a")}, "s1"))

	sessions, consumed := ExtractSessions(recs, tz8, true)
	title := sessions[0].Title
	if count := len([]rune(title)); count > 41 {
		t.Errorf("title is %d characters, want at most 41", count)
	}
	if !strings.HasPrefix(title, "这是一段超过四十") {
		t.Errorf("title = %q, want it to start with the question", title)
	}
	if consumed != len(recs) {
		t.Errorf("consumed = %d, want %d", consumed, len(recs))
	}
}

func TestExtractSessionsDateFromFirstQuestion(t *testing.T) {
	recs := records(t,
		human("q", "s1", defaultTS),
		assistant([]any{textBlockJSON("a")}, "s1"))

	sessions, consumed := ExtractSessions(recs, tz8, true)
	if sessions[0].Date != "2026-09-06" {
		t.Errorf("date = %q, want %q", sessions[0].Date, "2026-09-06")
	}
	if consumed != len(recs) {
		t.Errorf("consumed = %d, want %d", consumed, len(recs))
	}
}

// The session header date uses the LOCAL date, even when local time has
// crossed midnight relative to the UTC timestamp.
func TestExtractSessionsDateFollowsLocalTime(t *testing.T) {
	recs := records(t,
		human("q", "s1", "2026-09-06T23:30:00Z"),
		assistant([]any{textBlockJSON("a")}, "s1"))

	sessions, consumed := ExtractSessions(recs, tz8, true)
	if sessions[0].Date != "2026-09-07" {
		t.Errorf("date = %q, want %q", sessions[0].Date, "2026-09-07")
	}
	if consumed != len(recs) {
		t.Errorf("consumed = %d, want %d", consumed, len(recs))
	}
}

func TestExtractSessionsMissingTimestampYieldsEmptyTime(t *testing.T) {
	obj := human("q", "s1", defaultTS)
	delete(obj, "timestamp")
	recs := records(t, obj, assistant([]any{textBlockJSON("a")}, "s1"))

	sessions, consumed := ExtractSessions(recs, tz8, true)
	if got := sessions[0].Entries[0].T; got != "" {
		t.Errorf("time = %q, want empty", got)
	}
	if consumed != 2 {
		t.Errorf("consumed = %d, want 2", consumed)
	}
}

func TestExtractSessionsMultipleSessionsGroupedInOrder(t *testing.T) {
	recs := records(t,
		human("q1", "s1", defaultTS), assistant([]any{textBlockJSON("a1")}, "s1"),
		human("q2", "s2", defaultTS), assistant([]any{textBlockJSON("a2")}, "s2"))

	sessions, consumed := ExtractSessions(recs, tz8, true)
	ids := []string{sessions[0].SessionID, sessions[1].SessionID}
	if !reflect.DeepEqual(ids, []string{"s1", "s2"}) {
		t.Errorf("session ids = %v, want [s1 s2]", ids)
	}
	if consumed != len(recs) {
		t.Errorf("consumed = %d, want %d", consumed, len(recs))
	}
}

func TestExtractSessionsIncompleteFinalChainExcludedByDefault(t *testing.T) {
	recs := records(t,
		human("q1", "s1", defaultTS),
		assistant([]any{textBlockJSON("a1")}, "s1"),
		human("q2", "s1", defaultTS),
		assistant([]any{toolUseBlock("Edit")}, "s1"))

	sessions, consumed := ExtractSessions(recs, tz8, true)
	if len(sessions[0].Entries) != 1 || sessions[0].Entries[0].Q != "q1" {
		t.Errorf("entries = %#v, want only q1", sessions[0].Entries)
	}
	// q1's chain is the first two records; q2's is neither extracted nor
	// consumed, so it waits for the next trigger.
	if consumed != 2 {
		t.Errorf("consumed = %d, want 2", consumed)
	}
}

func TestExtractSessionsQuestionWithoutAssistantExcludedByDefault(t *testing.T) {
	recs := records(t, human("q", "s1", defaultTS))

	sessions, consumed := ExtractSessions(recs, tz8, true)
	if len(sessions) != 0 {
		t.Errorf("got %d sessions, want none", len(sessions))
	}
	if consumed != 0 {
		t.Errorf("consumed = %d, want 0", consumed)
	}
}

func TestExtractSessionsCompleteOnlyFalseConsumesQuestionOnly(t *testing.T) {
	recs := records(t, human("q", "s1", defaultTS))

	sessions, consumed := ExtractSessions(recs, tz8, false)
	if sessions[0].Entries[0].Q != "q" {
		t.Errorf("question = %q, want %q", sessions[0].Entries[0].Q, "q")
	}
	if got := sessions[0].Entries[0].A; got != "" {
		t.Errorf("answer = %q, want unanswered", got)
	}
	if consumed != 1 {
		t.Errorf("consumed = %d, want 1", consumed)
	}
}
