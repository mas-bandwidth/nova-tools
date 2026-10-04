# nova-bus USE rating, nova-tools 1.1.0

Rater: deepseek-v4.1-flash
Build: 7128aee5a5f4
Score: 7.5/10

## Reasons
nova-bus works end to end on a real bus and needs no infrastructure: a bare origin
and two clones in a scratch directory carried a note, a reply, a receipt, a group
send, a prepare/send pair, a wait, a check and a close. Its refusals name the
problem, the value wanted and the next command, and its help is unusually complete.
The score is held down by three things a cold AI meets: no verb offers --json, so
every result must be parsed from prose; a first `inbox --advance` reads the local
checkout before it fetches, so a bus that moved reports nothing new and writes the
cursor behind the note; and a `receipt --note` path is resolved against the bus
root only while help says just `by id or by path`.
A 10 needs --json on every verb from the one result value, an --advance that
fetches before it walks, a receipt path scope that is stated or accepts the paths a
reader has, and a help `example:` block that runs cold.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-bus check --bus bus --full --json` | Every verb refuses --json with exit 2 `unknown flag --json`, and the help never offers it; an AI must parse the human lines to act on any result. | Render the one result value as JSON as well as lines on every verb, and name --json in help. | L |
| 2 | `nova-bus inbox --bus bus --as Ada --receipt-max-words 20 --advance --remote origin --branch main` | Reads the local checkout before it fetches: a first --advance on a bus that moved prints `changed=0` and writes the cursor at the stale HEAD while the remote held the reply; the note appears only on the next run. | With --advance and --remote, fetch and fast-forward before the walk so the cursor and the listing describe the fetched HEAD. | M |
| 3 | `nova-bus receipt --bus cloneB --as Bo --note cloneB/from-ada/2026-10-04T0424Z-third-49ffb865902d.md --remote origin --branch main --dry-run` | A --note path is resolved against the bus root only: a cwd-relative or absolute path to a real note exits 1 as `is neither an id on this bus nor a note that exists`, while help says only `by id or by path`. | State that the path is relative to the bus root, or resolve it against both roots, and name the accepted shape in the refusal. | S |
| 4 | `nova-bus names --bus ./bus` | The help `example:` block is not runnable cold: its first line exits 2 until the separate setup prose has run, and that prose carries a `<the note goes here>` placeholder. | Put the setup lines inside the example block, or add a quickstart verb that writes a fixture bus. | M |
| 5 | `nova-bus send -h` | The --host flag prints as `<host=>`, a type that names no shape, and every verb repeats it. | Give --host a real type name in the flag table. | S |

## Good, keep
The `how it works:` paragraph names a bus, a lane, a note, a cursor and where they
live, and each refusal names every problem at once with a `run:` command.
`wait` documents the harness that cannot loop, its --idle-exit codes and the
`next=` rearm line, so a caller keeps no clock of its own.
`--bodies` frames each body with a byte count, and `prepare` gives a deterministic
id and a saved artifact that makes a retry safe.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| inbox reads a stale checkout without saying so | STILL THERE | `nova-bus inbox --bus bus --as Ada --receipt-max-words 20 --advance --remote origin --branch main` prints `INBOX SCOPE mode=full cursor=- changed=0` and `INBOX CURSOR commit=b6387dd` while the remote held the reply; the next run names it. |
| inbox --advance --bodies does not advance | FIXED | `nova-bus inbox --bus cloneB --as Bo --receipt-max-words 20 --advance --bodies --remote origin --branch main` prints `INBOX CURSOR commit=f5f82dd pushed=true attempts=1`, and `git show HEAD:from-bo/CURSOR` holds f5f82dd. |
| --advance pulls unread notes in unmentioned | FIXED | the same run prints `INBOX NOTE id=ada-49ffb865902d from=Ada addr=to ... third` before `INBOX CURSOR`, so the new note is named on the run that advances. |
| the receipt path scope is undocumented and misleading on absolute paths | STILL THERE | `nova-bus receipt --bus cloneB --as Bo --note /abs/path/third-49ffb865902d.md --remote origin --branch main --dry-run` exits 1 with `is neither an id on this bus nor a note that exists`, though the file is in the checkout. |