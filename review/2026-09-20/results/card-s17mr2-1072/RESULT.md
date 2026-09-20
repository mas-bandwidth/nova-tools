RESULT: s17mr2-1072 sha=CANNOT_READ

## CANNOT READ — truncated diff hides the test body

**File:** `internal/codegen/cpptable/accessor_descriptor_test.go`
**Hunk needed:** lines 28–268 (the entire test function body after `const accessorDescriptorMain = ...`)

The diff is truncated at "6000 of 14783 BYTES" mid-way through the new test file. The portion I cannot see contains the main loop and all assertion logic — the `for (...)` iteration reading every field of both BlockBlock and ProbeTableCookBody twice (accessor vs descriptor), the pointer SLOT comparison, and the return-by-failures check. Without this content I cannot determine:

1. Whether any assertions exist on the scalar fields (Step 3: could be vacuous tests or unfilled cells)
2. Whether the negative control test actually breaks an agreement to turn red (Step 3/5: unfilled precedent)
3. Whether the slot-negative-control test breaks the pointer's SLOT specifically (Step 3)
4. Whether anything calls these test functions beyond Go's `testing` runner (Step 5 parallel concern)

## What I CAN assess from the visible portions

### Makefile (lines +343–+360 added)
Three new `.PHONY` targets declared and called under `test:`. Target names match what PORTING.md shows. **Structurally OK.**

### docs/PORTING.md matrix row flip (line +1387)
C++ cell flips from `❌ #421` to `✅ tables-accessor-descriptor-agreement tables-accessor-negative-control tables-slot-negative-control`. Names align with the Makefile additions.

**Step 4 concern:** This is a cell-flip without visibility into whether the diff behind it contains assertions that actually close that cell. Per the hint referencing `test/js-tables/fixedform.mjs` (:122-123 defining two checker functions retired but still defined at :642/:860 with nothing calling either), the risk pattern is: a matrix cell scored as ✅ with no executing assertion backing it. I cannot confirm the reverse.

### New file: `accessor_descriptor_test.go` (visible fragment)
The schema declares a `Block` (fixed table) and `Node` (variable/cook table). Comments describe double-reading: "reads every field of both records TWICE — once through the generated accessor ... and once through the descriptor's own offset." Negative control targets are declared for scalar-offset breakage and pointer-SLOT breakage respectively.

The visible comments and schema design are **consistent with the documented approach** (two independent derivations, compare accessor against descriptor, two controls each moving one derivation). No obvious defect in the structure I can see.

## Step 5 (make target reachability) — partially assessable
The card notes `make/go.mk:557` is the only `go test` in the go gate and its `-run` filter names four tests. These new tests (`TestCppAccessorDescriptorAgreement`, etc.) are not named among those four, so they may not execute in the go leg gate even though the package reports `ok`. The `test:` Makefile target does call them via `$(MAKE) tables-accessor-descriptor-agreement`, so they ARE reachable from `make test`. This depends on whether `make test` is considered the gating target for this PR's leg.
