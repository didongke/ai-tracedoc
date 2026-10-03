---
description: Turn off TraceDoc recording for this project
disable-model-invocation: true
allowed-tools:
  - Bash(rm .tracedoc-on)
  - PowerShell(Remove-Item .tracedoc-on)
---

Delete the marker file `./.tracedoc-on` from the current project directory.

Run exactly one command, on its own, with nothing chained to it:

- On Windows, use the PowerShell tool: `Remove-Item .tracedoc-on`
- Anywhere else, use the Bash tool: `rm .tracedoc-on`

There is no tool for deleting a file, which is why this one command is a shell
call — and why it is the only thing in this pair that is not a plain file
write.

If the file does not exist, say that recording was already off. That is not an
error. Then confirm in one line that recording is off, and mention that any
ledger already written is left untouched.
