package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These exercise the adapter end to end, by running the real binary the way
// the hook does. Chinese fixture content simulates real users and checks that
// content is recorded verbatim.

const repoRoot = "../.."

var binaryPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tracedoc-bin")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	binaryPath = filepath.Join(dir, "tracedoc")
	if runtime.GOOS == "windows" {
		binaryPath += ".exe"
	}
	build := exec.Command("go", "build", "-o", binaryPath, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func humanRec(question, sid string) map[string]any {
	return map[string]any{
		"type": "user", "origin": map[string]any{"kind": "human"},
		"message":     map[string]any{"role": "user", "content": question},
		"isSidechain": false, "sessionId": sid,
		"timestamp": "2026-09-06T10:00:00.000Z",
	}
}

func assistantRec(text, sid string) map[string]any {
	return map[string]any{
		"type": "assistant",
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": text}}},
		"sessionId": sid,
	}
}

func toolUseRec(name, sid string) map[string]any {
	return map[string]any{
		"type": "assistant",
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "name": name, "id": "call_1",
				"input": map[string]any{}}}},
		"sessionId": sid,
	}
}

func writeTranscript(t *testing.T, path string, records ...map[string]any) {
	t.Helper()
	var sb strings.Builder
	for _, record := range records {
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(raw)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTranscript(t *testing.T, path string, records ...map[string]any) {
	t.Helper()
	var sb strings.Builder
	for _, record := range records {
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(raw)
		sb.WriteByte('\n')
	}
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	if _, err := fh.WriteString(sb.String()); err != nil {
		t.Fatal(err)
	}
}

// adapter is one temp project with its transcript, mirroring the original
// test fixture: an ai-title first, as real transcripts carry one.
type adapter struct {
	dir        string
	transcript string
}

func newAdapter(t *testing.T) *adapter {
	t.Helper()
	return newAdapterIn(t, t.TempDir())
}

func newAdapterIn(t *testing.T, dir string) *adapter {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(dir, "session.jsonl")
	a := &adapter{dir: dir, transcript: transcript}
	writeStandardTranscript(t, transcript)
	return a
}

// writeStandardTranscript is the shared fixture: an ai-title first, as real
// transcripts carry one.
func writeStandardTranscript(t *testing.T, path string) {
	t.Helper()
	writeTranscript(t, path,
		map[string]any{"type": "ai-title", "aiTitle": "缓存设计讨论",
			"sessionId": "s1"},
		humanRec("怎么设计缓存？", "s1"),
		assistantRec("建议用 LRU。", "s1"),
		humanRec("为什么不用 LFU？", "s1"),
		toolUseRec("Read", "s1"),
	)
}

func (a *adapter) enable(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(a.dir, ".tracedoc-on"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (a *adapter) ledgerPath(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "tracedoc.md") {
			return filepath.Join(a.dir, entry.Name())
		}
	}
	t.Fatalf("no ledger in %s", a.dir)
	return ""
}

func (a *adapter) ledger(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(a.ledgerPath(t))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func (a *adapter) hasLedger() bool {
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "tracedoc.md") {
			return true
		}
	}
	return false
}

func runHook(t *testing.T, a *adapter, sessionID, event string) (string, string, int) {
	t.Helper()
	return runHookBinary(t, binaryPath, a, sessionID, event)
}

// hookEnv is the environment a hook runs in: the test process's own, with
// CLAUDE_PROJECT_DIR pointing at the project.
//
// Claude Code always sets that variable for a hook, so setting it here is what
// the real invocation looks like. Any inherited value is dropped rather than
// shadowed, because a process environment with a duplicate key resolves
// differently across platforms and libcs.
func hookEnv(projectDir string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "CLAUDE_PROJECT_DIR=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "CLAUDE_PROJECT_DIR="+projectDir)
}

func runHookBinary(t *testing.T, binary string, a *adapter,
	sessionID, event string) (string, string, int) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"session_id": sessionID, "transcript_path": a.transcript,
		"cwd": a.dir, "reason": "other", "hook_event_name": event,
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary)
	cmd.Env = hookEnv(a.dir)
	cmd.Stdin = strings.NewReader(string(payload))
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run hook: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

