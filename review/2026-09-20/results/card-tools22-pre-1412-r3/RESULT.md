RESULT tools22-pre-1412-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1412 at head b49fb99b52f8: ci: TestNoUncheckedFieldsIndex, the class behind #1390
PREREAD 1412 claims=10 proven=8 unproven=2 defects=1 high=0

PR 1412
HEAD b49fb99b52f8c4827ae9b0ca0882f6fba8e18718
BASE dev
MERGE-BASE 04bb4e1c7ee73537396bf436701f129233446432
BEHIND 71
FILES 2 production, 3 test
LINES +654 -0

CLAIMS

1. An index or slice expression on the result of strings.Fields, strings.Split or bytes.Fields must be guarded by a len(x) comparison in the same function, or the run goes red.
   PROVEN-BY internal/ci/fieldsindex_rule_test.go:30 TestFieldsIndexRefusesThePreFixCursor — the shape of #1390 (a two-field Fields result reached at fields[2:]) yields exactly one finding that names the splitter, the slice expression and the missing len check.

2. The fix the rule asks for — a length check with a refusal line ahead of the index — is accepted and yields no finding.
   PROVEN-BY internal/ci/fieldsindex_rule_test.go:48 TestFieldsIndexAcceptsTheFixedCursor — asserts zero findings for the len(fields) < 3 + refusal shape.

3. The shapes that cannot be short are accepted: len(x) in a for header (the reverse walk), len(x) in the subscript itself, switch len(x), range x, a whole-slice x[:], and x[0] / x[1:] on a strings.Split whose separator is a non-empty string literal.
   PROVEN-BY internal/ci/fieldsindex_rule_test.go:67 TestFieldsIndexAcceptsTheShapesThatCannotBeShort — seven subtests each assert zero findings.

4. The narrowings are narrow: fields[0] on strings.Fields, parts[1] on a Split, a computed or empty separator, bytes.Fields, and a high slice bound are all refused.
   PROVEN-BY internal/ci/fieldsindex_rule_test.go:133 TestFieldsIndexRefusesTheShapesThatCanBeShort — six subtests each assert exactly one finding.

5. The allowlist key is file:function, receiver-qualified when the offender is a method.
   PROVEN-BY internal/ci/fieldsindex_rule_test.go:185 TestFieldsIndexKeyIsFileAndFunction — asserts the key is "cmd/nova-wake/serve.go:server.spawn".

6. The allowlist is shrink-only in both directions — an unlisted unchecked index is red and a listed one that has left is red — and every row must carry a reason; it ships empty.
   PROVEN-BY internal/ci/fieldsindex_class_test.go:39 TestNoUncheckedFieldsIndex (the stale-row loop) together with internal/ci/fieldsindex_rule_test.go:207 TestFieldsIndexAllowlistIsShrinkOnly (reason required per row).

7. nova-wake's (*server).spawn now guards strings.Fields(s.onNote) with a length check of its own, so the one offender the rule found is fixed rather than allowlisted.
   PROVEN-BY internal/ci/fieldsindex_class_test.go:39 TestNoUncheckedFieldsIndex — delete the len(fields) == 0 check at cmd/nova-wake/serve.go:584 and spawn's fields[0] becomes an unguarded index that makes the class test go red.

8. spawn refuses an empty --on-note at runtime — rc 2 and a WAKE REFUSED line naming "--on-note carries no command" — instead of panicking on fields[0].
   UNPROVEN — no test in this diff and no test in the tree calls spawn with an empty or whitespace-only onNote; the parse-time check at serve.go:96 makes the branch unreachable from the CLI, and no unit test constructs a server with an empty onNote to exercise it.

9. serve refuses an empty or whitespace-only --on-note at parse time.
   PROVEN-BY-EXISTING cmd/nova-wake/serve_test.go:803 TestServeRefusesWhitespaceOnlyOnNote — asserts exit 2 and the "--on-note is required" refusal for --on-note "   ".

10. The rule's first run named 26 sites in 15 functions, 25 of which could not be short.
    UNPROVEN — a historical narrative in the commit and SPEC-CI.md; no test witnesses the count, and the tree still holds an unchecked inline Fields index at internal/pulse/pool.go:520 that this rule cannot see (see DEFECT 1).

DEFECTS

DEFECT medium internal/ci/fieldsindex_class_test.go:162 — the rule only follows index/slice expressions whose base is an identifier assigned from a splitter (splitVars records only *ast.AssignStmt / *ast.ValueSpec LHS idents, and splitIndexTarget requires an *ast.Ident base), so an inline index such as strings.Fields(rest)[0] escapes it entirely — yet internal/pulse/pool.go:520, present at the merge-base, is exactly that: an unchecked strings.Fields index that panics on a blank suffix — so the SPEC sentence "an index or slice expression on the result of strings.Fields... must be preceded by a len(x) comparison" and the commit's "that leaves one real offender, and it is fixed" both overstate what the rule enforces, and the class the rule claims to be behind is still live in the tree in a form it cannot see — the fix is to make splitIndexTarget also handle a CallExpr base (e.g. strings.Fields(x)[0]) so the inline case is either refused or allowlisted, and to reconcile the SPEC wording with the variable-only tracking it actually implements.

QUESTIONS

1. In internal/pulse/pool.go:520, cellID calls strings.Fields(rest)[0] where rest is the text after the last ":id"; when ":id" is immediately followed by ":card" the suffix is blank and Fields returns an empty slice — is ":id" with no value, directly before ":card", a shape real roadmap files produce, or is the blank-suffix path unreachable in practice?

2. The PR head is 71 commits behind dev and the class test walks every non-test .go file under cmd/ and internal/ at test time — has TestNoUncheckedFieldsIndex been run against a current dev tree, and does the gate run it on the merge result or only on the PR branch?

3. Why is splitFuncs limited to strings.Fields, strings.Split and bytes.Fields while strings.SplitN, strings.FieldsFunc and bytes.Split are left out — deliberate scope, or an omission a SplitN with a computed separator (no at-least-one guarantee) could later slip through?

4. Can anyone confirm the "26 findings in 15 functions" first-run figure, and was internal/pulse/pool.go:520 part of that run at all, or invisible to it for the same reason the rule misses it now?

Left owed — I did not run the class or rule tests (no test run is expected on this card); the "tree is clean under the rule" claims rest on close reading of every splitter site reachable by the rule in cmd/ and internal/ at the PR head and on a spot-check of the sites current dev added in the 71 commits since the merge-base, all of which proved guarded. I read both new test files in full, SPEC-CI.md in full, and the serve.go hunks in context.

git status --short:
(clean — nothing printed)

git rev-parse HEAD:
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-1412-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1412-r1	1	2026-09-20T19:04:55Z	2026-09-20T19:11:08Z	0	opencode	deepseek-v4-flash	50086	39119	0	2155008	0	0.0783
