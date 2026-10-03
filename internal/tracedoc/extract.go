// Package tracedoc implements the agent-agnostic core of the ai-tracedoc
// plugin: transcript parsing, Q&A extraction and ledger management.
//
// It is a transcription of the original Python implementation (src/tracedoc)
// and stays deliberately close to it, including in its corner cases. The
// ledger is a shared artefact -- it lives in the project directory and is
// committed alongside the code -- so behaviour that looks accidental is
// reproduced on purpose rather than tidied up. Where the port knowingly
// diverges, the site says so and says why.
package tracedoc

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// Record is one JSONL line of a Claude Code session transcript. Unknown
// fields are ignored.
type Record struct {
	Type        string `json:"type"`
	SessionID   string `json:"sessionId"`
	Timestamp   string `json:"timestamp"`
	AiTitle     string `json:"aiTitle"`
	IsSidechain bool   `json:"isSidechain"`

	// AgentID is kept raw because membership, not value, is what matters:
	// the predicate is `"agentId" in record`, so a null or empty agentId
	// still marks the record as a sidechain. A decoded string could not
	// tell absent from null.
	AgentID json.RawMessage `json:"agentId"`

	Origin struct {
		Kind string `json:"kind"`
	} `json:"origin"`

	Message struct {
		// Content is either a string (a typed human message) or a list of
		// blocks (an assistant message), so it is decoded per use.
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// Entry is one recorded question with its answer. An unanswered question
// carries an empty A, which renders exactly as the original's None did.
type Entry struct {
	Q string
	A string
	T string // local time of the question, "" when the timestamp is unusable
}

// Session is one session's worth of extracted Q&A, in order of appearance.
type Session struct {
	SessionID string
	Title     string
	Date      string
	Entries   []Entry
}

type textBlock struct {
	Type string  `json:"type"`
	Text *string `json:"text"`
}

// IterRecordsFrom reads the JSONL records of path after startOffset.
//
// It returns (newOffset, records, ends), where ends[i] is the byte offset
// just past the line holding records[i], so a caller can advance by
// "consumed record count". newOffset advances only past successfully parsed
// lines: an unterminated trailing line that fails to parse is left for the
// next trigger, while a corrupt *complete* line is skipped and advanced so
// it can never wedge the pipeline.
func IterRecordsFrom(path string, startOffset int) (int, []Record, []int, error) {
	fh, err := os.Open(path)
	if err != nil {
		return startOffset, nil, nil, err
	}
	defer fh.Close()
	if _, err := fh.Seek(int64(startOffset), io.SeekStart); err != nil {
		return startOffset, nil, nil, err
	}

	var (
		records []Record
		ends    []int
		offset  = startOffset
	)
	reader := bufio.NewReader(fh)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			if rec, ok := parseRecord(line); ok {
				records = append(records, rec)
				offset += len(line)
				ends = append(ends, offset)
			} else if line[len(line)-1] == '\n' {
				offset += len(line) // corrupt complete line: skipped, advanced
			}
		}
		if readErr != nil {
			break // unterminated trailing line, or end of file
		}
	}
	return offset, records, ends, nil
}

// parseRecord mirrors json.loads(raw.decode("utf-8")). Go's json.Unmarshal
// accepts invalid UTF-8 by substituting U+FFFD where Python raises
// UnicodeDecodeError, so the encoding check has to be explicit to keep the
// two agreeing on what counts as a corrupt line.
func parseRecord(line []byte) (Record, bool) {
	if !utf8.Valid(line) {
		return Record{}, false
	}
	var rec Record
	if err := json.Unmarshal(line, &rec); err != nil {
		return Record{}, false
	}
	return rec, true
}

// questionText returns the content when it is a plain string, which is how a
// typed human message is stored. The pointer distinguishes an absent or null
// content from an empty one, matching isinstance(content, str).
func questionText(r *Record) (string, bool) {
	if len(r.Message.Content) == 0 {
		return "", false
	}
	var s *string
	if err := json.Unmarshal(r.Message.Content, &s); err != nil || s == nil {
		return "", false
	}
	return *s, true
}

