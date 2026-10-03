package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// This exercises the exact command shape hooks.json declares, against a
// plugin root shaped like Claude Code's install cache. It is the one path the
// other tests simulate rather than reproduce, and it is where the platform
// choice actually happens.
//
// The command must stay in shell form. An exec-form hook (one carrying an
// args array) is spawned directly, and Windows CreateProcess refuses a
// shebang script outright, so the dispatcher could never run that way.
func TestDispatcherInvokedAsHooksJSONDeclares(t *testing.T) {
	shPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX sh on PATH; Claude Code runs hooks through one, " +
			"so this test needs it to mean anything")
	}

	// A root containing both a space and a ".claude" segment, since both are
	// things the Git Bash argument handling has been known to mangle.
	pluginRoot := filepath.Join(t.TempDir(), "Plugins Cache", ".claude",
		"ai-tracedoc")
	binDir := filepath.Join(pluginRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	dispatcher, err := os.ReadFile(filepath.Join(repoRoot, "bin", "tracedoc"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "tracedoc"), dispatcher, 0o755); err != nil {
		t.Fatal(err)
	}

	hostBinary := filepath.Join(binDir,
		"tracedoc-"+runtime.GOOS+"-"+runtime.GOARCH)
	if runtime.GOOS == "windows" {
		hostBinary += ".exe"
	}
	build := exec.Command("go", "build", "-o", hostBinary, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build host binary: %v", err)
	}

	a := newAdapter(t)
	a.enable(t)
	payload, err := json.Marshal(map[string]any{
		"session_id": "s1", "transcript_path": a.transcript,
		"cwd": a.dir, "hook_event_name": "SessionEnd",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Claude Code substitutes ${CLAUDE_PLUGIN_ROOT} and hands the shell the
	// resulting path inside double quotes. Both separators are tried because
	// the value comes from wherever the plugin was installed.
	for _, tc := range []struct{ label, root string }{
		{"backslash root", pluginRoot},
		{"forward-slash root", filepath.ToSlash(pluginRoot)},
	} {
		script := filepath.Join(t.TempDir(), "cmd.sh")
		command := "sh \"" + filepath.Join(tc.root, "bin", "tracedoc") + "\"\n"
		if err := os.WriteFile(script, []byte(command), 0o644); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command(shPath, script)
		cmd.Stdin = strings.NewReader(string(payload))
		cmd.Env = append(os.Environ(), "CLAUDE_PLUGIN_ROOT="+tc.root)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Errorf("%s: dispatcher failed: %v (stderr: %s)",
				tc.label, err, stderr.String())
			continue
		}
		if !strings.Contains(a.ledger(t), "怎么设计缓存？") {
			t.Errorf("%s: ledger does not carry the recorded question", tc.label)
		}
	}
}

// The dispatcher's two "cannot record" cases are deliberately different.
//
// A platform we ship nothing for is not the user's problem, so it stays
// quiet. A supported platform whose build cannot be run is a broken install:
// deterministic, hit on every session, and fixable -- so it exits non-zero
// and says what to do. Skipping forever in silence is the failure this
// plugin exists to prevent.
func TestDispatcherWithoutABuildFailsLoudly(t *testing.T) {
	shPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX sh on PATH")
	}

	pluginRoot := t.TempDir()
	binDir := filepath.Join(pluginRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := os.ReadFile(filepath.Join(repoRoot, "bin", "tracedoc"))
	if err != nil {
		t.Fatal(err)
	}
	// The dispatcher alone: no platform binaries beside it.
	if err := os.WriteFile(filepath.Join(binDir, "tracedoc"), dispatcher, 0o755); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(t.TempDir(), "cmd.sh")
	command := "sh \"" + filepath.Join(pluginRoot, "bin", "tracedoc") + "\"\n"
	if err := os.WriteFile(script, []byte(command), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(shPath, script)
	cmd.Stdin = strings.NewReader("{}")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Error("a supported platform with no build is a broken install; " +
			"exiting 0 would hide it forever")
	}
	if !strings.Contains(stderr.String(), "installed incorrectly") {
		t.Errorf("the cause must be named on stderr, got %q", stderr.String())
	}
}

// A binary the OS refuses to execute must not reach the user as the shell's
// raw "Permission denied" and nothing else -- which is exactly what it did
// before: the message named neither the cause nor the cure.
//
// Not hypothetical. On 2026-09-16 Windows Smart App Control refused this
// plugin's own bin/tracedoc-windows-amd64.exe, and the Stop hook failed with
// that bare errno; the same file then ran 30/30 from the same shell minutes
// later. SAC offers no per-app exclusion, so the cause is worth naming.
//
// The trigger cannot be forced on every host, so it is probed rather than
// assumed: Git Bash folds a non-executable interpreter into 127 ("required
// file not found") where a real kernel returns 126. A host that cannot
// produce the shape skips, instead of asserting something it cannot show.
func TestDispatcherNamesTheCauseWhenTheBinaryCannotBeExecuted(t *testing.T) {
	shPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX sh on PATH")
	}

	// A script whose interpreter exists but carries no executable bit. The
	// kernel refuses at exec time with EACCES -- the same errno a Windows
	// code-integrity block produces -- rather than at the dispatcher's own
	// -x pre-check, which would take the other branch.
	work := t.TempDir()
	unrunnable := filepath.Join(work, "unrunnable")
	if err := os.WriteFile(unrunnable, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(work, "stub")
	body := "#!" + filepath.ToSlash(unrunnable) + "\necho unreachable\n"
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	probe := exec.Command(shPath, "-c", `exec "$1"`, "_", stub)
	err = probe.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 126 {
		t.Skipf("this host does not surface an unrunnable binary as 126 (got %v); "+
			"the branch is still exercised where it can be", err)
	}

	pluginRoot := t.TempDir()
	binDir := filepath.Join(pluginRoot, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := os.ReadFile(filepath.Join(repoRoot, "bin", "tracedoc"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "tracedoc"), dispatcher, 0o755); err != nil {
		t.Fatal(err)
	}
	hostBinary := "tracedoc-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		hostBinary += ".exe"
	}
	if err := os.WriteFile(filepath.Join(binDir, hostBinary), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(t.TempDir(), "cmd.sh")
	command := "sh \"" + filepath.Join(pluginRoot, "bin", "tracedoc") + "\"\n"
	if err := os.WriteFile(script, []byte(command), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(shPath, script)
	cmd.Stdin = strings.NewReader("{}")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Error("a binary that cannot be executed must not be reported as success")
	}
	if !strings.Contains(stderr.String(), "could not be executed") {
		t.Errorf("the cause must be named on stderr, got %q", stderr.String())
	}
}

// The counterpart: a platform we ship nothing for is not a broken install,
// and must not surface as a hook error on every session.
func TestDispatcherUnsupportedPlatformStaysQuiet(t *testing.T) {
	shPath, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX sh on PATH")
	}
	// A stub `uname` claiming a platform nothing is built for, placed ahead
	// of the real one on PATH.
	stubDir := t.TempDir()
	stub := "#!/bin/sh\ncase \"$1\" in -s) echo Plan9 ;; -m) echo mips ;; esac\n"
	if err := os.WriteFile(filepath.Join(stubDir, "uname"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(t.TempDir(), "cmd.sh")
	command := "sh \"" + filepath.Join(repoRoot, "bin", "tracedoc") + "\"\n"
	if err := os.WriteFile(script, []byte(command), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(shPath, script)
	cmd.Stdin = strings.NewReader("{}")
	cmd.Env = append(os.Environ(), "PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Errorf("an unsupported platform must stay quiet, got %v", err)
	}
	if !strings.Contains(stderr.String(), "unsupported") {
		t.Errorf("it should still say why, got %q", stderr.String())
	}
}
