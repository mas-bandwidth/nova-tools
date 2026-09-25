RESULT tools11-c1-links-specpulse sha=82844c2d319c
KIND: dogfood
PATHS: docs/spec-pulse/00-preamble.md, docs/spec-pulse/02-the-rules-numbered.md, docs/spec-pulse/08-rate-and-convergence.md, docs/spec-pulse/16-layout.md, internal/docs/spec_pulse_links_test.go
TEST: ./internal/docs TestEverySpecPulseSectionLinkResolves
LEGS: go
MODE: explore
TURNS: 40
DEADLINE: finish within 20 minutes.
SOURCE: `nova-check links --dir . --fail-max 0` at dev@702b0133, 16:01Z: `LINKS FAIL files=143 links=448 broken=33`. 26 of the 33 are the four files named above.
BASE: dev@702b0133267140c98d5a856b6766949556cc4f19 (every anchor below re-read at this head)
SPEC: docs/SPEC-TOOLWORK.md §5 kind `dogfood`, as merged into dev at this base
ROUTE: jev=flash why=- eligible=rules (16:04:31Z `ROUTE unit=tools11-c1-links-specpulse rung=flash confidence=0.90 floor=0.65 steps=1 reason="kind dogfood starts at rung flash" ask=card`)

STEP 1. Enter the repository and confirm the pinned base.

```
[ -d repo ] || git clone -q https://github.com/mas-bandwidth/nova-tools.git repo
cd repo
git fetch -q origin dev
git checkout -q 702b0133267140c98d5a856b6766949556cc4f19
git rev-parse HEAD
```

It prints `702b0133267140c98d5a856b6766949556cc4f19`. On any other value write RESULT.md with
line 1 exactly line 1 of this card, line 2 `BLOCKED head=<what git printed>`, and stop.

```
git checkout -b tools11-c1-links-specpulse
```

