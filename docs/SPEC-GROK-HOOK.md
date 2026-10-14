# Grok hook adapter

`nova-hook hook --harness grok` installs this adapter, then arms the hook record. The flag is the existing hook verb. There is no second command.

The home is `--grok-home`, else `GROK_HOME`, else the grok directory under the user home. The path is absolute and clean. A line break is refused. Every other character is allowed, and each argument of the installed command is quoted for the shell.

Install writes three files before the hook record:

- `hooks/nova-hook.json`, global hooks that record busy on `UserPromptSubmit` and idle on `Stop`, `StopFailure`, `StopCancelled`, and `Notification` with matcher `idle_prompt`.
- `hooks/nova-hook-grok.sh`, which writes the latest word into `<home>/nova-hook/state` and, on busy, appends one mark to `<home>/nova-hook/turn` before that write. An idle write replaces the state word and leaves the turn mark. The script never sends or acks, and it always exits 0.
- `nova-hook.wake`, created at mode 0600 when absent and never truncated.

One monitor watches that wake file (`tail -n 0 -F` on it) or this process, not both. A line on the wake file wakes an idle session and reaches a running turn.

At most one notice is pending (`tla/GrokHookNotice.tla`). Messages that arrive while that notice is outstanding, or while a turn runs, fold into it. The next line is `1 more message; read the bus`, or `N more messages; read the bus`. A burst is one line. The turn file is the witness that a busy mark happened. A later idle write of the state word does not erase it, so a busy mark and an idle mark between two reads still clear the notice and the folded line is delivered. The message body and subject stay on the bus. The model reads the bus. A failed write puts the count back and stops this process; it does not drop the hook record.

`--dry-run` writes nothing and does not arm. `--once` installs and arms, and does not stream. A role that does not hold the bus push is refused before install. The client's own challenge lines stay on this process's stdout. A challenge that arrives as a bus message folds into the notice like any other message.

The bench configurations for `GrokHookNotice` are owed. No cfg is in the tree until that run.