func runSelfTest(t *testing.T, transcript string) (string, string, int) {
	t.Helper()
	return runSelfTestWith(t, binaryPath, transcript)
}

func runSelfTestWith(t *testing.T, binary, transcript string) (string, string, int) {
	t.Helper()
	return runBinaryWith(t, binary, "--self-test", transcript)
}

// runBinaryWith runs one binary with the given arguments and returns what it
// said.
//
// It exists so that a comparison between two binaries names its invocations in
// one place: every probe added here is automatically run against both sides,
// rather than against whichever one a hand-written exec.Command happened to
// reach.
func runBinaryWith(t *testing.T, binary string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %s %v: %v", filepath.Base(binary), args, err)
		}
		code = exitErr.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

// --- --self-test --------------------------------------------------------

func TestSelfTestPrintsEntriesWithoutWriting(t *testing.T) {
	a := newAdapter(t)
	stdout, stderr, code := runSelfTest(t, a.transcript)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	for _, want := range []string{"怎么设计缓存？", "建议用 LRU。", "为什么不用 LFU？"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output is missing %q", want)
		}
	}
	if strings.Contains(stdout, "Read") {
		t.Error("tool calls must not be recorded")
	}
	if a.hasLedger() {
		t.Error("--self-test must not write a ledger")
	}
}

func TestSelfTestUsageError(t *testing.T) {
	cmd := exec.Command(binaryPath, "--self-test")
	if err := cmd.Run(); err == nil {
		t.Error("a missing transcript argument must be a usage error")
	}
}

// --- hook runs ----------------------------------------------------------

func TestHookCreatesLedger(t *testing.T) {
	a := newAdapter(t)
	a.enable(t)
	_, stderr, code := runHook(t, a, "s1", "SessionEnd")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	content := a.ledger(t)
	for _, want := range []string{"怎么设计缓存？", "建议用 LRU。", "为什么不用 LFU？"} {
		if !strings.Contains(content, want) {
			t.Errorf("ledger is missing %q", want)
		}
	}
}

func TestHookRunsTwiceNoDuplicate(t *testing.T) {
	a := newAdapter(t)
	a.enable(t)
	runHook(t, a, "s1", "SessionEnd")
	first := a.ledger(t)
	runHook(t, a, "s1", "SessionEnd")
	if second := a.ledger(t); second != first {
		t.Errorf("a re-run changed the ledger:\n--- first ---\n%s\n--- second ---\n%s",
			first, second)
	}
}

func TestHookContinuesNewRecordsOnly(t *testing.T) {
	a := newAdapter(t)
	a.enable(t)
	runHook(t, a, "s1", "SessionEnd")
	appendTranscript(t, a.transcript, humanRec("新问题", "s1"), assistantRec("新回答", "s1"))
	runHook(t, a, "s1", "SessionEnd")

	content := a.ledger(t)
	if !strings.Contains(content, "新问题") {
		t.Error("the new question must be appended")
	}
	if got := strings.Count(content, "怎么设计缓存？"); got != 1 {
		t.Errorf("the old question appears %d times, want 1", got)
	}
}

