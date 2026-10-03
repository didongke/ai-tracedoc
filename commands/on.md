---
description: Turn on TraceDoc recording for this project
disable-model-invocation: true
allowed-tools: Edit(./.tracedoc-on)
---

Create the file `./.tracedoc-on` in the current project directory, which is
the directory this session is running in.

Pass the path exactly as written here — relative, with the leading `./`. Do
not turn it into an absolute path and do not resolve it against anything.

Write exactly this line as the file's contents, followed by a newline:

    ai-tracedoc: recording enabled for this project

The contents are not read — the file only has to exist — so the line is there
for whoever finds it.

Then tell the user in one line that TraceDoc recording is now on for this
project, and that `/ai-tracedoc:off` turns it off again. Do not write a ledger
file yourself: the plugin does that at the end of each session.
