# Friends' working directories

A friend is an AI who works beside the coordinator under her own name: a
nova-config friend row (docs/SPEC-CONFIG.md, `friend`), a row of the sprint's
friends table (docs/FLEET.md), and a working directory `~/<name>-working` on
the machine she runs on. The coordinator is a friend row too (the sprint row's
`coordinator` names one), and her own working directory follows the same
standard. This page is the standard for that directory: how a job arrives, how
it is reported, where its work lives, and how the work is removed once done.

## The inbox/outbox standard

Only the coordinator reaches out. A job is a directory:

- the coordinator delivers `inbox/<job>/`, with its `BRIEF.md`; `<job>` begins
  with its date, `2026-10-02-cold-rating`;
- the friend makes `outbox/<job>/` when she starts;
- the friend writes `outbox/<job>/REPORT.md` when she is done, with a
  `Verdict:` line (its first word HOLD, FAIL, FAILED or BROKEN is a failed job;
  any other word, or no such line, is ok).

A job is ready while `outbox/<job>/` is absent, working while it is there
without `REPORT.md`, and done once `REPORT.md` exists. `nova-sprint friend
sync` reads these directories (writing in them only a sprint card's brief, below)
into the friends table's `ready`, `working`, `done` and `ok%`.

## A sprint card

A card of the sprint whose brief says `WHO: friend` or `WHO: friend <name>` is
dealt to a friend (docs/SPEC-SPRINT.md section 1, a friend's card; the owner,
2026-10-03: "Could we try expressing the work left for nova-tools-1.1.0 into
cards, and doing it via the sprint, but doing parts on friends where we would
normally do friend work."). It arrives as a job like any other:
`inbox/<card>/BRIEF.md` (after a clear, `<card>~<epoch>`), written by
`nova-sprint friend sync`. Its first line is the STATUS line:

```
STATUS: nova-sprint card <card>, epoch <e>, attempt <n>; push your work to the branch sprint/<card>.g<gen>.e<e>; when done, write outbox/<card>/REPORT.md with Verdict: LAND|HOLD|FAIL and Head: <sha>
```

then the working-directory line below, a later attempt's start (the current tip
of the card's base branch on origin, never an older base, with the work of the
last attempt that pushed carried onto it by her, redone where it does not
apply, and the Head she reports on that tip; nothing checks that descent, and
the sprint's only check of her finish is that Head is origin's tip of her
branch) and why it exists (`This attempt exists because:`, `A reader found:`, `The coordinator
asks:`), a blank line, and the card's brief. What a friend does with it:

1. Work in `jobs/<card>/` as for any job; commit, and push the commit to the
   branch the STATUS line names (never another: the sprint reads and lands
   origin's tip of that branch, and nothing else).
2. Write `outbox/<card>/REPORT.md` in this form, exactly:

   ```
   Verdict: LAND
   Head: <the full 40-character sha of the commit pushed>

   <one paragraph: what changed and the gate's result>
   ```

   `LAND` is work ready for its reads and its landing; `HOLD` is work stopped
   for the coordinator's decision, `FAIL` work that could not be done; for
   either, `Head:` may be left out, and the first paragraph says why. The
   first `Verdict:` and `Head:` lines are read (markdown marks around them
   are fine); the first paragraph that is neither of them nor a heading goes
   onto the card, cut to 1 KiB.

The sync finishes the card on its next run after the report (the period of the
loop that runs it: 15 s in the coordinator's loop): `LAND` goes to review at
origin's tip of the branch when that tip is the full sha `Head:` names; a Head
that is not the tip (a commit not pushed there, or pushed to after the report)
or a branch origin does not hold finishes nothing, and the sync says so naming
both shas each run until the report's Head is the tip (or the card's deadline
passes); a `LAND` with no full sha, or any other word, comes back failed saying
what it lacks; `HOLD` and `FAIL` come back failed to the coordinator with the
paragraph. A sprint card is not counted among the
inbox's jobs: the friends table counts it from the sprint.

## Where a job's work lives

inbox/ and outbox/ hold text: the brief, the report, the evidence. A job's
clones, worktrees and build output live in `jobs/<job>/`, the same `<job>` as
its inbox directory, and nowhere else; the build cache is the friend's one
cache, `.cache/go-build`, never one per job. Every brief to a friend (and every
brief to a coordinator's child) carries this line, with the name and the job
filled in:

```
Work in ~/<name>-working/jobs/<job>/: every clone, worktree and build output goes inside it, GOCACHE=~/<name>-working/.cache/go-build, and the report goes to ~/<name>-working/outbox/<job>/REPORT.md.
```

A clone left inside `inbox/<job>/`, beside its brief (the layout before this
line), is found there too.

## Retention: `nova-sprint friend clean`

A done job's clones are removed by the machine, nightly, by the rule the bench
slots keep (a launch that is done leaves no checkout; docs/SPEC-SWARM.md,
`member`), carried to a friend's jobs (ideas#833; the owner, 2026-10-02:
"cleanup must be auto!"). `nova-sprint friend clean`, for every friend row
(docs/SPEC-SPRINT.md, `friend clean`):

1. A job is `inbox/<job>/` or `jobs/<job>/`; it is done when
   `outbox/<job>/REPORT.md` exists, and its age is that report's.
2. Inside a done job at least 3 days old (`--days`), a clone with nothing
   uncommitted, no stash and no commit missing from every remote-tracking ref
   is removed, and so is build output (`node_modules`, `target`, `gocache`,
   `gocache-*`, `.gocache`, `go-build`, `wt-*`).
3. A clone with work nowhere else is dirty: it is listed in the loop's log
   each night with its path, age and why, and removed once its job is 14 days
   old whatever its state, the removal saying it was dirty.
4. inbox and outbox text, the friend's own repository, and every file outside
   `inbox/` and `jobs/` are never touched. A job not done is never touched,
   however old.
5. The friend's `.cache/go-build` is held under 10 GiB, least recently used
   entries first, as the member holds its pool's.

`--dry-run` prints every removal and listing with the bytes it would free and
removes nothing. The run ends `FRIENDS-CLEAN OK freed=<bytes> listed=<n>`.

It runs from a loop row on the machine that holds the directories, once a day,
reading the roster from the config store:

```
nova-config loop add friend-clean-bench-a --machine bench-a --argv '["/usr/bin/env","NOVA_PG_PASSWORD_ENV=NOVA_PG_CONFIG_PASSWORD","nova-sprint","friend","clean","--pg","postgres://nova_config@localhost:5432/nova"]' --seat bench --keys NOVA_PG_CONFIG_PASSWORD --every 86400 --as ada
```

Its log is the loop's, `~/nova-bench/loops/friend-clean-bench-a.log`.
