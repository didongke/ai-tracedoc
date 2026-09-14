#!/usr/bin/env python3
"""ai-tracedoc Claude Code adapter: SessionEnd / Stop hook entry point.

Reads the hook JSON from stdin (session_id / transcript_path / cwd / ...),
guards on the .tracedoc-on marker, incrementally parses the transcript and
appends Q&A entries to the project's TraceDoc ledger. Never blocks the
session: every failure exits 0 with diagnostics on stderr only
(design doc §4.3).
"""
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "src"))

from tracedoc import ledger                                  # noqa: E402
from tracedoc.extract import extract_sessions, iter_records_from  # noqa: E402


def self_test(transcript_path):
    """Offline mode: print what would be written (SessionEnd full-flush
    semantics), without writing any file."""
    _, records, _ = iter_records_from(transcript_path, 0)
    sessions, _ = extract_sessions(records, complete_only=False)
    for session in sessions:
        print(ledger.format_session_header(
            session["date"], session["title"], session["session_id"]))
        print(ledger.format_entries(session["entries"]))


def run(hook):
    cwd = hook.get("cwd") or os.environ.get("CLAUDE_PROJECT_DIR") or os.getcwd()
    transcript = hook.get("transcript_path")
    session_id = hook.get("session_id")
    if not transcript or not session_id or not os.path.isfile(transcript):
        return
    if not os.path.isfile(os.path.join(cwd, ledger.MARKER_FILENAME)):
        return

    project = os.path.basename(os.path.normpath(cwd))
    with ledger.project_lock(cwd):
        # Critical section: read state -> append ledger -> save state
        # (design doc §3).
        state = ledger.load_state(cwd)
        known = (state.get("sessions") or {}).get(session_id)
        offset = known.get("offset", 0) if known else 0

        new_offset, records, ends = iter_records_from(transcript, offset)
        # Stop mode consumes complete chains only (incomplete chains wait
        # for the next trigger); SessionEnd flushes everything.
        complete_only = hook.get("hook_event_name") != "SessionEnd"
        sessions, consumed = extract_sessions(records, complete_only=complete_only)
        for session in sessions:
            if not session["entries"]:
                continue
            ledger.append_session(cwd, project, session, state)

        advance = ends[consumed - 1] if consumed else offset
        if advance != offset:
            sessions_state = state.setdefault("sessions", {})
            if session_id in sessions_state:
                sessions_state[session_id]["offset"] = advance
                ledger.save_state(cwd, state)


def main(argv):
    if "--self-test" in argv:
        index = argv.index("--self-test")
        if index + 1 >= len(argv):
            print("usage: --self-test <transcript.jsonl>", file=sys.stderr)
            return 1
        self_test(argv[index + 1])
        return 0
    try:
        run(json.loads(sys.stdin.read() or "{}"))
    except Exception as exc:      # noqa: BLE001 — never block session end
        print("ai-tracedoc: %s" % exc, file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