// Stop mode: a trailing chain ending in a tool call is deferred, then
// appended exactly once once it is completed.
func TestStopHookIncompleteChainDeferred(t *testing.T) {
	a := newAdapter(t)
	a.enable(t)
	writeTranscript(t, a.transcript,
		humanRec("q1", "s1"), assistantRec("a1", "s1"),
		humanRec("q2", "s1"), toolUseRec("Edit", "s1"))

	runHook(t, a, "s1", "Stop")
	content := a.ledger(t)
	if !strings.Contains(content, "q1") || !strings.Contains(content, "a1") {
		t.Error("the complete chain must be recorded")
	}
	if strings.Contains(content, "q2") {
		t.Error("an incomplete chain must not be recorded yet")
	}

	appendTranscript(t, a.transcript, assistantRec("a2", "s1"))
	runHook(t, a, "s1", "Stop")
	content = a.ledger(t)
	for _, want := range []string{"q2", "a2"} {
		if !strings.Contains(content, want) {
			t.Errorf("ledger is missing %q", want)
		}
	}
	// The fallback session title also contains q1, so count entry markers.
	if got := strings.Count(content, "**Question:** "); got != 2 {
		t.Errorf("question markers = %d, want 2 (q1+q2, no duplicates)", got)
	}
}

// SessionEnd mode: a trailing chain ending in a tool call is recorded
// question-only and fully consumed.
func TestSessionEndRecordsIncompleteChainQuestionOnly(t *testing.T) {
	a := newAdapter(t)
	a.enable(t)
	writeTranscript(t, a.transcript,
		humanRec("q1", "s1"), assistantRec("a1", "s1"),
		humanRec("q2", "s1"), toolUseRec("Edit", "s1"))

	runHook(t, a, "s1", "SessionEnd")
	content := a.ledger(t)
	if got := strings.Count(content, "**Question:**"); got != 2 {
		t.Errorf("question markers = %d, want 2", got)
	}
	if got := strings.Count(content, "**Answer:**"); got != 1 {
		t.Errorf("answer markers = %d, want 1: only q1 was answered", got)
	}
	if !strings.Contains(content, "q2") {
		t.Error("the unanswered question must still be recorded")
	}
}

func TestNoMarkerNoLedger(t *testing.T) {
	a := newAdapter(t)
	_, stderr, code := runHook(t, a, "s1", "SessionEnd")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	if a.hasLedger() {
		t.Error("without the marker the plugin must write nothing")
	}
}

// The plugin must never block a session, whatever arrives on stdin.
func TestBadStdinExitsZero(t *testing.T) {
	a := newAdapter(t)
	a.enable(t)
	cmd := exec.Command(binaryPath)
	cmd.Stdin = strings.NewReader("not-json")
	cmd.Dir = a.dir
	if err := cmd.Run(); err != nil {
		t.Errorf("bad stdin must still exit 0, got %v", err)
	}
}

func TestEmptyStdinExitsZero(t *testing.T) {
	a := newAdapter(t)
	a.enable(t)
	cmd := exec.Command(binaryPath)
	cmd.Stdin = strings.NewReader("")
	cmd.Dir = a.dir
	if err := cmd.Run(); err != nil {
		t.Errorf("empty stdin must still exit 0, got %v", err)
	}
}

// A cwd the binary cannot resolve must say so. Left silent it is
// indistinguishable from a project that was simply never opted in, so
// recording would appear to be switched off forever with nothing to go on.
func TestUnusableCwdIsReported(t *testing.T) {
	a := newAdapter(t)
	a.enable(t)
	payload, err := json.Marshal(map[string]any{
		"session_id": "s1", "transcript_path": a.transcript,
		"cwd":             filepath.Join(a.dir, "no-such-subdirectory"),
		"hook_event_name": "SessionEnd",
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binaryPath)
	cmd.Stdin = strings.NewReader(string(payload))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Errorf("an unusable cwd must still exit 0, got %v", err)
	}
	if !strings.Contains(stderr.String(), "not a usable directory") {
		t.Errorf("the cause must reach stderr, got %q", stderr.String())
	}
}

