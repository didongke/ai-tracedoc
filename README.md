# ai-tracedoc

Automatically records Claude Code development conversations into a per-project
TraceDoc ledger: your questions and the AI's final answers — faithfully, with
no inference and no summarization.

[中文说明](README.zh-CN.md)

## How it works

- Stop hook (after every AI response) + SessionEnd hook (flush at session end)
  → bin/tracedoc → parses the session transcript (JSONL)
- Recorded: every question you typed, verbatim, plus the AI's final text
  answer for each question — in whatever language you wrote it
- Not recorded: thinking blocks, tool calls, file contents, intermediate
  output, subagent conversations
- An answer still in progress (ending in a tool call) is deferred until it
  completes
- The raw transcript stays in `~/.claude/projects/`; this plugin copies nothing

## Requirements

- Claude Code — tested on 2.1.260
- **Linux, macOS or Windows. Nothing else to install.**

The plugin ships prebuilt binaries for all six targets — Windows, Linux and
macOS, each on x86-64 and arm64 — so there is no runtime to install and no
network access at run time. On Windows the hook is launched through Git Bash,
which Claude Code already needs.

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
cp -r .claude-plugin hooks bin ~/.claude/skills/ai-tracedoc/
```

The manual route uses POSIX shell commands — on Windows, run it in Git Bash.
The marketplace route above works in any shell.

Verify:

```bash
claude plugin list                  # expect ai-tracedoc, Status ✔ enabled
claude plugin details ai-tracedoc   # Hooks (2) SessionEnd, Stop
```

## Which version is running

```
/ai-tracedoc:version
```

Prints the version of the copy Claude Code is actually running. The plugin can
be present in several places at once — the repository you are working in, the
marketplace source, and every version ever installed under
`~/.claude/plugins/cache/` — and they disagree with each other, so "the version
in plugin.json" is not one answer but several. Each install lands in a directory
named after its version, which means an update adds a path rather than
overwriting one.

That directory name is the answer, rather than a field read out of the manifest
inside it: Claude Code refuses to run a plugin's own pre-execution command
against a path under `~/.claude/`, which is where an installed copy lives. A
marketplace install is named after its version, so the name is the version —
whereas a copy loaded in place from a working tree reports that folder's name
instead.

## Enable recording (once per project)

Inside Claude Code, in the project you want recorded:

```
/ai-tracedoc:on
```

`/ai-tracedoc:off` turns it back off, leaving any ledger already written where
it is. Recording is per project and off by default, so nothing is written
anywhere you have not asked for it.

Switching it off ends the segment rather than pausing it: whatever is said
while it is off stays out of the ledger when it is switched back on, and the
ledger carries a gap there. That gap is the point of switching it off.

The switch is a file named `.tracedoc-on` in the project root, so you can also
create it yourself from a shell — whichever line below suits it:

```bash
touch .tracedoc-on               # Git Bash, macOS, Linux
type nul > .tracedoc-on          # cmd.exe
New-Item .tracedoc-on            # PowerShell
```

With the plugin's `bin` directory to hand, `tracedoc --enable` and
`tracedoc --disable` do the same thing in any shell.

Questions are recorded as they happen (after each AI answer), with a final
flush at session end. Nothing is written until both switches are on.

## The ledger

- Location: project root, one growing file per project
- Naming: `<YYYYMMDD>-<project-dir>-tracedoc.md`, e.g.
  `20260906-my-project-tracedoc.md`
- Past 200 KB, new volumes continue as `...-tracedoc-02.md`, `-03.md`, …
  with "Continued in / Continued from" links; a session never splits
  across volumes
- Entry format. The blank lines are part of it rather than cosmetic: the
  ledger is a contract with the files already on disk, so they are reproduced
  deliberately and not tidied up.

```markdown
## 2026-09-06 · Session title

<!-- session: 8f3c1a… -->

**Question:** 2026-09-06 14:32 · Why was this designed this way?


**Answer:** …the AI's final answer, verbatim…


**Question:** 2026-09-06 14:35 · A question the AI never answered
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
- On Windows, Smart App Control may refuse the unsigned binary — per file,
  intermittently, and with no per-app exclusion. The hook names the cause
  and exits non-zero rather than failing silently. Recording is interrupted
  rather than ended: the ledger resumes from the same position at the next
  hook that runs, so nothing is lost unless the refusal outlasts the
  session. Confirm with `Get-WinEvent -LogName
  Microsoft-Windows-CodeIntegrity/Operational`; the real remedy is
  Authenticode signing

## Development

Build the released binaries — all six targets, cross-compiled from whatever
machine you are on:

```bash
sh build.sh
```

Run the test suite:

```bash
go test ./...
```

`bin/` is committed, so run `build.sh` and commit the result after changing
any Go source. A test compares the committed binary against a fresh build and
fails when they disagree; without it a stale binary would ship silently, with
a green test suite.

**If you commit from Windows, restore the executable bit afterwards.** git
records that bit only where the filesystem has one, and Windows does not, so
a checkout made there ships the Linux and macOS binaries as `0644` — and
`build.sh`'s `chmod` cannot stick. The bit lives in git's index, not on disk:

```bash
git update-index --chmod=+x bin/tracedoc \
    bin/tracedoc-linux-amd64 bin/tracedoc-linux-arm64 \
    bin/tracedoc-darwin-amd64 bin/tracedoc-darwin-arm64
git ls-files -s bin/     # the five above should read 100755, the .exe 100644
```

CI checks this and fails with the same instructions, so it cannot ship
unnoticed.

Verify the extraction pipeline against a real transcript without touching
any ledger:

```bash
bin/tracedoc --self-test ~/.claude/projects/<project-slug>/<session-id>.jsonl
```

`internal/tracedoc/` is the agent-agnostic core, `cmd/tracedoc/` is the Claude
Code adapter, and `bin/tracedoc` is the shell dispatcher that picks the right
platform binary. Only `hooks/` and `cmd/` are Claude Code specific.

## Writings

A four-part essay series, *[The AI Thought Quartet](https://dev.to/didongke/series/44232)* — published on dev.to:

1. [AI Is Not a Smarter Search Engine — It's a Mirror for Your Thinking](https://dev.to/didongke/ai-is-not-a-smarter-search-engine-its-a-mirror-for-your-thinking-bci)
2. [Code Can Be Committed — But Who Owns the Thinking?](https://dev.to/didongke/code-can-be-committed-but-who-owns-the-thinking-39k9)
3. [From Recording Conversations to "Resurrecting" Thought](https://dev.to/didongke/from-recording-conversations-to-resurrecting-thought-ai-is-building-a-digital-pyramid-for-60e)
4. [The Reflector of Thought, the Accelerator of Civilization](https://dev.to/didongke/the-reflector-of-thought-the-accelerator-of-civilization-how-ai-is-reshaping-the-evolution-of-5293)

*A manifesto drafted for the future — backed by working code.*

## Roadmap

- Analysis skill: read ledger + code, answer "why was this decided this way"
- Other agents (Codex, …): the core layer is agent-agnostic, adapters TBD