// textOf joins the non-empty text blocks of an assistant record with a blank
// line between them, or returns "" when there are none. The original returned
// None in that case; "" renders identically everywhere it is used, and unlike
// None it cannot be confused with a missing answer downstream.
func textOf(r *Record) string {
	if len(r.Message.Content) == 0 {
		return ""
	}
	// Decoding element-wise rather than straight into a slice keeps a
	// non-object element from failing the whole message, which is what the
	// original's isinstance(block, dict) check did.
	var raw []json.RawMessage
	if err := json.Unmarshal(r.Message.Content, &raw); err != nil {
		return ""
	}
	var texts []string
	for _, item := range raw {
		var block textBlock
		if err := json.Unmarshal(item, &block); err != nil {
			continue
		}
		if block.Type != "text" || block.Text == nil {
			continue
		}
		if text := strings.TrimSpace(*block.Text); text != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n\n")
}

// isSidechain matches `bool(record.get("isSidechain")) or "agentId" in record`.
func isSidechain(r *Record) bool {
	return r.IsSidechain || r.AgentID != nil
}

// IsHumanQuestion reports whether the record is a typed human message.
func IsHumanQuestion(r *Record) bool {
	if r.Type != "user" || isSidechain(r) {
		return false
	}
	if r.Origin.Kind != "human" {
		return false
	}
	_, ok := questionText(r)
	return ok
}

// FormatLocalTime converts a transcript UTC timestamp to local time as
// "%Y-%m-%d %H:%M", returning "" for a missing or unparseable one.
//
// loc is injectable for tests and defaults to the system's local zone. A
// timestamp carrying no zone at all is read as local time, which is what
// Python's datetime.fromisoformat followed by astimezone() does; Go's
// RFC 3339 parser rejects that shape outright, so it is tried first and the
// naive layouts fall back to ParseInLocation.
func FormatLocalTime(timestamp string, loc *time.Location) string {
	if timestamp == "" {
		return ""
	}
	if loc == nil {
		loc = time.Local
	}
	if t, err := time.Parse(time.RFC3339Nano, timestamp); err == nil {
		return t.In(loc).Format("2006-01-02 15:04")
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.ParseInLocation(layout, timestamp, loc); err == nil {
			return t.Format("2006-01-02 15:04")
		}
	}
	return ""
}

// fallbackTitle is the first question, flattened to one line and truncated to
// 40 characters, used when a session has no ai-title record.
func fallbackTitle(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}
	first := strings.ReplaceAll(strings.TrimSpace(entries[0].Q), "\n", " ")
	if runes := []rune(first); len(runes) > 40 {
		return string(runes[:40]) + "…"
	}
	return first
}

type pendingChain struct {
	SID string
	Q   string
	A   string
	T   string
}

// ExtractSessions groups a record stream into per-session Q&A, returning the
// sessions in order of appearance and the number of records fully consumed.
// Callers advance their stored offset by exactly that many records.
//
// Chain completeness: a chain closed by the next human question is recorded
// immediately, but the trailing chain counts as complete only when its last
// assistant record carries text. With completeOnly (Stop mode) an incomplete
// trailing chain is neither extracted nor consumed, so it waits for the next
// trigger; without it (SessionEnd mode) the chain is recorded question-only
// and fully consumed.
func ExtractSessions(records []Record, loc *time.Location, completeOnly bool) ([]*Session, int) {
	sessionMap := map[string]*Session{}
	var order []string
	titles := map[string]string{}
	dates := map[string]string{}

	var (
		pending       *pendingChain
		lastAssistant *Record
		consumed      int
	)

	closeChain := func() {
		if pending == nil {
			return
		}
		if lastAssistant != nil {
			pending.A = textOf(lastAssistant)
		}
		session, seen := sessionMap[pending.SID]
		if !seen {
			session = &Session{SessionID: pending.SID}
			sessionMap[pending.SID] = session
			order = append(order, pending.SID)
		}
		session.Entries = append(session.Entries,
			Entry{Q: pending.Q, A: pending.A, T: pending.T})
	}

	for index, record := range records {
		kind := record.Type
		sid := record.SessionID
		switch {
		case kind == "ai-title":
			title := strings.TrimSpace(record.AiTitle)
			if title != "" && sid != "" {
				titles[sid] = title
			}
		case kind == "assistant" && !isSidechain(&record) &&
			pending != nil && sid == pending.SID:
			current := record
			lastAssistant = &current
		case IsHumanQuestion(&record):
			if pending != nil { // a new question closes the previous chain
				closeChain()
				consumed = index
			}
			if sid != "" {
				if _, seen := dates[sid]; !seen {
					dates[sid] = firstTen(record.Timestamp)
				}
			}
			question, _ := questionText(&record)
			pending = &pendingChain{
				SID: sid,
				Q:   question,
				T:   FormatLocalTime(record.Timestamp, loc),
			}
			lastAssistant = nil
		}
	}

	if pending != nil { // trailing chain
		lastHasText := lastAssistant != nil && textOf(lastAssistant) != ""
		if lastHasText || !completeOnly {
			closeChain()
			consumed = len(records)
		}
	}

	sessions := make([]*Session, 0, len(order))
	for _, sid := range order {
		session := sessionMap[sid]
		if title := titles[sid]; title != "" {
			session.Title = title
		} else {
			session.Title = fallbackTitle(session.Entries)
		}
		firstT := ""
		if len(session.Entries) > 0 {
			firstT = session.Entries[0].T
		}
		switch {
		case len(firstT) >= 10:
			session.Date = firstT[:10]
		case dates[sid] != "":
			session.Date = dates[sid]
		default:
			session.Date = time.Now().Format("2006-01-02")
		}
		sessions = append(sessions, session)
	}
	return sessions, consumed
}

// firstTen is Python's `(value or "")[:10]` on a byte string; timestamps are
// ASCII, so slicing bytes and slicing characters agree.
func firstTen(value string) string {
	if len(value) > 10 {
		return value[:10]
	}
	return value
}
