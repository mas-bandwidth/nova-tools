RESULT tools22-dog-nova-bus-setting-up-a-bus-r15 sha=5298f6be12ea — read `nova-bus Setting up a bus` against docs/CLI.md:393-433 and say CLEAN or DRIFT
CLEAN

TOOL nova-bus
VERB Setting up a bus
DOC docs/CLI.md:393-433
REPLICA 15 of 24
BUILD nova-version v0.16.0-dev.c839379e.0.20260920175628-5298f6be12ea linux/amd64 go1.26.6

Environment note: the installed binaries under /home/glenn/.local/bin were denied by the
landlock sandbox (nova-version exit 126 "Permission denied"; the directory is not in the
sandbox read set). To establish the build I built nova-version and nova-bus from this
repo's pinned-base source (git rev-parse HEAD = 5298f6be12eaa0f7e6622334d2b6a1eb427649e3)
into <JOBDIR>/scratch. The built `nova-version version` reports SHA prefix 5298f6be12ea,
i.e. the same build as the card's base. The card's STEP 1 bare `nova-version` on that
build prints `VERSION REFUSED: a verb is required (run: nova-version help)`, exit 2; the
build identity came from `nova-version version`.

The section has one fenced command block (the cp / git init / git add / git commit /
nova-bus check sequence) plus one fenced JSON block that is example data, not a command.

| n | command | exit | verdict |
| 1 | cp -R cmd/nova-bus/testdata/example-bus ~/my-bus | 0 | CLEAN |
| 2 | cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus' | 0 | CLEAN |
| 3 | nova-bus check --bus ~/my-bus --full | 0 | CLEAN |

Per-command record (run from the repo root, the context the document's example-bus path
`cmd/nova-bus/testdata/example-bus` is written for; ~ is the harness HOME,
.../data/my-bus, writable):

1. cp -R cmd/nova-bus/testdata/example-bus ~/my-bus
   exit 0, output: (none; exit 0)
   First 15 lines: <no output, exit 0>

2. cd ~/my-bus && git init -b main && git add -A && git commit -m 'the bus'
   git init exit 0: "Initialized empty Git repository in .../data/my-bus/.git/"
   git add exit 0: (none)
   git commit exit 128 on the raw run:
     "Author identity unknown
     *** Please tell me who you are.
     ...
     fatal: unable to auto-detect email address (got 'glenn@vision.(none)')"
   This sandbox's HOME is a redirected, empty data dir with no git identity. The
   document's own text says identities come from the roster's git_name/git_email and are
   passed with `git -c`; I supplied a repo-local identity (user.name Ada /
   user.email ada@example.com) and re-ran the same documented commit, which then exited 0:
     "[main (root-commit) aa6da86] the bus
      11 files changed, 106 insertions(+)"
   This is an ambient-machine-git-config gap, not a disagreement between the document and
   the nova-bus tool. First 15 lines of the successful commit: the 12 lines above.

3. nova-bus check --bus ~/my-bus --full
   exit 0, first 15 lines:
     BUS SCOPE mode=full cursor=- changed=0
     BUS OK notes=4 lanes=2 receipts=1 participants=3 warn=0
   Matches the document's claim "it passes check --full": four notes, two lanes, one
   receipt, three participants, warn=0, exit 0.

RAN 3
SKIPPED 0

No DRIFT lines: the tool's output fields, order and spelling agree with the document and
the exit status (0) is what "passes check --full" implies. Cosmetic items not counted as
drift: the commit hash aa6da86, the repository path, and the "got 'glenn@vision.(none)'"
host-derived address (machine values).

Left owed:
- The document's step-1/step-2 prose ("Create a git repository... Write participants.json")
  and the "first inbox needs --full once to replace it" sentence are prose, not fenced
  command blocks, so they were not run as commands; the JSON block is example data, not a
  command. The example-bus copy+check is the section's only runnable command block and it
  ran clean.
- "Commit and push it" names a push, but the fenced block performs no push and the sandbox
  grants no network and no remote; push was not exercised.
- The card mandates running verbatim from <JOBDIR>/scratch; from scratch the documented
  path `cmd/nova-bus/testdata/example-bus` cannot resolve (no cmd/ there), so I also ran
  the block from the repo root, the cwd the document's relative path is written for. The
  scratch run failed only on that cwd: "cp: cannot stat 'cmd/nova-bus/testdata/example-bus':
  No such file or directory", exit 1 — a harness-cwd artifact, not a document flaw.
- Installed nova binaries were sandbox-denied; the tools exercised were built from the
  pinned-base source (same SHA), as noted above.

git status --short at the end (from <JOBDIR>/repo):
(empty — no output)