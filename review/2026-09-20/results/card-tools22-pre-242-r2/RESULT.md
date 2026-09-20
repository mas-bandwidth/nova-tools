RESULT tools22-pre-242-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#242 at head 98348858408b: nova-daemon: SPEC draft 1
PREREAD 242 claims=15 proven=0 unproven=15 defects=3 high=0

PR 242
HEAD 98348858408b60a5b578d945527fadda84cf5b6b
BASE dev
MERGE-BASE 31e351956a59a63e3da1b323abecb63da7b92cdd
BEHIND 39
FILES 1 production, 0 test
LINES +1677 -0

This PR is a single document, docs/SPEC-DAEMON.md (1677 lines, the only file in the
diff). The tree contains no nova-daemon code and no nova-daemon test anywhere
(grep for nova-daemon in the tree hits docs/NEXT-TOOLS.md only; the SPEC.md
Conventions, internal/oneline and internal/bounded it leans on do exist). Every
behavioural claim below is therefore UNPROVEN: nothing in this diff or in the
tree witnesses it, and the document says so itself — line 1674-1677 calls it a
proposal not ratified, and rule 5 marks every systemd claim UNVERIFIED with a
linux walk and a darwin measurement as gates on ratification.

1. One binary at the unit layer renders, installs, checks and repairs launchd agents and systemd user units from one declaration file kept in git, and stamps every run so a unit that ran and failed is never confused with one that never fired. UNPROVEN (docs/SPEC-DAEMON.md:3-7) — no code or test exists.
2. A unit is ten tab-separated fields with nothing implied, the header must equal the spec's byte for byte, and a wrong field count or empty field is a refusal exit 2, never a skip. UNPROVEN (docs/SPEC-DAEMON.md:71-92).
3. The command is argv split on single spaces, executed with no shell, no pipe, no glob, no environment expansion; adjacent, leading or trailing spaces refuse at load. UNPROVEN (docs/SPEC-DAEMON.md:94-101).
4. render writes nothing and runs nothing and is the only producer of unit text; install renders and then loads. UNPROVEN (docs/SPEC-DAEMON.md:103-108).
5. --sandbox is required on render/install/check (refused exit 2, reason=no_tool when absent) and check compares exactly four named argv fields of the loaded unit — argv[0], --sandbox, --secrets-exec, every --secrets-arg — reporting WRONG-ARGV, exit 1, on any difference. UNPROVEN (docs/SPEC-DAEMON.md:430-447).
6. A deadline is a wall, not a guard: stdin is /dev/null, the command is a process-group leader, and the deadline is TERM then KILL after a 10-second grace; for a role and a consumer the expiry stamps TIMEOUT, while a daemon's stamps RECYCLED and run exits 0. UNPROVEN (docs/SPEC-DAEMON.md:271-307).
7. A daemon whose two most recent recycles ended RECYCLED with the same log_bytes — zero twice or the same number twice — is verdict IDLE, a failure, exit 1. UNPROVEN (docs/SPEC-DAEMON.md:320-333).
8. A run that exits 0 with its process group still populated is LEFTWORK, a fourth state, and run then TERM/KILLs the group and reports how many it killed. UNPROVEN (docs/SPEC-DAEMON.md:335-349).
9. check compares DECLARED against LOADED (read from the loader, never from this tool's memory or the installed file) against FIRED, WRONG-WHEN when declared and loaded schedules differ, and CAUGHT-UP reason=powered_down|unproved is a counted non-failure at exit 0. UNPROVEN (docs/SPEC-DAEMON.md:463-552).
10. The heartbeat is exactly one declared consumer whose command resolves — EvalSymlinks then os.SameFile — to the supervisor with first argument heartbeat; a second is refused at load, and check fails BEAT reason=no_beat|too_slow|stalled. UNPROVEN (docs/SPEC-DAEMON.md:673-723).
11. A refusal over a running unit records the owed act in state/owed.json with the refused install's own flags verbatim, retried by the heartbeat with the content re-derived at repair time; an owed install whose record carries no --sandbox is refused BEAT FAIL owed_no_argv, never guessed. UNPROVEN (docs/SPEC-DAEMON.md:725-768).
12. run writes the start stamp before the child starts and the end stamp after reaping, appends runs.tsv pruned to --keep-runs, and the stamp carries unit, line, kind, fired_for, started_at, ended_at, exit, signal, timed_out, recycled, work_outstanding, deadline_sec, wall, bin_sha256, self_sha256, file_sha256, booted_at, log, log_bytes and env_names — names, never values. UNPROVEN (docs/SPEC-DAEMON.md:777-784).
13. run's exit status is the command's 0-124, 0 also a daemon's recycle, 71 is documented as darwin's sandbox-exec exec failure, 125 is RUN REFUSED, 126, 127, and 128+N for a role or consumer killed by signal — a daemon's recycle is never 128+N. UNPROVEN (docs/SPEC-DAEMON.md:1028-1048).
14. Every rendered file is byte-identical run to run for an unchanged declaration with the environment in sorted key order; every launchd.plist(5) quote was read verbatim on a darwin box, and every systemd claim is UNVERIFIED, with walking the linux leg and measuring launchctl bootout/bootstrap under the narrowed profile as gates on ratification. UNPROVEN (docs/SPEC-DAEMON.md:152-185, 1427-1435) — the platform quotes and measurements are claimed, not witnessed here.
15. Fifty red-first tests are demanded and none of them exist in the tree. UNPROVEN (docs/SPEC-DAEMON.md:1343-1641) — this is a list of demands, and no test in the diff or the tree asserts any rule of this spec.

DEFECT low docs/SPEC-DAEMON.md:805 and 1281 — two dangling cross-references: rule 22's file_sha256 rationale points at "open question 3" and the "not-persistent" clause points at "open question 2", but the Open questions section numbers only question 1 and folds draft 1's other two questions into unnumbered closed bullets — an implementer tracing either reference finds nothing — number the two closed questions 2 and 3 (or reword the references to the bullets).
DEFECT low docs/SPEC-DAEMON.md:1419 — in the demanded-tests list test 19c is printed before 19a and 19b, so the suffixes are out of order and a reader looking for test 19b (a ratification gate that rule 11's wall=none-for-the-beat prescription depends on) finds it after 19c — reorder to 19, 19a, 19b, 19c.
DEFECT low docs/SPEC-DAEMON.md:1651 — "a box-level unit that survives with no login (a linux `LaunchDaemon` equivalent, `loginctl enable-linger`, a system unit)" conflates three things in the one open question about platform vocabulary: `loginctl enable-linger` is the per-user mechanism the same question says is owed work (a systemd --user unit does not survive logout without it), not a box-level unit, and "a linux LaunchDaemon equivalent" and "a system unit" name the same shape twice — name the box-level shape once and drop linger from the parenthetical.

1. This repo's other SPEC-*.md files (SPEC-SANDBOX, SPEC-SECRETS, SPEC-MERGE) each sit beside an implementation and its tests. Is a spec-only PR with zero code and zero of its 50 demanded tests the intended shape for nova-daemon's first landing, i.e. should SPEC-DAEMON.md be ratified and merged before any implementation exists?
2. Rule 5 and test 19b make walking the linux leg and measuring launchctl bootout/bootstrap under the narrowed sandbox profile gates on ratification. Who owns those two measurements, and if the darwin one reports denied, does rule 11's wall=none-for-the-beat prescription and test 19c's one-invocation mixed estate need rewriting before APPROVE?
3. Rule 20 stores the refused install's --secrets-arg values — the secret store path, key path and sops path — verbatim in owed.json, and rule 22 puts them in every stamp. Rule 7 says the unit files are world-readable and promises "never a value"; are the store/key paths themselves considered sensitive enough to protect, and is the state directory's permission the protection or is it unstated?
4. The document claims every launchd.plist(5) quote was "read verbatim from the man page on a darwin box by two independent cold reads." Can Emma and Johnny, as the named readers, confirm those reads ran on a darwin box, and is a third independent read of the quoted passages wanted before ratification?

Left owed — I read the entire diff (one file, docs/SPEC-DAEMON.md, 1677 lines) in full. I sampled rather than read in full the companion documents it cites (SPEC.md, SPEC-SANDBOX.md, SPEC-SECRETS.md, SPEC-MERGE.md, docs/NEXT-TOOLS.md, docs/CLI.md) and confirmed the existence of internal/oneline and internal/bounded; I did not re-read the 39 origin/dev commits between the merge base and the card's base, which this PR does not touch. There is no production code and no test file in the diff, so nothing else in the PR was left unread.

git status --short:
(clean — nothing printed)
git rev-parse HEAD:
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-242-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-242-r1	1	2026-09-20T19:28:34Z	2026-09-20T19:37:24Z	0	opencode	deepseek-v4-flash	55182	22740	0	752640	0	0.0352
