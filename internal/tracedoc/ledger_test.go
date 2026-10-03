package tracedoc

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	project = "proj-x"
	today   = "20260906"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// --- volume naming ------------------------------------------------------

func TestVolumeNumber(t *testing.T) {
	cases := []struct {
		filename string
		want     int
	}{
		{"20260906-proj-x-tracedoc.md", 1},
		{"20260906-proj-x-tracedoc-02.md", 2},
		{"20260906-proj-x-tracedoc-13.md", 13},
	}
	for _, tc := range cases {
		if got := VolumeNumber(tc.filename); got != tc.want {
			t.Errorf("VolumeNumber(%q) = %d, want %d", tc.filename, got, tc.want)
		}
	}
}

func TestVolumeFilename(t *testing.T) {
	if got := VolumeFilename(project, today, 1); got != "20260906-proj-x-tracedoc.md" {
		t.Errorf("volume 1 = %q, want no suffix", got)
	}
	if got := VolumeFilename(project, today, 2); got != "20260906-proj-x-tracedoc-02.md" {
		t.Errorf("volume 2 = %q, want a two-digit suffix", got)
	}
}

// --- volume creation ----------------------------------------------------

func TestCreateVolumeFirstVolumeHeader(t *testing.T) {
	cwd := t.TempDir()
	name, err := CreateVolume(cwd, project, today, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if name != "20260906-proj-x-tracedoc.md" {
		t.Errorf("name = %q", name)
	}
	content := readFile(t, filepath.Join(cwd, name))
	if !strings.Contains(content, "# proj-x · TraceDoc") {
		t.Error("the header must name the project")
	}
	if !strings.Contains(content, "ai-tracedoc") {
		t.Error("the header must carry the auto-generated note")
	}
}

func TestCreateVolumeLaterVolumeLinksBack(t *testing.T) {
	cwd := t.TempDir()
	name, err := CreateVolume(cwd, project, today,
		"20260906-proj-x-tracedoc.md", 2)
	if err != nil {
		t.Fatal(err)
	}
	content := readFile(t, filepath.Join(cwd, name))
	if !strings.Contains(content, "Volume 2") {
		t.Error("a later volume must be labelled with its number")
	}
	if !strings.Contains(content,
		"Continued from: [20260906-proj-x-tracedoc.md]") {
		t.Error("a later volume must link back to the one it continues")
	}
}

// --- locating the current volume ----------------------------------------

func TestLocateLatestVolumeNoneWhenNoVolume(t *testing.T) {
	cwd := t.TempDir()
	got, err := LocateLatestVolume(cwd, project, &State{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("got %q, want no volume", got)
	}
}

func TestLocateLatestVolumeStatePointerWins(t *testing.T) {
	cwd := t.TempDir()
	if _, err := CreateVolume(cwd, project, today, "", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateVolume(cwd, project, today, "", 2); err != nil {
		t.Fatal(err)
	}
	state := &State{CurrentVolume: "20260906-proj-x-tracedoc.md"}
	got, err := LocateLatestVolume(cwd, project, state)
	if err != nil {
		t.Fatal(err)
	}
	if got != "20260906-proj-x-tracedoc.md" {
		t.Errorf("got %q, want the state file's pointer", got)
	}
}

func TestLocateLatestVolumeGlobPicksHighestNumber(t *testing.T) {
	cwd := t.TempDir()
	if _, err := CreateVolume(cwd, project, today, "", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateVolume(cwd, project, today, "", 2); err != nil {
		t.Fatal(err)
	}
	got, err := LocateLatestVolume(cwd, project, &State{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "20260906-proj-x-tracedoc-02.md" {
		t.Errorf("got %q, want the highest-numbered volume", got)
	}
}

// Two files both claiming volume 1 must be reported, not guessed between:
// appending a session to the wrong one is not recoverable afterwards.
func TestLocateLatestVolumeAmbiguousRaises(t *testing.T) {
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "20260906-proj-x-tracedoc.md"), "x")
	writeFile(t, filepath.Join(cwd, "20990101-proj-x-tracedoc.md"), "x")

	_, err := LocateLatestVolume(cwd, project, &State{})
	var ledgerErr *LedgerError
	if !errors.As(err, &ledgerErr) {
		t.Fatalf("err = %v, want a LedgerError", err)
	}
}

// --- formatters ---------------------------------------------------------

func TestFormatSessionHeader(t *testing.T) {
	text := FormatSessionHeader("2026-09-06", "Title", "s1")
	if !strings.Contains(text, "## 2026-09-06 · Title") {
		t.Error("the header must carry the date and title")
	}
	if !strings.Contains(text, "<!-- session: s1 -->") {
		t.Error("the header must carry the session marker")
	}
}

func TestFormatEntriesWithAndWithoutAnswer(t *testing.T) {
	text := FormatEntries([]Entry{
		{Q: "q1", A: "a1", T: "2026-09-06 18:00"},
		{Q: "q2"},
	})
	if !strings.Contains(text, "**Question:** 2026-09-06 18:00 · q1") {
		t.Error("a timestamped question must carry its local time")
	}
	if !strings.Contains(text, "**Answer:** a1") {
		t.Error("the answered question must carry its answer")
	}
	if !strings.Contains(text, "**Question:** q2") {
		t.Error("a question with no timestamp must have no time prefix")
	}
	if got := strings.Count(text, "**Answer:**"); got != 1 {
		t.Errorf("answer markers = %d, want 1", got)
	}
}

// The blank lines around each entry fall out of joining a parts list that
// carries an empty terminator per entry, so they are pinned here rather than
// left looking like incidental whitespace. The expected value below is the
// reference implementation's own output, not a reading of it: an intuitive
// "one blank line between blocks" transcription is short by one newline at
// every boundary.
func TestFormatEntriesBlankLineLayout(t *testing.T) {
	text := FormatEntries([]Entry{
		{Q: "q1", A: "a1", T: "2026-09-06 18:00"},
		{Q: "q2"},
	})
	// Written as the parts the implementation joins, because that is what
	// the layout actually is; a flattened literal is easy to get wrong by
	// exactly the newline the test exists to protect.
	want := strings.Join([]string{
		"**Question:** 2026-09-06 18:00 · q1\n",
		"\n**Answer:** a1\n",
		"",
		"**Question:** q2\n",
		"",
	}, "\n")
	if text != want {
		t.Errorf("layout drifted:\n got %q\nwant %q", text, want)
	}
}

// --- state file ---------------------------------------------------------

func TestStateFileRoundtrip(t *testing.T) {
	cwd := t.TempDir()
	state := &State{
		CurrentVolume: "v.md",
		Sessions:      map[string]*SessionState{"s1": {Offset: 5, Volume: "v.md"}},
	}
	if err := SaveState(cwd, state); err != nil {
		t.Fatal(err)
	}
	loaded := LoadState(cwd)
	if !reflect.DeepEqual(loaded, state) {
		t.Errorf("loaded = %#v, want %#v", loaded, state)
	}
}

func TestStateFileMissingOrCorruptReturnsFresh(t *testing.T) {
	cwd := t.TempDir()
	if got := LoadState(cwd); got.CurrentVolume != "" || len(got.Sessions) != 0 {
		t.Errorf("a missing state file must load as empty, got %#v", got)
	}
	writeFile(t, filepath.Join(cwd, StateFilename), "not-json")
	if got := LoadState(cwd); got.CurrentVolume != "" || len(got.Sessions) != 0 {
		t.Errorf("a corrupt state file must load as empty, got %#v", got)
	}
}

// --- locking ------------------------------------------------------------

// Design doc §6: the critical section is serialized by the project lock.
func TestProjectLockBlocksConcurrentHolder(t *testing.T) {
	cwd := t.TempDir()
	acquired := make(chan struct{}, 1)

	err := ProjectLock(cwd, func() error {
		go func() {
			_ = ProjectLock(cwd, func() error {
				acquired <- struct{}{}
				return nil
			})
		}()
		select {
		case <-acquired:
			t.Error("the lock must block while another holder has it")
		case <-time.After(300 * time.Millisecond):
			// still blocked, which is the point
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-acquired:
		// released on exit, as required
	case <-time.After(5 * time.Second):
		t.Error("the lock was never released")
	}
}

func TestProjectLockReacquirableAfterExit(t *testing.T) {
	cwd := t.TempDir()
	if err := ProjectLock(cwd, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := ProjectLock(cwd, func() error { return nil }); err != nil {
		t.Fatalf("the lock must be re-acquirable after release: %v", err)
	}
}

// --- appending ----------------------------------------------------------

func TestAppendTextAppendsAndPreservesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.md")
	writeFile(t, path, "existing\n")
	if err := appendText(path, "new\n"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "existing\nnew\n" {
		t.Errorf("content = %q, want %q", got, "existing\nnew\n")
	}
}

// append_session requires the caller to hold the lock; every case below runs
// inside ProjectLock.

func sessionFixture(sid string, questions []string, date, title string) *Session {
	session := &Session{SessionID: sid, Title: title, Date: date}
	for _, q := range questions {
		session.Entries = append(session.Entries,
			Entry{Q: q, A: "Answer " + q})
	}
	return session
}

func TestAppendSessionNewSessionCreatesVolume(t *testing.T) {
	cwd := t.TempDir()
	state := &State{Sessions: map[string]*SessionState{}}
	err := ProjectLock(cwd, func() error {
		return AppendSession(cwd, project,
			sessionFixture("s1", []string{"q1"}, "2026-09-06", "Title"),
			state, today, VolumeThresholdBytes)
	})
	if err != nil {
		t.Fatal(err)
	}
	content := readFile(t, filepath.Join(cwd, "20260906-proj-x-tracedoc.md"))
	for _, want := range []string{
		"## 2026-09-06 · Title", "**Question:** q1", "**Answer:** Answer q1",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("ledger is missing %q", want)
		}
	}
	if state.CurrentVolume != "20260906-proj-x-tracedoc.md" {
		t.Errorf("current volume = %q", state.CurrentVolume)
	}
}

func TestAppendSessionSecondSessionAppendsSameVolume(t *testing.T) {
	cwd := t.TempDir()
	state := &State{Sessions: map[string]*SessionState{}}
	err := ProjectLock(cwd, func() error {
		if err := AppendSession(cwd, project,
			sessionFixture("s1", []string{"q1"}, "2026-09-06", "Title"),
			state, today, VolumeThresholdBytes); err != nil {
			return err
		}
		return AppendSession(cwd, project,
			sessionFixture("s2", []string{"q2"}, "2026-09-06", "Title"),
			state, today, VolumeThresholdBytes)
	})
	if err != nil {
		t.Fatal(err)
	}
	content := readFile(t, filepath.Join(cwd, "20260906-proj-x-tracedoc.md"))
	if !strings.Contains(content, "q1") || !strings.Contains(content, "q2") {
		t.Error("both sessions must land in the same volume")
	}
	if got := strings.Count(content, "<!-- session: "); got != 2 {
		t.Errorf("session headers = %d, want 2", got)
	}
}

func TestAppendSessionKnownSessionAppendsEntriesOnly(t *testing.T) {
	cwd := t.TempDir()
	state := &State{Sessions: map[string]*SessionState{}}
	err := ProjectLock(cwd, func() error {
		if err := AppendSession(cwd, project,
			sessionFixture("s1", []string{"q1"}, "2026-09-06", "Title"),
			state, today, VolumeThresholdBytes); err != nil {
			return err
		}
		return AppendSession(cwd, project,
			sessionFixture("s1", []string{"q2"}, "2026-09-06", "Title"),
			state, today, VolumeThresholdBytes)
	})
	if err != nil {
		t.Fatal(err)
	}
	content := readFile(t, filepath.Join(cwd, "20260906-proj-x-tracedoc.md"))
	if got := strings.Count(content, "<!-- session: s1 -->"); got != 1 {
		t.Errorf("session headers = %d, want 1: a continuation adds no header", got)
	}
	if !strings.Contains(content, "**Question:** q2") {
		t.Error("the new entry must be appended")
	}
}

func TestAppendSessionOverflowCreatesSecondVolumeWithLinks(t *testing.T) {
	cwd := t.TempDir()
	state := &State{Sessions: map[string]*SessionState{}}
	err := ProjectLock(cwd, func() error {
		// threshold forced to 1 byte to trigger the split
		if err := AppendSession(cwd, project,
			sessionFixture("s1", []string{"q1"}, "2026-09-06", "Title"),
			state, today, 1); err != nil {
			return err
		}
		return AppendSession(cwd, project,
			sessionFixture("s2", []string{"q2"}, "2026-09-06", "Title"),
			state, today, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	vol1 := readFile(t, filepath.Join(cwd, "20260906-proj-x-tracedoc.md"))
	vol2 := readFile(t, filepath.Join(cwd, "20260906-proj-x-tracedoc-02.md"))
	if !strings.Contains(vol1, "q1") || strings.Contains(vol1, "q2") {
		t.Error("volume 1 must hold only the first session")
	}
	if !strings.Contains(vol1, "Continued in: [20260906-proj-x-tracedoc-02.md]") {
		t.Error("volume 1 must point forward")
	}
	if !strings.Contains(vol2, "Continued from: [20260906-proj-x-tracedoc.md]") {
		t.Error("volume 2 must point back")
	}
	if !strings.Contains(vol2, "q2") {
		t.Error("volume 2 must hold the second session")
	}
	if state.CurrentVolume != "20260906-proj-x-tracedoc-02.md" {
		t.Errorf("current volume = %q, want the new one", state.CurrentVolume)
	}
}

func TestAppendSessionKnownSessionContinuationNeverSplits(t *testing.T) {
	cwd := t.TempDir()
	state := &State{Sessions: map[string]*SessionState{}}
	err := ProjectLock(cwd, func() error {
		if err := AppendSession(cwd, project,
			sessionFixture("s1", []string{"q1"}, "2026-09-06", "Title"),
			state, today, VolumeThresholdBytes); err != nil {
			return err
		}
		// Volume 1 is now over the threshold, but s1 is a known session, so
		// it keeps writing volume 1 rather than splitting mid-session.
		return AppendSession(cwd, project,
			sessionFixture("s1", []string{"q2"}, "2026-09-06", "Title"),
			state, today, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "20260906-proj-x-tracedoc-02.md")); err == nil {
		t.Error("a continuation must never start a new volume")
	}
	if content := readFile(t, filepath.Join(cwd, "20260906-proj-x-tracedoc.md")); !strings.Contains(content, "q2") {
		t.Error("the continuation must land in volume 1")
	}
}

func TestAppendSessionOverflowUsesTodayForNewVolume(t *testing.T) {
	cwd := t.TempDir()
	state := &State{Sessions: map[string]*SessionState{}}
	err := ProjectLock(cwd, func() error {
		if err := AppendSession(cwd, project,
			sessionFixture("s1", []string{"q1"}, "2026-09-06", "Title"),
			state, "20260101", 1); err != nil {
			return err
		}
		return AppendSession(cwd, project,
			sessionFixture("s2", []string{"q2"}, "2026-09-06", "Title"),
			state, "20260315", 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "20260315-proj-x-tracedoc-02.md")); err != nil {
		t.Errorf("a new volume takes the current date: %v", err)
	}
}

// Design doc §5: after the ledger is deleted, the next session recreates it.
func TestAppendSessionRecreatesAfterDeletion(t *testing.T) {
	cwd := t.TempDir()
	state := &State{Sessions: map[string]*SessionState{}}
	err := ProjectLock(cwd, func() error {
		if err := AppendSession(cwd, project,
			sessionFixture("s1", []string{"q1"}, "2026-09-06", "Title"),
			state, today, VolumeThresholdBytes); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(cwd, "20260906-proj-x-tracedoc.md")); err != nil {
			return err
		}
		return AppendSession(cwd, project,
			sessionFixture("s2", []string{"q2"}, "2026-09-06", "Title"),
			state, today, VolumeThresholdBytes)
	})
	if err != nil {
		t.Fatal(err)
	}
	content := readFile(t, filepath.Join(cwd, "20260906-proj-x-tracedoc.md"))
	if !strings.Contains(content, "q2") {
		t.Error("the recreated ledger must hold the new session")
	}
	if strings.Contains(content, "q1") {
		t.Error("the deleted session's entries must not reappear")
	}
}

// Historically corrupt state -- a known session pointing at no volume --
// falls back to the new-session path rather than appending to "".
func TestAppendSessionKnownSessionWithEmptyVolumeReregisters(t *testing.T) {
	cwd := t.TempDir()
	state := &State{Sessions: map[string]*SessionState{"s1": {Offset: 5, Volume: ""}}}
	err := ProjectLock(cwd, func() error {
		return AppendSession(cwd, project,
			sessionFixture("s1", []string{"q1"}, "2026-09-06", "Title"),
			state, today, VolumeThresholdBytes)
	})
	if err != nil {
		t.Fatal(err)
	}
	content := readFile(t, filepath.Join(cwd, "20260906-proj-x-tracedoc.md"))
	if !strings.Contains(content, "<!-- session: s1 -->") {
		t.Error("the session must be re-registered with a real volume")
	}
	if !strings.Contains(content, "**Question:** q1") {
		t.Error("its entry must be written")
	}
	if got := state.Sessions["s1"].Volume; got != "20260906-proj-x-tracedoc.md" {
		t.Errorf("volume = %q, want a real filename", got)
	}
}

// The whole ledger, pinned as a literal.
//
// This is the output the original Python implementation produced for this
// transcript, established by byte-level comparison before that code was
// removed. Keeping it as a literal is now the only thing standing between the
// format and a well-meaning tidying-up: it is a contract with the ledgers
// already on disk, and drift forks them silently.
func TestLedgerGoldenOutput(t *testing.T) {
	recs := records(t,
		map[string]any{"type": "ai-title", "aiTitle": "缓存设计讨论",
			"sessionId": "s1"},
		human("怎么设计缓存？", "s1", "2026-09-15T10:00:00.000Z"),
		assistant([]any{textBlockJSON("建议用 LRU。")}, "s1"),
		human("为什么不用 LFU？", "s1", "2026-09-15T10:05:00.000Z"),
		assistant([]any{toolUseBlock("Read")}, "s1"),
	)
	sessions, _ := ExtractSessions(recs, tz8, false)
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}

	cwd := t.TempDir()
	state := &State{Sessions: map[string]*SessionState{}}
	err := ProjectLock(cwd, func() error {
		return AppendSession(cwd, "proj", sessions[0], state,
			"20260915", VolumeThresholdBytes)
	})
	if err != nil {
		t.Fatal(err)
	}

	want := `# proj · TraceDoc

This file is auto-generated by the ai-tracedoc plugin from Claude Code session transcripts. It has not been manually curated.


## 2026-09-15 · 缓存设计讨论

<!-- session: s1 -->

**Question:** 2026-09-15 18:00 · 怎么设计缓存？


**Answer:** 建议用 LRU。


**Question:** 2026-09-15 18:05 · 为什么不用 LFU？

`
	got := readFile(t, filepath.Join(cwd, "20260915-proj-tracedoc.md"))
	if got != want {
		t.Errorf("the ledger format drifted:\n--- got ---\n%s\n--- want ---\n%s",
			got, want)
	}
}
