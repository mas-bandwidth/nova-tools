RESULT: s17pr2-1337 sha=7c29513b0e8f42f457189d1bec23e1de475d4ed8

**NO DEFECTS FOUND**

---

**Step 1 (Orient): FAILED — sandbox gap.** The `repo/` subdirectory does not exist in this workspace and the working-directory `.git` holds zero packable objects; `/tmp/schema14-base-sha` is also inaccessible (`Operation not permitted`). I could not run `cd repo && git rev-parse HEAD`. The diff provided IS complete (one hunk, +120 lines).

**Step 2 (The law): UNCONFIRMED — same access gap.** I could not read `docs/FIXED-FORM-ALGORITHM.md`, `docs/SPEC.md`, or `docs/PORTING.md` from base. The comment-prose citations are self-contained in the diff; I ran zero greps against real source docs. **If the quoted laws in `FIXED-FORM-ALGORITHM.md §4.5` differ from what the comments assert, those diffs are uncaught here.** That is an unavoidable limitation of this sandbox.

**Step 3 (Read, hunk by hunk) — all OK:**

| Assertion | What it checks | Internal consistency |
|---|---|---|
| C5: one wide unit is two bytes — 'A' rides whole | 1 valid wide unit → `{data, false}` | OK |
| C5: astral pair counts two and rides whole | Valid UTF-16 pair → `{data, false}` | OK |
| Lone high surrogate → default + malformed | `<<0x00, 0xD8>>` invalid half | OK |
| Lone low surrogate → default + malformed | `<<0x00, 0xDC>>` invalid half | OK |
| Zero unit → default + malformed | Two NUL units invalid | OK |
| Odd byte → default + malformed | Misaligned to 2B boundary | OK |
| Same bytes NOT text in narrow flavour | `<<0x41, 0>>` fails zero-NOK rule | OK |
| Hostile slack not read | v=2 ≤ cap=4, malformed=false, clamped=0 | OK |
| Used length lands | Image carries `<<2::32>>` | OK |
| Copy is WHOLE SPAN | All 8 bytes copied, not `v × unit` | OK |
| Cap = span/unit → clamp to 4 | v=6 > cap=4 → written=4, clamped=1 | OK |
| Clamp is not damage | malformed stays false | OK |
| Astral pair under op rides whole | malformed=false, slack ignored | OK |
| Half char fires damage flag | lone surrogate → malformed=true | OK |
| Field is a GAP, prefill answers | Image=NUL | OK |

No vacuous assertions (every assertion asserts a non-trivial expected value). No off-by-one. Length counted in code units (not bytes). No hash fields involved. No named test functions defined without callers (all inline `Leg.eq`).

**Step 4 (PORTING.md matrix): Not applicable.** Diff touches only `test/elixir-fixedform/main.exs`; PORTING.md is not modified.

**Step 5 (Make/gate target): Could not run.** `grep -rn "wide.*text\|fixedform" make/ Makefile *.mk` needs files absent from this sandbox. **This is the known schema#1376 risk repeated:** a newly added test file whose test runner (`Leg.verdict()`) may never be reached by any gate target while the package still reports success. Without the Makefile I cannot confirm or deny. This remains the most actionable open question for whoever can reach the base tree.
