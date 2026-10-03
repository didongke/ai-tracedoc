---
description: Show which version of TraceDoc is running
disable-model-invocation: true
---

!`sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "${CLAUDE_PLUGIN_ROOT}/.claude-plugin/plugin.json"`

Reply with that version and nothing else.

One line. No JSON, no prose, no tool calls, no edits to any file. This is a
lookup, so the only correct response is the number that came back — a version
check that writes to the repository is not a version check.
