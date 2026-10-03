// Command tracedoc is the ai-tracedoc hook entry point for Claude Code's
// SessionEnd and Stop events.
//
// It reads the hook JSON on stdin (session_id / transcript_path / cwd / ...),
// guards on the .tracedoc-on marker, incrementally parses the transcript and
// appends Q&A entries to the project's TraceDoc ledger.
//
// It never blocks a session: every failure path reports on stderr and exits
// 0, so a broken ledger can never stop someone from ending a session. The one
// exception is a --self-test usage error, which is a person at a terminal
// asking for help rather than a hook running unattended.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/didongke/ai-tracedoc/internal/tracedoc"
)

type hookPayload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
}

func main() {
	os.Exit(runGuarded(os.Args[1:]))
}

// runGuarded is run() with a net under it.
//
// The plugin's contract is that it never blocks a session, and a panic would
// break that just as surely as an unhandled error: a hook exiting non-zero is
// a hook failing the session it was only supposed to observe. The original
// wrapped its entry point in try/except for the same reason. In Go the
// realistic source is syscall.LazyProc.Call, which panics if a procedure is
// missing from the DLL rather than returning an error.
//
// The panic is reported rather than swallowed -- hidden is the one thing it
// must not be -- but it does not become the exit code.
func runGuarded(argv []string) (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "ai-tracedoc: internal error: %v\n", r)
			code = 0
		}
	}()
	return run(argv)
}

func run(argv []string) int {
	// Commands a person types. They exit non-zero on failure, unlike the hook
	// path below -- someone at a terminal needs to hear about a problem.
	if len(argv) > 0 {
		switch argv[0] {
		case "--enable":
			return setMarker(true)
		case "--disable":
			return setMarker(false)
		case "-h", "--help":
			usage()
			return 0
		}
	}

	for index, arg := range argv {
		if arg != "--self-test" {
			continue
		}
		if index+1 >= len(argv) {
			fmt.Fprintln(os.Stderr, "usage: tracedoc --self-test <transcript.jsonl>")
			return 1
		}
		selfTest(argv[index+1])
		return 0
	}

	// Stdin is read as raw bytes and decoded as UTF-8 by encoding/json. The
	// Python original read through sys.stdin, whose encoding is the locale's
	// -- cp936 on a Chinese Windows -- so one non-ASCII character in the
	// payload decoded to the wrong bytes, the transcript path then resolved
	// to nothing, and the hook exited 0 having written no ledger and said
	// nothing. Silent, and invisible in any log.
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-tracedoc: %v\n", err)
		return 0
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}") // an empty stdin is an empty hook, not an error
	}
	var payload hookPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		fmt.Fprintf(os.Stderr, "ai-tracedoc: %v\n", err)
		return 0
	}
	if err := record(&payload); err != nil {
		fmt.Fprintf(os.Stderr, "ai-tracedoc: %v\n", err)
	}
	return 0
}

// record performs one hook run. A nil return means "nothing to do" as often
// as it means "done"; both are silent by design.
func record(hook *hookPayload) error {
	// CLAUDE_PROJECT_DIR is consulted first because it is the directory the
	// session was launched in, and it stays there for the whole session. The
	// payload's cwd is where Claude is *now*: Claude Code documents that it
	// follows `cd`, and a session that walks into a subdirectory would look
	// for the marker there, not find it, and stop recording in silence --
	// indistinguishable from a project that never opted in. It would also
	// name the ledger after the subdirectory, since the name comes from this
	// same value. At session start the two agree, so anchoring on the project
	// root changes nothing except that drift.
	cwd := os.Getenv("CLAUDE_PROJECT_DIR")
	if cwd == "" {
		cwd = hook.Cwd
	}
	if cwd == "" {
		workingDir, err := os.Getwd()
		if err != nil {
			return err
		}
		cwd = workingDir
	}

	if hook.TranscriptPath == "" || hook.SessionID == "" {
		return nil
	}
	if info, err := os.Stat(hook.TranscriptPath); err != nil || info.IsDir() {
		return nil
	}
	// The marker is what opts a project in.
	if !tracedoc.RecordingEnabled(cwd) {
		// Separate "this project is not opted in", which is ordinary and
		// must stay silent, from "cwd does not resolve", which means the
		// hook was handed a path it cannot use. Left unsaid, the second
		// looks exactly like the first -- forever.
		if info, cwdErr := os.Stat(cwd); cwdErr != nil || !info.IsDir() {
			return fmt.Errorf("cwd %q is not a usable directory", cwd)
		}
		return tracedoc.ProjectLock(cwd, func() error {
			return advanceWhileOff(cwd, hook)
		})
	}

	project := filepath.Base(filepath.Clean(cwd))

	return tracedoc.ProjectLock(cwd, func() error {
		// Critical section: read state -> append ledger -> save state.
		state := tracedoc.LoadState(cwd)
		offset := 0
		if known, ok := state.Sessions[hook.SessionID]; ok {
			offset = known.Offset
		}

		// The reader's own new offset is deliberately discarded: the stored
		// offset advances by consumed record count, via ends below, because
		// a corrupt line advances the reader without producing a record.
		_, records, ends, err := tracedoc.IterRecordsFrom(hook.TranscriptPath, offset)
		if err != nil {
			return err
		}

		// Stop mode consumes complete chains only, so a chain still being
		// written waits for the next trigger; SessionEnd flushes everything.
		completeOnly := hook.HookEventName != "SessionEnd"
		sessions, consumed := tracedoc.ExtractSessions(records, nil, completeOnly)
		for _, session := range sessions {
			if len(session.Entries) == 0 {
				continue
			}
			if err := tracedoc.AppendSession(cwd, project, session, state,
				"", tracedoc.VolumeThresholdBytes); err != nil {
				return err
			}
		}

		advance := offset
		if consumed > 0 && consumed-1 < len(ends) {
			advance = ends[consumed-1]
		}
		if advance != offset {
			if known, ok := state.Sessions[hook.SessionID]; ok {
				known.Offset = advance
				return tracedoc.SaveState(cwd, state)
			}
		}
		return nil
	})
}

