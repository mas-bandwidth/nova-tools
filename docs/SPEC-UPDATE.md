# nova-update — specification (draft 1)

Status: **draft 1, proposed, not implemented.** This is the first slice of
issue #89. Glenn, 2026-09-12: *"there are probably new versions we should check
for everything for updates regularly."* The answer is a tool, not a habit: one
`check` that reads each dependency's own source, and an `apply` that changes
exactly one named thing on a person's word. Spec first, per the law.

`nova-update` is this estate's first tool that reads somebody else's server on a
schedule. [SPEC.md](SPEC.md)'s **Conventions** govern it — exit codes, the
one-line field grammar, `internal/oneline`, `internal/bounded`, no guessed
paths, the cap-and-count law — and this file says only what is more. It has no
clock of its own: no daemon, no timer, no `--watch`, no state file; the estate
runs it nightly and reads the counts in the morning. Nothing reacts to its exit
code, and no verdict of `check` ever starts an `apply`.

## Purpose

A window that coordinates other lines wants to know when something it depends on
is behind. Today that is a search of release pages by hand, or a nightly that
reads a version file and nothing else. `nova-update check` reads the world once,
bounded, and prints one row per dependency with its installed and its latest, so
the morning line carries the count of things behind. `nova-update apply <name>`
is the person's word that one of those things actually changes.

## The dependency file — a config file, never discovered

The list of dependencies is one text file kept in git, named by a flag. There is
no default path, no search of the working directory, no `$HOME`, no scan, no
wildcard discovery: a missing `--file` is `refusing to guess`, exit 2. A change
to what we depend on is therefore a diff somebody read.

One header line, then one tab-separated entry per line; `#` opens a comment,
and nothing else is skipped.

```
name<TAB>installed<TAB>latest<TAB>owner
```

- `name` is the one string `apply` accepts, byte for byte.
- `installed` is a command whose first line carries this box's version.
- `latest` is a source, `<scheme>:<locator>`.
- `owner` is the person who answers when the entry is behind.

`installed` is argv, never a shell: no pipe, no glob, no `&&`, no environment
expansion, and an argument needing a space is refused at load time with the
remedy *put it in a script and name the script* (the estate's field law).
`latest` is one of a small set of bounded sources, each a single GET that
answers with the newest version: `github:owner/repo` (the releases `tag_name`),
`npm:package` (the `/latest` `version`), `brew:formula` (`versions.stable`),
and `local:<argv>` (a command run here). One GET per entry, no pagination, no
redirect beyond three hops, the body capped at 256 KB, a body reaching the cap
being UNKNOWN rather than parsed from a prefix. **No credentials**: a source
needing a token is a new spec.

## The verbs

```
nova-update check --file <path> [--max <n>] [--timeout <d>] [--source-timeout <d>]
nova-update apply --file <path> <name> [--version <v>] [--timeout <d>]
nova-update help
```

`check` reads every entry's installed version here and its latest from the
entry's own source, and prints one bounded row per dependency: `name=`,
`installed=`, `latest=`, `source=`. Then a summary line that always prints,
success or failure:

```
UPDATE OK n=<n> behind=<n> took=<d> file=<path>
```

`n=` is the number of entries read, `behind=` the number whose latest is newer
than installed. Exit 0 when `behind=0`; exit 1 when anything is behind, new,
different or unknown — a failure never reads as *up to date*.

`apply <name>` installs exactly the one thing named, the way the file says, on
a person's word, and prints one line:

```
UPDATE APPLIED <name> <from> -> <to>
```

`<from>` is the installed version before, `<to>` the version installed (the
latest the source reported, or `--version <v>` when the person named one). An
`apply` with no name is a refusal, exit 2. There is no `--all`, no `--stale`,
no glob, no `-y`: one run, one name, and a verdict of `check` is never a name.
The `{version}` token in the entry's apply command, wherever it appears, is the
only interpolation; a latest the source did not answer, with no `--version`, is
a refusal and no process starts.

## Refusals

Every refusal names its remedy, and the next thing to type is on the line:

- no `--file`, or one that does not exist — exit 2, `refusing to guess`,
  naming the flag or the path;
- a malformed file line — exit 2, naming the line number;
- `apply` with no name — exit 2, *a name is required*;
- `apply` of a name the file does not carry, byte for byte — exit 2, naming the
  file and its entry count;
- `apply` of an entry the file marks not to install — exit 2, naming the entry;
- `--max` negative, an unknown source scheme, or an argv needing a space — each
  exit 2, naming the value and the remedy.

## Bounds

- `--max <n>` bounds the rows printed, default 20, `0` means all, a negative is
  refused; a capped verdict gets one `UPDATE MORE` line with its true total.
- `--timeout <d>` bounds one source read, default `5s`; `--source-timeout <d>`
  bounds the whole run, default `60s`; at most four reads are in flight at once.
  A source that does not answer inside its timeout is UNKNOWN, never OK, never
  retried and never cached: a cache would turn tonight's failure into a green.
  Entries not reached inside the source-timeout are UNKNOWN, reason `budget`,
  and the run still prints its count line and exits 1 (the two-minute rule).

## The output grammar

Per SPEC.md's one-line law. `UPDATE` is the first token; `OK`, `APPLIED`,
`MORE` and `REFUSED` are the rest, and every value is one `internal/oneline`
token; `REFUSED` goes to stderr, the rest to stdout. `UPDATE OK` is the last
line of `check`.

```
UPDATE OK n=<n> behind=<n> took=<d> file=<path>
UPDATE <STALE|NEWER|DIFFERENT|UNKNOWN> name=<name> installed=<v|-> latest=<v|-> source=<source> owner=<owner>
UPDATE MORE kind=<stale|newer|differ|unknown> shown=<n> total=<t> <remedy>
UPDATE APPLIED <name> <from> -> <to>
UPDATE REFUSED: <reason> (<remedy>)
UPDATE APPLY REFUSED <name>: <reason> (<remedy>)
```

`STALE` is the line for a verified older installed version, and for nothing
else; `NEWER` and `DIFFERENT` print under their own names; `UNKNOWN` is a source
that did not answer or a version that could not be read; a person looks; not
`behind=0` is exit 1.

## What this draft does not do

- **No report verb, no recipients, no bus, no snapshot.** This draft is `check`
  and `apply`, nothing that sends, and no state file of its own.
- **No models.** A weight in the ollama library is listed, never pulled: an
  `apply` of a model is a refusal, and the owner pulls it by their own step.
- **No pins between our own tools, no `kind` table.** Every dependency is one
  row; nothing is derived from another row.
- **No scheduling.** No daemon, no `--watch`, no loop, no timer: the estate runs
  it nightly, and a run ends inside its `--source-timeout`.
- **No silent install.** Nothing applies without a name, and no verdict of
  `check` is a name; `--all`, `--stale` and `-y` do not exist.
- **No guessed paths and no cache.** No `$HOME` read, no search of the working
  directory, no remembered answer: a dead source is asked again next run.

## Exit codes

Per SPEC.md: **0** — everything current, or an `apply` that left the box on the
target; **1** — anything `behind`, `newer`, `different` or `unknown`, or an
`apply` whose after is not the target; **2** — could not run: every refusal the
rules name.
