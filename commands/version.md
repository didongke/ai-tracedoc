---
description: Show which version of TraceDoc is running
disable-model-invocation: true
allowed-tools: Bash(basename "${CLAUDE_PLUGIN_ROOT}")
---

<!-- The version is the install directory's name, not a field read out of
     plugin.json. Claude Code refuses an injected command that names a path
     under ~/.claude/, and an installed plugin's root is always under one. That
     check outranks allow rules, and it is keyed on the path rather than on what
     the command does: `sed -n ...p` on plugin.json is reported as an edit of a
     sensitive file, because sed counts as write-capable. Splitting the read
     into grep|cut is no better -- a compound command is checked part by part,
     and an injected command never gets to prompt, so a part that would ask
     aborts the whole invocation. Both forms failed on a real 0.3.3 install on
     2026-10-03. A marketplace install always lands in a directory named after
     its version, and a directory name opens nothing -- which is what makes the
     running directory's name the version of the running copy. Loaded out of a
     working tree it prints that directory's name instead: the one case this is
     wrong, and one no installed user is in. Do not put the read back. -->

!`basename "${CLAUDE_PLUGIN_ROOT}"`

Reply with that version and nothing else.

One line. No JSON, no prose, no tool calls, no edits to any file. This is a
lookup, so the only correct response is the number that came back — a version
check that writes to the repository is not a version check.
