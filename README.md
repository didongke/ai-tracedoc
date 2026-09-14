# ai-tracedoc

Automatically records Claude Code development conversations into a per-project
TraceDoc ledger: your questions and the AI's final answers — faithfully, with
no inference and no summarization.

[中文说明](README.zh-CN.md)

## How it works

- Stop hook (after every AI response) + SessionEnd hook (flush at session end)
  → hooks/record-session.py → parses the session transcript (JSONL)
- Recorded: every question you typed, verbatim, plus the AI's final text
  answer for each question — in whatever language you wrote it
- Not recorded: thinking blocks, tool calls, file contents, intermediate
  output, subagent conversations
- An answer still in progress (ending in a tool call) is deferred until it
  completes
- The raw transcript stays in `~/.claude/projects/`; this plugin copies nothing

## Requirements

- Claude Code — tested on 2.1.260, **Linux only**
- Python 3.7+ (stdlib only, no third-party dependencies)
- macOS is expected to work (POSIX `fcntl`) but has not been tested
- Windows is not supported

## Install (once per machine)

Via the plugin marketplace (recommended):

```bash
claude plugin marketplace add didongke/ai-tracedoc
claude plugin install ai-tracedoc@ai-tracedoc
```

Or manually:

```bash
git clone https://github.com/didongke/ai-tracedoc.git
cd ai-tracedoc
mkdir -p ~/.claude/skills/ai-tracedoc
cp -r .claude-plugin hooks src ~/.claude/skills/ai-tracedoc/
```

Verify:

```bash
claude plugin list                  # expect ai-tracedoc, Status ✔ enabled
claude plugin details ai-tracedoc   # Hooks (2) SessionEnd, Stop
```

## Enable recording (once per project)

```bash
touch .tracedoc-on
```

Questions are recorded as they happen (after each AI answer), with a final
flush at session end. Nothing is written until both switches are on.

## The ledger

- Location: project root, one growing file per project
- Naming: `<YYYYMMDD>-<project-dir>-tracedoc.md`, e.g.
  `20260906-my-project-tracedoc.md`
- Past 200 KB, new volumes continue as `...-tracedoc-02.md`, `-03.md`, …
  with "Continued in / Continued from" links; a session never splits
  across volumes
- Entry format:

```markdown
## 2026-09-06 · Session title

**Question:** 2026-09-06 14:32 · Why was this designed this way?
**Answer:** …the AI's final answer, verbatim…
```

## Suggested .gitignore

```gitignore
*tracedoc.md
.tracedoc-state.json
.tracedoc.lock
```

The ledger is not committed by default (it may contain sensitive Q&A).
Commit selected files only if you intend to share them.

## Notes

- Entries are written during the session, so the exit method (`/exit`,
  Ctrl+D, closing the terminal, killing the process) does not affect what
  has already been recorded; at most the very last unanswered question may
  be missing
- Headless sessions (`claude -p`) are recorded too (the Stop hook fires there)
- Ledger content is verbatim excerpts of AI answers and may contain code or
  secrets — review before sharing
- The plugin does not read or record environment variables
- Recording relies on Claude Code's internal transcript format, which is
  not a public API — after a Claude Code upgrade, verify with `--self-test`
  (see Development) if entries stop appearing

## Development

Run the test suite:

```bash
python3 -m unittest discover -s tests -v
```

Verify the extraction pipeline against a real transcript without touching
any ledger:

```bash
python3 hooks/record-session.py --self-test ~/.claude/projects/<project-slug>/<session-id>.jsonl
```

The core layer (`src/tracedoc/`) is agent-agnostic; only
`hooks/` is Claude Code specific.

## Roadmap

- Analysis skill: read ledger + code, answer "why was this decided this way"
- Other agents (Codex, …): the core layer is agent-agnostic, adapters TBD
