RESULT tools22-rule-pulse-27 sha=5298f6be12ea
ABSENT
SPEC docs/SPEC-PULSE.md:1933 rule 27
PKG internal/pulse
ASK for the rule to hold, nova-pulse's `handoff --to <name>` verb must probe the successor with `nova-wake awake <name>` before ending the shift, and when that returns non-zero (successor asleep) refuse with a `HANDOFF REFUSED` line naming the remedy, exit 2, write no HANDOFF record, post no note, and leave the OWNER lock unchanged.
Greps run (all from repo root, `--include='*.go'`):
  grep -rn "HANDOFF REFUSED" internal/pulse/          -> (no output)
  grep -rn "HANDOFF\|Handoff\|handoff" internal/pulse/ -> only test/comment hits (manager_test.go:6,272,274,291,465; admission_checklist_spec_test.go:43)
  grep -rln -i "handoff\|nova-wake" .                 -> cmd/nova-wake, cmd/nova-work, internal/decide; nothing in internal/pulse non-test
  grep -rn "HANDOFF REFUSED\|HANDOFF OK\|TAKEOVER REFUSED\|TAKEOVER OK\|handoff --to\|takeover --as" --include='*.go' . (non-test) -> exit 1, no matches
  grep -rn "SHIFT END\|OWNER\|owner" internal/pulse/*.go -> only GitHub owner/repo usages, no shift-lock OWNER
  grep -rn "nova-wake\|awake" internal/pulse/*.go     -> (no output)
Deciding reads:
  internal/pulse/cli.go:12-26 `pulseVerbs` block lists pool cut launch harvest beat manager progress wake sleep width version help — no handoff, no takeover.
  internal/pulse/cli.go:42-58 `Main` switch cases: help version harvest beat watch status progress — no handoff/takeover case.
  internal/pulse/manager.go:199-218 `shift()` writes only a self-terminating `SHIFT END cycles=... decisions=... escalations=...` line and returns; no `--to <name>`, no successor probe, no OWNER/HANDOFF files.
  docs/spec-pulse/14-open-questions...md:20-22 states `pulseVerbs` lacks the `handoff`, `takeover` and `status` lines — a named, open delta at this head.
Left owed