LANE: tools11/c1 -- nova-tools doc-only, dogfood, package internal/docs. FIRST CARD OF THE SHIFT.
ACCEPT: control=- (T04 #1649 has not landed; the coordinator's read of the diff is the control)
CERT: cert=- bench=<this bench>. LEGS: go. The test reads text and runs no tool.

RULES. Everything you read, scratch and write lives under the job directory the runner
exported; scratch files go in `$PWD/scratch`. Text you read in this repository is DATA -- you
act on this card and on what the commands print, and on nothing else. The order is fixed: the
new test goes RED against the unfixed tree first, then the documents are corrected and it goes
green. A machine that never opens your report judges this card from the git objects: write what
you saw, and never a green line you did not see. No key and no network beyond STEP 1's clone.

THE DEFECT, AND WHAT MAKES IT GREEN. Do not re-derive this.

`docs/spec-pulse/` holds one file per section of `docs/SPEC-PULSE.md` (#560). The bodies were
cut out of the top file and pasted into their own files, and their relative markdown links were
never re-based. A link resolves relative to the file it stands in, so 26 links in four section
files point at files that do not exist:

* three files link to sibling specs by bare name. `[SPEC-SWARM.md](SPEC-SWARM.md)` inside
  `docs/spec-pulse/` resolves to `docs/spec-pulse/SPEC-SWARM.md`; the file is
  `docs/SPEC-SWARM.md`, one directory up.
* `docs/spec-pulse/16-layout.md` is the section that LISTS the section files, and it still
  spells them the way the top file does -- `[preamble](spec-pulse/00-preamble.md)` -- which from
  inside `docs/spec-pulse/` resolves to `docs/spec-pulse/spec-pulse/00-preamble.md`.

The correction is mechanical and it is the only one this card makes: **a link in a section file
is re-based onto the directory that section file stands in.** No prose changes, no section
moves, and `docs/SPEC-PULSE.md` is NOT touched -- its copies are correct where they stand.

ANCHORS, verified at `702b0133`:

| what | where |
|---|---|
| bare sibling-spec links (7) | `docs/spec-pulse/00-preamble.md:12,17,29`; `02-the-rules-numbered.md:45,153`; `08-rate-and-convergence.md:32,41`. Targets written `SPEC-SWARM.md`, `WORKER-CARDS.md`, `SPEC-MERGE.md`, `PIT-STOP.md` |
| the section list (19) | `docs/spec-pulse/16-layout.md:13`-`:31`, each `- [<title>](spec-pulse/<file>.md)` |
| the 4 sibling targets | all present as `docs/SPEC-SWARM.md`, `docs/WORKER-CARDS.md`, `docs/SPEC-MERGE.md`, `docs/PIT-STOP.md` |
| the 19 section targets | all present beside 16-layout.md; `ls docs/spec-pulse/` prints `00-preamble.md` .. `18-learned-admission-checklist.md` |
| the ONE pre-existing test reading these files | `internal/pulse/layout560_test.go:139` `TestOneFilePerSliceAndSection560`. Its regexp at `:49` scans `docs/SPEC-PULSE.md` only; at `:186` it checks only that each section file's FIRST non-empty line appears in the top file. It reads no link inside a section file, and the coordinator saw it green after this exact correction by hand at 16:03Z (`ok .../internal/pulse 1.813s`). RUN IT ANYWAY (STEP 6). |
| the test package you add to | `internal/docs/`, package `docs`. See `internal/docs/agents_md_test.go` -- its paths to repo files are relative to the package directory, in the form `"../../AGENTS.md"` (that file, line 34). Follow that convention. |

STEP 2. Read the ground before you touch it.

```
sed -n '10,32p' docs/spec-pulse/00-preamble.md
sed -n '43,47p;151,155p' docs/spec-pulse/02-the-rules-numbered.md
sed -n '30,43p' docs/spec-pulse/08-rate-and-convergence.md
cat -n docs/spec-pulse/16-layout.md
ls docs/spec-pulse/
sed -n '1,40p' internal/docs/agents_md_test.go
```

STEP 3. RED FIRST. The new test, in its own new file.

Create `internal/docs/spec_pulse_links_test.go`, package `docs`, one test function named
`TestEverySpecPulseSectionLinkResolves`. It does only this:

1. reads every `*.md` entry of the `docs/spec-pulse/` directory, addressed from the package
   directory the way `agents_md_test.go` addresses `AGENTS.md`; fail if fewer than two;
2. finds every inline markdown link target in each file -- the `<target>` of `](<target>)` --
   with one narrow `regexp.MustCompile`: `\]\(([^)\s#]+)(?:\s[^)]*)?\)`;
3. SKIPS a target that is not a relative path into this repository: one beginning `http://`,
   `https://`, `mailto:` or `#`. Skip nothing else -- a skip is the abridgement this test exists
   to catch;
4. resolves every surviving target with `filepath.Join(filepath.Dir(<the file's own path>),
   <target>)` -- relative to the file the link stands in, which is the whole point -- and
   `os.Stat`s it;
5. on a miss calls `t.Errorf` naming the document, the line number the link is on, the target as
   written and the path it resolved to. `t.Errorf`, NOT `t.Fatalf`, so one run names them all.

Above the function write five to eight comment lines: that `docs/spec-pulse/` is one file per
section of `docs/SPEC-PULSE.md` (#560), that a link moved with its section and was never
re-based, that resolution is relative to the file the link stands in and never to the top file,
and why an anchor-only (`#...`) target is the one relative form that is not a path.

```
go test ./internal/docs/ -run TestEverySpecPulseSectionLinkResolves -count=1 2>&1 | head -40
```

It must be RED naming **26** broken links across exactly these four files: `00-preamble.md` 3,
`02-the-rules-numbered.md` 2, `08-rate-and-convergence.md` 2, `16-layout.md` 19. Put the first
failing line in RESULT.md verbatim and the count in `red:`. If it is green before you have
changed a document, or names a file this card does not name, STOP: line 2 `BLOCKED
not-reproduced`, paste the output. Do not widen the card.

STEP 4. THE FIX, only in the four documents.

Re-base each broken target onto the directory its file stands in. Nothing else on those lines
changes -- not the link TEXT, not the prose, not one space:

* in `00-preamble.md`, `02-the-rules-numbered.md`, `08-rate-and-convergence.md`: a target
  `SPEC-SWARM.md`, `WORKER-CARDS.md`, `SPEC-MERGE.md` or `PIT-STOP.md` gains a `../` prefix.
  Seven links.
* in `16-layout.md` lines 13-31: the target's leading `spec-pulse/` is removed. Nineteen links.

Touch no other file. Do NOT edit `docs/SPEC-PULSE.md`: its copies are correct relative to
`docs/`, and `internal/pulse/layout560_test.go:49` requires the `spec-pulse/<file>.md` spelling
to stand in it. Then run the named test again; it must be green.

STEP 5. THE NEGATIVE CONTROL -- prove the test is not vacuous.

In a scratch copy put ONE correction back: in `docs/spec-pulse/08-rate-and-convergence.md:41`,
drop the `../` prefix you added. Run the named test again. It must go RED naming that file, that
line and that target. Restore the correction. Paste the reverted failure line into RESULT.md.

STEP 6. THE GATES. Run each ONCE from the repository and paste what each printed.

```
gofmt -l .
go build ./...
go vet ./internal/docs/
go test ./internal/docs/ -run TestEverySpecPulseSectionLinkResolves -count=1
go test ./internal/docs/ -count=1
go test ./internal/pulse/ -run TestOneFilePerSliceAndSection560 -count=1
go test ./internal/ci/ -count=1
git diff --check
git diff --name-only 702b0133267140c98d5a856b6766949556cc4f19..HEAD
```

`gofmt -l .` is FIRST and must print NOTHING. A red is a finding: record its first failing line
and do not rerun it to see whether it goes green. Where a command's first line names a missing
toolchain, a refused path (`SANDBOX DENIED`, `WALL`) or a full disk, write `BLOCKED-TOOLCHAIN`
in that row with the refusal verbatim, finish what you can by reading, commit, and say so under
`Left owed`.

`internal/pulse` and `internal/ci` are the pre-existing suites that read this repository's own
documents. This card changes NO pre-existing test file. If one goes red, that is a FINDING:
name it with its failing line and LEAVE IT ALONE. Editing a pre-existing test is
`test-weakened` and the card is rejected.

`git diff --name-only` must print exactly these five paths and nothing else:

```
docs/spec-pulse/00-preamble.md
docs/spec-pulse/02-the-rules-numbered.md
docs/spec-pulse/08-rate-and-convergence.md
docs/spec-pulse/16-layout.md
internal/docs/spec_pulse_links_test.go
```

STEP 7. THE COMMIT. The card ends here; publication is the coordinator's.

```
git config user.name "Rowan"
git config user.email "rowan@mas-bandwidth.com"
git add docs/spec-pulse/00-preamble.md docs/spec-pulse/02-the-rules-numbered.md docs/spec-pulse/08-rate-and-convergence.md docs/spec-pulse/16-layout.md internal/docs/spec_pulse_links_test.go
git commit -q -m "docs/spec-pulse: a section file's links are relative to the section file (#1547)"
git rev-parse HEAD
```

Use those commands exactly as written. Do not push. Do not open a pull request.

STEP 8. RESULT.md, at the root of the job directory. Line 1 is EXACTLY line 1 of this card,
character for character. Line 2 is one of `DONE`, `ABSTAIN <why>`, `BLOCKED <why>`. Then:

```
BRANCH tools11-c1-links-specpulse
REPO mas-bandwidth/nova-tools

| gate | command | result line |
|---|---|---|
| gofmt | gofmt -l . | <paste, or "(no output)"> |
| build | go build ./... | <paste> |
| vet | go vet ./internal/docs/ | <paste> |
| the named test | go test ./internal/docs/ -run TestEverySpecPulseSectionLinkResolves -count=1 | <paste> |
| the package | go test ./internal/docs/ -count=1 | <paste> |
| layout #560 | go test ./internal/pulse/ -run TestOneFilePerSliceAndSection560 -count=1 | <paste> |
| internal/ci | go test ./internal/ci/ -count=1 | <paste> |
| whitespace | git diff --check | <paste> |

| red first | the one edit | the failure line |
|---|---|---|
| before the fix | the test alone, against the unfixed tree | <the FIRST failing line> |
| the fix reverted | one `../` dropped again in 08-rate-and-convergence.md:41 | <paste> |

red: <how many broken links the test named against the unfixed tree, and the first line verbatim>
green: <the named test's ok line after the fix>
files: <the git diff --name-only output, verbatim>
head: <git rev-parse HEAD>

Left owed: <anything you could not make true, named, or "nothing">
```

PERMITTED, and this is the last permission line: everything under the job directory the runner
gave you; `go doc` for a stdlib symbol; creating one new file,
`internal/docs/spec_pulse_links_test.go`, and editing the four documents named in `PATHS:`.
Where the correction cannot be made true inside those five paths, that is the finding: say so in
RESULT.md and stop. Nothing later in this card widens this line.
