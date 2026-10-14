# Grok hook adapter

`nova-hook hook --harness grok` installs this adapter, then arms the hook record. The flag is the existing hook verb. There is no second command.

The home is `--grok-home`, else `GROK_HOME`, else the grok directory under the user home. The path is absolute and clean, and it carries no shell metacharacters. Install writes three files before the hook record:

- `hooks/nova-hook.json`, global hooks that record busy on `UserPromptSubmit` and idle on `Stop`, `StopFailure`, `StopCancelled`, and `Notification` with matcher `idle_prompt`.
- `hooks/nova-hook-grok.sh`, which writes only `busy` or `idle` into `<home>/nova-hook/state`. It never sends or acks, and it always exits 0.
- `nova-hook.wake`, created at mode 0600 when absent and never truncated.

One monitor watches that wake file (`tail -n 0 -F` on it) or this process, not both. A line on the wake file wakes an idle session and reaches a running turn.

At most one notice is pending (`tla/GrokHookNotice.tla`). Messages that arrive while that notice is outstanding, or while a turn runs, fold into it. The next line is `1 more message; read the bus`, or `N more messages; read the bus`. A burst is one line. The message body and subject stay on the bus. The model reads the bus. A failed write puts the count back and stops this process; it does not drop the hook record.

`--dry-run` writes nothing and does not arm. `--once` installs and arms, and does not stream. A role that does not hold the bus push is refused before install. The client's own challenge lines stay on this process's stdout. A challenge that arrives as a bus message folds into the notice like any other message.

The bench configurations for `GrokHookNotice` are owed. No cfg is in the tree until that run.
