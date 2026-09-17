# nova-version — specification

`nova-version moved` writes the TOOLS MOVED note from two revisions' binaries, and
`nova-update apply --sha` builds the whole `cmd/*` set at one revision into one stamped
bin. SPEC.md's **Conventions** govern — exit codes, the one-line grammar, the field law,
no guessed paths — and [SPEC-UPDATE.md](SPEC-UPDATE.md) holds the manifest verbs this file
does not restate. `help` prints these two lines, byte for byte:

```
nova-version moved --from <sha> --to <sha> --repo <dir> --out <path>
nova-update apply --sha <sha> --repo <dir> --bin <dir> [--timeout <d>]
```

## nova-version moved and nova-update apply --sha

1. **Every path and revision comes from a flag.** `--repo` is the checkout, `--out` the
   note and `--bin` the binary directory; a missing one is *refusing to guess*, exit 2. A
   revision resolves in `--repo` alone — never the cwd, never `origin/HEAD`.
2. **`moved` reads each revision's binaries, never a hand-written list.** For every
   `cmd/*` it builds both revisions, runs each `<tool> help`, and parses the verbs and
   flags the help prints; the note is the added, removed and renamed sets that diff
   yields, so a flag on no binary's help can never be announced.
3. **`moved` reads the two helps and writes the note to `--out`.** It prints one line to
   stdout naming every field: `MOVED OK from=<sha> to=<sha> added=<n> removed=<n>
   renamed=<n> verbs=<n> file=<path>`; an empty diff is `added=0 removed=0 renamed=0`,
   exit 0, never a refusal.
4. **`moved` refuses, exit 2, one remedy each.** A missing flag is *refusing to guess*,
   naming it; a revision that is not a commit in `--repo` names the revision and the
   `git fetch` that would bring it; a `cmd/*` that builds but answers no help names the
   tool, the revision and the build to repair there.
5. **`apply --sha` is `apply` in its build mode.** With `--sha` it reads no `--file` and
   takes no name: it builds `./cmd/...` at the revision under that revision's one stamp,
   reads every built binary's `version` back, and installs the set; `--sha` given with
   `--file` is a refusal naming both flags.
6. **`apply --sha` prints one line, every field named.** `APPLY SHA sha=<sha> bin=<dir>
   stamp=<stamp> built=<n> took=<d>`; `built=` is the number of binaries installed and
   `stamp=` the identity every one of them reports.
7. **`apply --sha` refuses a mixed set, naming the pair.** Two binaries at different
   stamps — a lost linker symbol answers `devel` while the rest answer the revision — are
   refused, exit 2, naming both binaries and both stamps, and nothing is installed; the
   staged build is discarded, so `--bin` is never left mixed.
8. **The rest of the `apply --sha` refusals.** A missing `--sha`, `--repo` or `--bin` is
   *refusing to guess*, naming it; an unresolved revision names it and the fetch; a
   `cmd/*` that does not build names the package and the revision.
9. **The mistake `moved` removes, in one sentence.** The ADOPT EVERYTHING note named four
   `--decide` flags still on open PRs (#1141), and `moved` cannot, because every flag it
   announces was read off the binary's own help.
10. **The mistake `apply --sha` removes, in one sentence.** Friends' bins were mixed
    across stamps, and one `apply --sha` builds the whole set under one stamp and refuses
    a directory that is already mixed.
11. **Both are bounded and clockless.** Each prints one line, caps every child through
    `internal/bounded`, takes its clock from the injected seam, and touches the network
    only for the `git fetch` a named missing revision asks for.

### Red tests this section demands

Numbered, one sentence each, every fake standing where the real thing is a bench, a
network or a clock; `git`, `go` and every built binary are fakes on `PATH`.

1. `TestMovedReadsTheBuildNeverAList`: a fake `<tool> help` at `--from` without `--decide` and at `--to` with it yields `added=decide`, and a flag a hand list would name but neither help prints is absent — the mutation that matters.
2. `TestMovedRefusesAMissingFlag`: no `--repo`, and separately no `--out`, is exit 2 printing `refusing to guess`, naming the flag, and starts no build.
3. `TestMovedRefusesAnUnresolvedRevision`: a fake `git` answering "not a commit" for `--from` is exit 2 naming the revision and the fetch remedy, and writes no note.
4. `TestMovedRefusesABinaryWithNoHelp`: a fake `<tool> help` that exits non-zero, and one that prints nothing, are each exit 2 naming the tool and the revision.
5. `TestApplyShaBuildsOneStampOnAFakeBench`: a fake `go` records exactly one `-X main.version=` and every fake binary echoes it, so one `APPLY SHA` line carries `stamp=` of the revision and `built=` of the `cmd/*` count.
6. `TestApplyShaRefusesAMixedSetNamingThePair`: a fake `--bin` holding two binaries that answer two stamps is exit 2 naming both names and both stamps, and starts no build.
7. `TestApplyShaRefusesALostStamp`: a fake build whose one binary answers `devel` while the rest answer the revision is refused naming that binary and both stamps, and installs nothing.
8. `TestApplyShaIsBoundedByTheClock`: an injected clock and a fake build sleeping past `--timeout` is exit 2 with the timeout named and no partial `--bin` directory.