// advanceWhileOff keeps the stored position current while recording is paused.
//
// Turning recording off means "stop here", not "pause and catch up later". But
// the stored offset only moves while the hook is allowed to run, so without
// this a paused project's offset keeps pointing at the moment recording
// stopped -- and switching recording back on writes in everything that was
// said in between.
//
// Nothing is written for a project that has never been recorded: with no state
// file there is no position to keep, and a project that has not opted in is
// left entirely alone.
func advanceWhileOff(cwd string, hook *hookPayload) error {
	if _, err := os.Stat(filepath.Join(cwd, tracedoc.StateFilename)); err != nil {
		return nil // never recorded here: nothing to keep current
	}
	state := tracedoc.LoadState(cwd)
	known, ok := state.Sessions[hook.SessionID]
	if !ok {
		// A session with nothing recorded yet has no state of its own, but
		// the project has been recorded before -- so its position still has
		// to be tracked, or switching recording back on mid-session would
		// write in everything said since the session began.
		known = &tracedoc.SessionState{}
		state.Sessions[hook.SessionID] = known
	}
	position, _, _, err := tracedoc.IterRecordsFrom(hook.TranscriptPath, known.Offset)
	if err != nil {
		return err
	}
	if position == known.Offset {
		return nil
	}
	known.Offset = position
	return tracedoc.SaveState(cwd, state)
}

// setMarker creates or removes the per-project opt-in marker.
//
// The marker is an empty file, and there is no command that creates one on
// every platform: `touch` does not exist in cmd or PowerShell, and the
// alternatives differ between them. The plugin is already a program that
// knows how to write a file, so it may as well be the one to do it -- which
// makes enabling recording the same command everywhere.
func setMarker(enable bool) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-tracedoc: %v\n", err)
		return 1
	}
	path := filepath.Join(cwd, tracedoc.MarkerFilename)

	if enable {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "ai-tracedoc: %v\n", err)
			return 1
		}
		fmt.Printf("recording enabled for %s\n", cwd)
		return 0
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "ai-tracedoc: %v\n", err)
		return 1
	}
	fmt.Printf("recording disabled for %s\n", cwd)
	return 0
}

func usage() {
	fmt.Print(`ai-tracedoc - record Claude Code conversations into a TraceDoc ledger

Run inside a project directory:

  tracedoc --enable        start recording sessions in this directory
  tracedoc --disable       stop recording them

Offline checks (write nothing):

  tracedoc --self-test <transcript.jsonl>
                           print what would be recorded from a transcript

Claude Code calls this binary itself, through bin/tracedoc, with the hook
payload on stdin. In that mode it always exits 0: a broken ledger must never
block the session it was only supposed to observe.
`)
}

// selfTest prints what would be written, using SessionEnd's full-flush
// semantics, and writes nothing. It exists so someone can check what the
// plugin makes of a transcript without enabling it on a project.
func selfTest(transcriptPath string) {
	_, records, _, err := tracedoc.IterRecordsFrom(transcriptPath, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-tracedoc: %v\n", err)
		return
	}
	sessions, _ := tracedoc.ExtractSessions(records, nil, false)

	var out bytes.Buffer
	for _, session := range sessions {
		out.WriteString(tracedoc.FormatSessionHeader(
			session.Date, session.Title, session.SessionID))
		out.WriteString("\n")
		out.WriteString(tracedoc.FormatEntries(session.Entries))
		out.WriteString("\n")
	}
	// Written as bytes rather than through fmt so the output is UTF-8 on
	// every platform, whatever the console codepage happens to be.
	_, _ = os.Stdout.Write(out.Bytes())
}