func TestNoQuestionRunThenResume(t *testing.T) {
	a := newAdapter(t)
	a.enable(t)
	writeTranscript(t, a.transcript,
		map[string]any{"type": "ai-title", "aiTitle": "缓存设计讨论",
			"sessionId": "s1"})

	runHook(t, a, "s1", "SessionEnd")
	if a.hasLedger() {
		t.Error("a session with no questions must not create a ledger")
	}

	appendTranscript(t, a.transcript,
		humanRec("怎么设计缓存？", "s1"), assistantRec("建议用 LRU。", "s1"))
	runHook(t, a, "s1", "SessionEnd")

	content := a.ledger(t)
	for _, want := range []string{"怎么设计缓存？", "建议用 LRU。",
		"<!-- session: s1 -->"} {
		if !strings.Contains(content, want) {
			t.Errorf("ledger is missing %q", want)
		}
	}
}

// Turning recording off means "stop here", not "pause and catch up later".
// The stored position only moves while the hook is allowed to run, so unless
// it is kept current a resumed project writes in the whole paused stretch.
func TestPauseThenResumeDoesNotBackfillThePausedStretch(t *testing.T) {
	a := newAdapter(t)
	a.enable(t)
	runHook(t, a, "s1", "SessionEnd")
	before := a.ledger(t)

	// pause
	if err := os.Remove(filepath.Join(a.dir, ".tracedoc-on")); err != nil {
		t.Fatal(err)
	}
	appendTranscript(t, a.transcript,
		humanRec("暂停期间的问题", "s1"), assistantRec("暂停期间的回答", "s1"))
	runHook(t, a, "s1", "SessionEnd")
	if after := a.ledger(t); after != before {
		t.Error("the ledger changed while recording was off")
	}

	// resume
	a.enable(t)
	appendTranscript(t, a.transcript,
		humanRec("恢复后的问题", "s1"), assistantRec("恢复后的回答", "s1"))
	runHook(t, a, "s1", "SessionEnd")

	content := a.ledger(t)
	if strings.Contains(content, "暂停期间的问题") {
		t.Error("the question asked while recording was off was written in on resume")
	}
	if strings.Contains(content, "暂停期间的回答") {
		t.Error("the answer given while recording was off was written in on resume")
	}
	if !strings.Contains(content, "恢复后的问题") {
		t.Error("recording did not resume")
	}
	if !strings.Contains(content, "恢复后的回答") {
		t.Error("the answer given after resuming was not recorded")
	}
}

// The other half of that contract: a project that never opted in is left
// completely alone, state file included.
func TestProjectThatNeverOptedInGetsNoStateFile(t *testing.T) {
	a := newAdapter(t)
	runHook(t, a, "s1", "SessionEnd")

	if a.hasLedger() {
		t.Error("no ledger should be written without the marker")
	}
	statePath := filepath.Join(a.dir, ".tracedoc-state.json")
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Error("a project that never opted in must not get a state file")
	}
}

// A session's cwd follows `cd`; the project root does not.
//
// Claude Code documents the split -- the payload's cwd is "the new directory
// after Claude runs cd", while CLAUDE_PROJECT_DIR is "the project root where
// the session started" -- and both were confirmed against 2.1.263. Anchoring
// the marker lookup on the payload's cwd therefore stops recording the moment
// the shell walks into a subdirectory, silently and indistinguishably from a
// project that never opted in. The ledger name comes from the same value, so
// it would drift too.
func TestRecordingSurvivesMovingIntoASubdirectory(t *testing.T) {
	a := newAdapter(t)
	a.enable(t)
	runHook(t, a, "s1", "SessionEnd")

	sub := filepath.Join(a.dir, "code", "plugin")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	appendTranscript(t, a.transcript,
		humanRec("在子目录里问的问题", "s1"), assistantRec("在子目录里的回答", "s1"))

	// The hook now reports the subdirectory, as Claude Code does after a cd.
	// Only the environment still names the project.
	payload, err := json.Marshal(map[string]any{
		"session_id": "s1", "transcript_path": a.transcript,
		"cwd": sub, "reason": "other", "hook_event_name": "SessionEnd",
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binaryPath)
	cmd.Stdin = strings.NewReader(string(payload))
	cmd.Env = hookEnv(a.dir)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("hook with a drifted cwd: %v (stderr: %s)", err, stderr.String())
	}

	if !strings.Contains(a.ledger(t), "在子目录里问的问题") {
		t.Error("recording stopped when the session moved into a subdirectory")
	}
	// Nothing at all belongs down there -- not the ledger, not the state
	// file, not the lock.
	entries, err := os.ReadDir(sub)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("the subdirectory was written to: %v", names)
	}
}

