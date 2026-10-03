package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The plugin ships prebuilt binaries in the repository and nothing at install
// time rebuilds them, so a binary left behind by an edit is a silent failure
// of exactly the kind this plugin exists to prevent: the suite passes, the
// release installs, and it runs the old code.
//
// The comparison is behavioural rather than by hash, because Go does not
// promise byte-identical binaries across toolchain versions. What it covers
// is deliberately wider than the ledger alone -- the self-test output, the
// help text and the diagnostics on an unusable cwd are included -- since a
// change confined to an error path writes the same ledger and would otherwise
// slip through.
//
// The committed binary is executed from a copy rather than in place. See
// runnableCopy for why, and for what a refusal does to the comparison.
func TestCommittedBinaryMatchesSource(t *testing.T) {
	name := "tracedoc-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	committed := filepath.Join(repoRoot, "bin", name)
	if _, err := os.Stat(committed); err != nil {
		t.Skipf("no committed build for %s/%s in bin/", runtime.GOOS, runtime.GOARCH)
	}

	fresh := filepath.Join(t.TempDir(), "tracedoc")
	if runtime.GOOS == "windows" {
		fresh += ".exe"
	}
	build := exec.Command("go", "build", "-o", fresh, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build from source: %v", err)
	}

	committedBehaviour := behaviour(t, runnableCopy(t, committed))
	freshBehaviour := behaviour(t, fresh)
	if committedBehaviour != freshBehaviour {
		t.Errorf("bin/%s is stale: it disagrees with a build of the current "+
			"source. Run `sh build.sh` and commit the result.\n"+
			"--- committed ---\n%s\n--- fresh ---\n%s",
			name, committedBehaviour, freshBehaviour)
	}
}

// runnableCopy returns a copy of binary at a path of its own, having first
// established that the copy can actually be executed.
//
// The check is here because a refusal reaches Go as a start failure rather than
// an exit code, and the comparison downstream would report it as "bin/... is
// stale" -- a claim about the code that nothing supports. A refusal still fails
// the test, since a guard that goes quiet is the failure this plugin exists to
// prevent; it just does not pretend to have compared anything.
//
// Copying is not what makes it run, and an earlier version of this comment said
// it was: that Smart App Control refuses a path rather than a set of bytes. It
// does neither. Measured on 2026-09-21, this same path went from 25 straight
// refusals at 14:25 to 40 straight runs at 14:28 with nothing done to it, while
// a renamed copy ran 10 out of 10 beside an original refused 10 out of 10 in the
// same loop. The verdict moves in time, and every block that day named that one
// path only because that is the path the tests were running. The copy is here so
// the probe executes something this test owns and cleans up, and for no reason
// beyond that.
func runnableCopy(t *testing.T, binary string) string {
	t.Helper()
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), filepath.Base(binary))
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatal(err)
	}

	probe := exec.Command(dst)
	probe.Stdin = strings.NewReader("{}")
	if err := probe.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("%s could not be executed (%v). That is a code-integrity "+
				"refusal on this host, not a staleness finding: the bytes were "+
				"never given the chance to run.", filepath.Base(binary), err)
		}
	}
	return dst
}

// behaviour collects everything one binary says about a fixed transcript:
// what it writes, what --self-test prints, what --help prints, and what it
// reports when handed a path it cannot use.
func behaviour(t *testing.T, binary string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	a := &adapter{dir: dir, transcript: filepath.Join(dir, "session.jsonl")}
	writeStandardTranscript(t, a.transcript)
	a.enable(t)

	var sb strings.Builder

	_, stderr, code := runHookBinary(t, binary, a, "s1", "SessionEnd")
	fmt.Fprintf(&sb, "hook: exit=%d stderr=%s\n", code, stderr)
	sb.WriteString("ledger:\n")
	sb.WriteString(a.ledger(t))

	stdout, stderr, code := runSelfTestWith(t, binary, a.transcript)
	fmt.Fprintf(&sb, "\nself-test: exit=%d stderr=%s\n%s", code, stderr, stdout)

	// The help text, which nothing else here reaches: usage() prints only for
	// -h/--help, so a change confined to it produced an identical signature
	// and the guard passed. Measured 2026-09-21 -- mutating that string left
	// this comparison green, which is the error-path blind spot the header
	// above claims to close.
	stdout, stderr, code = runBinaryWith(t, binary, "-h")
	fmt.Fprintf(&sb, "\nusage: exit=%d stderr=%s\n%s", code, stderr, stdout)

	// A fixed path outside either run's directory. The diagnostic names the
	// path, so using a per-run temp dir here would make the two signatures
	// differ on the path alone and fail the comparison for the wrong reason.
	payload, err := json.Marshal(map[string]any{
		"session_id": "s1", "transcript_path": a.transcript,
		"cwd":             filepath.Join(os.TempDir(), "tracedoc-probe-missing"),
		"hook_event_name": "SessionEnd",
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary)
	cmd.Stdin = strings.NewReader(string(payload))
	var badCwdErr strings.Builder
	cmd.Stderr = &badCwdErr
	code = 0
	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run with a bad cwd: %v", err)
		}
		code = exitErr.ExitCode()
	}
	fmt.Fprintf(&sb, "bad-cwd: exit=%d stderr=%s\n", code, badCwdErr.String())

	// A second, independent project driving the pause/resume path.
	//
	// The scenario above only ever runs a plain SessionEnd, which leaves
	// advanceWhileOff -- the code that keeps a paused project's position
	// current -- entirely uncovered. It was written, and then changed again,
	// without this comparison noticing either time. What makes the path
	// visible here is the state file: advanceWhileOff writes nothing else, so
	// without it a change confined to pausing would produce an identical
	// ledger and slip through exactly as before.
	//
	// This guards staleness, not correctness -- that the paused stretch stays
	// out of the ledger is asserted directly in main_test.go, which would
	// otherwise hold for two equally broken binaries.
	sb.WriteString("\npause/resume:\n")
	sb.WriteString(pauseResumeBehaviour(t, binary))

	return sb.String()
}

// pauseResumeBehaviour records a session, switches recording off, says more,
// switches it back on, and reports the state file and ledger it ends with.
func pauseResumeBehaviour(t *testing.T, binary string) string {
	t.Helper()

	// The project name reaches the signature -- it is the ledger's filename and
	// heading -- so it has to be stable across the two runs. t.TempDir() alone
	// returns a numbered directory (/001, /002, ...), which differs between
	// them and would fail the comparison for a reason that has nothing to do
	// with the binaries.
	a := newAdapterIn(t, filepath.Join(t.TempDir(), "paused"))
	a.enable(t)
	runHookBinary(t, binary, a, "s1", "SessionEnd")

	if err := os.Remove(filepath.Join(a.dir, ".tracedoc-on")); err != nil {
		t.Fatal(err)
	}
	appendTranscript(t, a.transcript,
		humanRec("暂停期间的问题", "s1"), assistantRec("暂停期间的回答", "s1"))
	runHookBinary(t, binary, a, "s1", "SessionEnd")

	a.enable(t)
	appendTranscript(t, a.transcript,
		humanRec("恢复后的问题", "s1"), assistantRec("恢复后的回答", "s1"))
	runHookBinary(t, binary, a, "s1", "SessionEnd")

	state, err := os.ReadFile(filepath.Join(a.dir, ".tracedoc-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("state:\n%s\nledger:\n%s\n", state, a.ledger(t))
}
