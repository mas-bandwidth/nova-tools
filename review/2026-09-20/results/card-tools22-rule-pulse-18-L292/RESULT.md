RESULT tools22-rule-pulse-18-L292 sha=5298f6be12ea — does the code at this base do what docs/SPEC-PULSE.md rule 18 says?
GAP internal/pulse/harvest.go:393
SPEC docs/SPEC-PULSE.md:292 rule 18
PKG internal/pulse
ASK An implementation satisfies rule 18 only when each verb caps each of its event kinds — HARVEST PR, HARVEST RETRY, CUT SKIPPED, ADMIT REFUSED — at `--max` (default 20, 0 = unbounded) and then prints one `<TOKEN> MORE kind=<k> shown=<n> total=<t> <remedy>` count line on success and failure alike, each value a single `internal/oneline` token, never prints a list of titles, and makes `--max` bound `pool`/`cut`'s actual work while leaving the unprocessed remainder for the next call.

DECIDING LINES

`internal/pulse/harvest.go:147` — the harvest event lines are collected into ONE flat list, not one list per kind:
    lines := make([]string, 0) // HARVEST PR / RETRY / REFUSED per-card lines

`internal/pulse/harvest.go:324-327` — that single list is offered to one flat cap:
    grouped := bound(in.Stdout, in.Max)
    for _, l := range lines {
        grouped.Line(l)
    }

`internal/pulse/harvest.go:369-395` — the cap is a hand-rolled flat `boundedList` whose MORE line hard-codes a single `kind=event`, so `HARVEST PR` and `HARVEST RETRY` (listed by rule 18 as distinct kinds) share one `--max` budget and one count line instead of being capped per kind:
    func (l *boundedList) More() {
        if l.max > 0 && l.total > l.shown {
            fmt.Fprintf(l.w, "HARVEST MORE kind=event shown=%d total=%d use --max 0 to show all\n", l.shown, l.total)
        }
    }

Contrast with the per-kind shape the same package already mandates for `status` (`internal/pulse/status.go:113,161,178,202`, each `bounded.Capped(..., "STATUS", "width"/"stream"/"friend"/"fault", ...)`) and for `harvest --working` (`internal/pulse/harvest_working.go:105`, one cap per job kind). `internal/bounded/bounded.go:149-181` (`Group`) exists precisely so "20 findings of one kind must not bury the single finding of another"; harvest's flat `kind=event` does exactly what that defends against.

WHAT DOES CONFORM: `pool` bounds its work (`internal/pulse/pool.go:93-95` `if in.Max > 0 && len(rows) >= in.Max { break }`) and `cut` bounds both work and the `CUT SKIPPED` print (`internal/pulse/cut.go:76` `bounded.Capped(in.Stderr, in.Max, "CUT", "skipped", ...)`, and `cut.go:94-96` break), each with `CUT OK cards=<n>` = `len(cards)` and 0 = no bound.

SECOND GAP: ADMIT REFUSED is capped at a hard-coded 5, not `--max` (default 20). `internal/pulse/admission.go:20` `const AdmitShown = 5`, used at `admission.go:74` `bounded.Capped(w, AdmitShown, "ADMIT", "stop", ...)`; `admitCards` never receives `LaunchInput.Max`, so `--max 0` will not lift the cap and `--max 3` will not tighten it.

GREEPS RUN
    grep -rn "MORE kind=" --include='*.go' .
    grep -rn "ADMIT REFUSED\|CUT SKIPPED\|HARVEST PR\|HARVEST RETRY" --include='*.go' internal/
    grep -rn "bounded.Grouped\|bounded.Capped\|bounded.List\|kind=" internal/pulse/*.go
    grep -rn "func Test" --include='*_test.go' internal/pulse/
    grep -rn "max := f.fs.Int(\"max\"" cmd/nova-pulse/*.go

GUARD: the flat harvest behaviour is enshrined, not guarded — `internal/pulse/harvest_working_test.go:383 TestHarvestWorkingOutputIsBounded` asserts the flat "at most --max lines plus one HARVEST MORE" shape, and `internal/pulse/harvest_shared_queue_test.go:10` documents `HARVEST MORE kind=event ... total=151`. Fixing the cap to per-kind would turn that documented expectation red.

Left owed: `pool`'s `--max` default is 0 (`cmd/nova-pulse/main.go:370`), not 20, which is fine for a work-bound-only verb with no capped event-kind print; `cut`'s `writeSkippedTSV` (`cut.go:563-570`) still writes skipped.tsv rows for pool rows beyond `--max` whose template is missing, so a row left for the next call can be "counted skipped" after all — a smaller edge of the same rule.