// --- the opt-in marker ---------------------------------------------------

// The marker is an empty file, and no single shell command creates one on
// every platform -- `touch` does not exist in cmd or PowerShell. The binary
// does, so these must work.
func TestEnableAndDisableMarker(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, ".tracedoc-on")

	enable := exec.Command(binaryPath, "--enable")
	enable.Dir = dir
	if out, err := enable.CombinedOutput(); err != nil {
		t.Fatalf("--enable: %v (%s)", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("--enable must create the marker: %v", err)
	}

	disable := exec.Command(binaryPath, "--disable")
	disable.Dir = dir
	if out, err := disable.CombinedOutput(); err != nil {
		t.Fatalf("--disable: %v (%s)", err, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("--disable must remove the marker")
	}
}

func TestDisableWithoutAMarkerIsNotAnError(t *testing.T) {
	cmd := exec.Command(binaryPath, "--disable")
	cmd.Dir = t.TempDir()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("--disable on a project that never opted in must succeed: %v (%s)",
			err, out)
	}
}

// Enabling then running the hook is the whole user-visible path, so it is
// worth asserting end to end rather than only in pieces.
func TestEnableThenHookRecords(t *testing.T) {
	a := newAdapter(t)
	enable := exec.Command(binaryPath, "--enable")
	enable.Dir = a.dir
	if out, err := enable.CombinedOutput(); err != nil {
		t.Fatalf("--enable: %v (%s)", err, out)
	}

	if _, _, code := runHook(t, a, "s1", "SessionEnd"); code != 0 {
		t.Fatalf("hook exit code = %d", code)
	}
	if !a.hasLedger() {
		t.Error("after --enable, a session must actually be recorded")
	}
}

// --- the original's Windows failure modes, each pinned -------------------

// A non-ASCII project path is where the original failed silently. Python read
// stdin through sys.stdin, whose encoding is the locale's -- cp936 on a
// Chinese Windows -- while Claude Code writes UTF-8. The payload decoded to
// the wrong characters, the project path then resolved to nothing, and the
// hook exited 0 having written no ledger and printed no warning. Nothing
// downstream could tell that from "recording is switched off".
func TestChineseProjectPath(t *testing.T) {
	a := newAdapterIn(t, filepath.Join(t.TempDir(), "演示项目"))
	a.enable(t)

	_, stderr, code := runHook(t, a, "s1", "SessionEnd")
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	if !a.hasLedger() {
		t.Fatal("no ledger written for a non-ASCII project path")
	}
	content := a.ledger(t)
	for _, want := range []string{"怎么设计缓存？", "建议用 LRU。", "演示项目"} {
		if !strings.Contains(content, want) {
			t.Errorf("ledger is missing %q", want)
		}
	}
}

