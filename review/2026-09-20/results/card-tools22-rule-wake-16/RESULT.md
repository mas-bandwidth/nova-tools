RESULT tools22-rule-wake-16 sha=5298f6be12ea — does the code at this base do what docs/SPEC-WAKE.md rule 16 says?
CONFORMS internal/wake/source.go:25
SPEC docs/SPEC-WAKE.md:4276 rule 16
PKG internal/wake
ASK The code must separate poll cadences into two independent flags — --interval for fast sources (bus, line, reports) and --entry-interval for slow CI sources (entries, runs) — with no defaults on either.

deciding lines:
  internal/wake/source.go:22-25:
    // Every is this source's poll cadence. The bus's is --interval and an
    // entry's is --entry-interval, because an 8-minute hosted run deserves one
    // check at 8 minutes and not eight checks at one minute.
    Every() time.Duration

  cmd/nova-wake/main.go:651:
    interval     = fs.String("interval", "", "")

  cmd/nova-wake/main.go:662:
    entryEvery   = fs.String("entry-interval", "", "")

  cmd/nova-wake/main.go:709:
    if *interval == "" {
        p.missing("interval")
    }

  cmd/nova-wake/main.go:727:
    if (len(entries) > 0 || len(runs) > 0) && *entryEvery == "" {
        p.missing("entry-interval")
    }

  cmd/nova-wake/main.go:908-921 (cadence assignments):
    busSrc = &wake.Bus{..., Every_: every, ...}       // --interval
    sources = append(sources, &polled{src: &wake.Entries{..., Every_: entryEveryDur, ...}}) // --entry-interval
    sources = append(sources, &polled{src: &wake.Reports{..., Every_: every}, ...})         // --interval
    // forge sources (prs, branches/refs, runs) use forgeEveryDur / entryEveryDur respectively
    lockSrc = &wake.Locks{..., Every_: every, ...}      // --interval

cadence map (what the code assigns per source):
  --interval -> Bus (bus.go:158), Lines (line.go, via loop cadence at main.go:1182), Reports (report.go:29), Locks (lockfile.go:41)
  --entry-interval -> Entries (entry.go:35), Runs (run.go:59)
  --forge-interval -> PRs (pr.go:76), Branches (branch.go:41)

The loop polls each source at its Own Every() duration (main.go:1163: s.due = now.Add(s.src.Every())), so each source type respects its own cadence.

GUARDED-BY cmd/nova-wake/main_test.go:325 TestTheEntryIntervalIsTheRunLength
  Sub-tests: "--interval missing" (line 352), "--entry without --entry-interval" (line 358), "--interval below the floor" (line 365)

grep evidence:
  grep -rn "interval" --include='*.go' internal/wake/
  grep -rn "entry-interval\|entryEvery" --include='*.go' internal/wake/ cmd/nova-wake/
  grep -rn "func Test" --include='*_test.go' internal/wake/
  grep -rn "\"entry-interval\"\|\"interval\"" --include='*_test.go' cmd/nova-wake/

Left owed

git status --short
 ?? .lease
 ?? .nova-sandbox-tmp/
 ?? RESULT.md
 ?? harness-output.log
 ?? opencode.json
 ?? repo/
