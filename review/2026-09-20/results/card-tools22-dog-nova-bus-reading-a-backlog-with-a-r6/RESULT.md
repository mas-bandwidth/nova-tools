RESULT tools22-dog-nova-bus-reading-a-backlog-with-a-r6 sha=5298f6be12ea — read `nova-bus Reading a backlog with a typed decision` against docs/CLI.md:556-559 and say CLEAN or DRIFT
BROKEN the installed nova-bus / nova-version binaries cannot be executed on this machine (execve(2) fails EACCES despite mode 0755 owned by uid 1000), so the section could not be run at all
```
git rev-parse HEAD = 5298f6be12eaa0f7e6622334d2b6a1eb427649e3 (matches card base)
nova-version = FAILED: execve EACCES (Permission denied); installed tool not runnable, sha unknown and cannot be confirmed
```

- `TOOL nova-bus`, `VERB Reading a backlog with a typed decision`, `DOC docs/CLI.md:556-559`, `REPLICA 6 of 24`,
  `BUILD unknown — nova-version could not be executed`

## Table of command blocks

The section under test (`docs/CLI.md:556-559`) is prose describing the `--decide` feature of `nova-bus`;
it contains no fenced command blocks, so there are no commands to run. There is therefore no per-command
table. The tool that would exercise it, `nova-bus` (and its build-verifier `nova-version`), cannot be
executed on this machine.

| n | command | exit | verdict |
|---|---------|------|---------|
| 1 | `nova-version` (STEP 1 build check) | 1 | BROKEN — execve EACCES, not runnable |
| 2 | `nova-bus` (the documented tool, invoked per section) | 1 | BROKEN — execve EACCES, not runnable |

## BROKEN

The installed `nova-bus` and `nova-version` binaries under `/home/nova/.local/bin/` cannot be executed.
Even though `ls -l` shows `-rwxr-xr-x 1 nova nova` and `access(X_OK)` reports success in strace, the actual
`execve(2)` returns `-1 EACCES (Permission denied)`. Exact failure:

```
execve("/home/nova/.local/bin/nova-bus", ["/home/nova/.local/bin/nova-bus"], ...) = -1 EACCES (Permission denied)
strace: exec: Permission denied
+++ exited with 1 +++
```

and the same for `/home/nova/.local/bin/nova-version`.

Because the executable that this card exists to read against cannot run on this machine, the whole section
is unexercisable. This is a machine/sandbox-level execution refusal of the tool itself, not a content
disagreement between the document and tool output, so it is BROKEN rather than DRIFT or SKIP: the tool
could not run at all.

## RAN 0, SKIPPED 0 (no fenced command blocks exist in the section), BROKEN 2 (build-check and tool could not run)

## Left owed
- The build identity of the installed tool is unverifiable: `nova-version` (sha) could not be read because
  the binary refuses to execute.
- Every `nova-bus --decide` invocation described (typed decision, redaction of `sk-` keys, `kind`/`needs_reply`/
  `blocked`/`conf`/`wake`/`owner`/`ref` output fields, trailing `INBOX DECIDED n=` summary, `--floor`, `--key-env`,
  `--base-url`, `--allow-private`) could not be run or compared against the document's claims.