// The plugin must never block a session, including when its own lock cannot
// be taken. The original broke this contract outright on Windows: the fcntl
// import sat at module scope, outside the adapter's try/except, so the
// process died before main() was reached and the hook exited 1 -- turning a
// missing lock into a failed session.
func TestHookExitsZeroWhenTheLockIsUnavailable(t *testing.T) {
	a := newAdapter(t)
	a.enable(t)
	// A directory where the lock file belongs: opening it for writing fails.
	if err := os.Mkdir(filepath.Join(a.dir, ".tracedoc.lock"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := runHook(t, a, "s1", "SessionEnd")
	if code != 0 {
		t.Errorf("a hook that cannot lock must still exit 0, got %d (stderr: %s)",
			code, stderr)
	}
	if !strings.Contains(stderr, "ai-tracedoc:") {
		t.Errorf("the failure must be reported on stderr, got %q", stderr)
	}
}

// Diagnostics must survive any character a path can contain. The original
// reported errors through sys.stdout, whose encoding is the locale's, so a
// character outside the local codepage raised UnicodeEncodeError -- from
// inside the except handler, which meant the exception escaped and the hook
// exited 1 through the very code meant to guarantee it never would.
func TestNonLocalCodepageCharacterInDiagnostics(t *testing.T) {
	a := newAdapter(t)
	a.enable(t)

	missing := filepath.Join(t.TempDir(), "项目 🚀")
	payload, err := json.Marshal(map[string]any{
		"session_id": "s1", "transcript_path": a.transcript,
		"cwd":             missing,
		"hook_event_name": "SessionEnd",
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binaryPath)
	cmd.Stdin = strings.NewReader(string(payload))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Errorf("an unencodable diagnostic must still exit 0, got %v", err)
	}
	if !strings.Contains(stderr.String(), "🚀") {
		t.Errorf("the path must survive into stderr intact, got %q", stderr.String())
	}
}

// --- plugin files -------------------------------------------------------

func TestPluginJSONValid(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
		Author      struct {
			Name string `json:"name"`
		} `json:"author"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if data.Name != "ai-tracedoc" {
		t.Errorf("name = %q", data.Name)
	}
	// The version is deliberately not pinned to a literal here. Its value has
	// a consequence only where it can disagree with something, and that
	// comparison lives in TestShippedBinariesCarryThePluginVersion.

	if !strings.Contains(data.Description, "development") {
		t.Errorf("description = %q", data.Description)
	}
	if data.Author.Name != "ddk" {
		t.Errorf("author = %+v", data.Author)
	}
}

// pluginVersion reads the version from the plugin manifest, which is the one
// place a release declares it.
func pluginVersion(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if data.Version == "" {
		t.Fatal("plugin.json declares no version")
	}
	return data.Version
}

// The version in plugin.json and the version baked into every shipped binary
// must agree.
//
// They answer "which version am I running?" from two directions: plugin.json
// decides where the install cache puts the plugin and what /ai-tracedoc:version
// reports, while the build ID is the only thing readable out of the binary
// itself. build.sh derives the build ID from plugin.json, and until this test
// nothing ever read it back -- so bumping one and forgetting the other was
// invisible. That matters more than usual here, because the release policy is
// to burn the version number on every refused build: a two-file edit repeated
// often is a two-file edit forgotten eventually.
//
// Every binary is checked, not just the host's. Reading a build ID is a byte
// scan that executes nothing, so the arm64 and darwin builds are covered from
// a Windows checkout that can never run them.
func TestShippedBinariesCarryThePluginVersion(t *testing.T) {
	want := "ai-tracedoc-" + pluginVersion(t)

	binDir := filepath.Join(repoRoot, "bin")
	entries, err := os.ReadDir(binDir)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == "tracedoc" {
			// The shell dispatcher is not a build and carries no build ID.
			continue
		}
		path := filepath.Join(binDir, name)
		id, err := exec.Command("go", "tool", "buildid", path).Output()
		if err != nil {
			t.Errorf("%s: reading the build ID: %v", name, err)
			continue
		}
		checked++
		if got := strings.TrimSpace(string(id)); got != want {
			t.Errorf("%s carries build ID %q, want %q. Run `sh build.sh` and "+
				"commit the result.", name, got, want)
		}
	}
	if checked == 0 {
		t.Fatal("bin/ holds nothing to check, so this test proved nothing")
	}
}

// The commands are markdown, so nothing compiles them: a typo in the
// frontmatter surfaces only as a command that quietly misbehaves. These check
// the parts that carry meaning.
//
// Every .md in commands/ is checked, rather than a list written out here. A
// list covers only the commands someone remembered to add to it, which is how
// version.md came to sit outside this test entirely -- written, edited twice,
// and never once checked, in a test whose whole premise is that nothing else
// will catch a mistake in one.
//
// The shared rules apply to every file; the table below holds only what is
// specific to one command and is looked up by filename, so a new command is
// covered the moment it exists.
func TestCommandFilesAreWellFormed(t *testing.T) {
	specific := map[string][]string{
		"on.md": {
			// Manual-only: whether conversations get written to disk is
			// not something the model should decide on its own.
			"disable-model-invocation: true",
			// Editing is the one tool that can create the marker, and the
			// rule is scoped to that single path.
			"allowed-tools: Edit(./.tracedoc-on)",
			"./.tracedoc-on",
		},
		"off.md": {
			"disable-model-invocation: true",
			// There is no delete tool, so removal needs a shell -- and
			// Bash and PowerShell rules are separate families, so both
			// platforms have to be listed.
			"Bash(rm .tracedoc-on)",
			"PowerShell(Remove-Item .tracedoc-on)",
		},
		"version.md": {
			"disable-model-invocation: true",
			// It has to answer for the copy that is running. The working tree
			// and the install cache hold separate copies and can carry the same
			// version string, so the running directory is the only thing that
			// tells them apart -- and since 2026-10-03 that directory's name is
			// the whole answer, because Claude Code refuses an injected command
			// naming a path under ~/.claude/, which an installed plugin's root
			// always is. Reading plugin.json there is not available to us.
			"${CLAUDE_PLUGIN_ROOT}",
			// Pre-approval, because an injected command never gets to prompt:
			// a check that would have asked aborts the invocation instead.
			`allowed-tools: Bash(basename "${CLAUDE_PLUGIN_ROOT}")`,
		},
	}

	dir := filepath.Join(repoRoot, "commands")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			files = append(files, entry.Name())
		}
	}
	if len(files) == 0 {
		t.Fatalf("no command files in %s: a glob that matches nothing passes "+
			"by saying nothing", dir)
	}

	for _, file := range files {
		raw, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		text := string(raw)

		if !strings.HasPrefix(text, "---\n") {
			t.Errorf("%s: frontmatter must open on the file's first line", file)
		}
		if !strings.Contains(text, "description: ") {
			t.Errorf("%s: no description, so the command is undiscoverable", file)
		}
		for _, want := range specific[file] {
			if !strings.Contains(text, want) {
				t.Errorf("%s: missing %q", file, want)
			}
		}
	}

	// Renaming a command would otherwise drop its expectations in silence: the
	// glob stops finding the file and nothing is left to fail.
	for file := range specific {
		if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
			t.Errorf("the expectations above name %s, which is not there: %v",
				file, err)
		}
	}
}

func TestHooksJSONValid(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	// The dispatcher is invoked in shell form: Windows CreateProcess cannot
	// execute a shebang script, so an args array would break the hook. It is
	// run through `sh` rather than executed directly so that only the platform
	// binaries depend on the executable bit surviving git -- which it does
	// not, on a checkout made from Windows. The placeholder stays inside
	// quotes because Git Bash strips unquoted backslashes and the plugin root
	// path is full of them.
	const want = `sh "${CLAUDE_PLUGIN_ROOT}/bin/tracedoc"`
	for _, event := range []string{"SessionEnd", "Stop"} {
		entries, ok := data.Hooks[event]
		if !ok {
			t.Errorf("hooks.json has no %s event", event)
			continue
		}
		if entries[0].Matcher != "*" {
			t.Errorf("%s matcher = %q", event, entries[0].Matcher)
		}
		hook := entries[0].Hooks[0]
		if hook.Type != "command" {
			t.Errorf("%s hook type = %q", event, hook.Type)
		}
		if hook.Command != want {
			t.Errorf("%s command = %q, want %q", event, hook.Command, want)
		}
		if hook.Timeout != 30 {
			t.Errorf("%s timeout = %d", event, hook.Timeout)
		}
	}
}
