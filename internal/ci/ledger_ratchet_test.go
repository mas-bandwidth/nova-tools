package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The counted class-rule ledgers only shrink and the slowtests budgets only
// fall, and before this ratchet nothing compared a ledger with the branch's
// merge base: a change could add a forged row and raise a shard's ceiling
// together and the run still read green (security#70 finding 2). This
// ratchet reads every counted ledger shard under internal/ci/testdata/<rule>/...
// and the two slowtests ledgers at the merge base with origin/dev -- the same
// base the deprecated-imports ratchet resolves -- and refuses a row key the
// base lacks, a ceiling the base did not have, and a slowtests budget the base
// did not grant. A shard the base does not hold is all growth: a new ledger
// is a red to justify, not a green to assume.

// ledgerCeilingPrefix opens the one comment line a counted shard's row count is
// capped by, the same line internal/ci/allowlist reads (allowlist.go).
const ledgerCeilingPrefix = "# ceiling:"

const (
	slowTestsAllowlistPath   = "internal/ci/slow-tests_allowlist.txt"
	sleepsSkipsAllowlistPath = "internal/ci/sleeps-skips_allowlist.txt"
)

// parseLedgerRows reads a counted shard's text into its row keys (the first
// field of each row, the key every class rule's list is shrunk by) and its
// ceiling (-1 when the shard carries none).
func parseLedgerRows(text string) (keys map[string]bool, ceiling int) {
	keys, ceiling = map[string]bool{}, -1
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			if v, ok := strings.CutPrefix(line, ledgerCeilingPrefix); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					ceiling = n
				}
			}
			continue
		}
		if key := strings.Fields(line); len(key) > 0 {
			keys[key[0]] = true
		}
	}
	return keys, ceiling
}

// ledgerShardGrowth compares a counted shard's base text with its head text:
// every head row key the base lacks, and a head ceiling above the base's. A
// head with no ceiling over a base with one is a raise; adding a ceiling when
// the base had none is not a raise.
func ledgerShardGrowth(base, head string) (added []string, raisedCeiling bool) {
	baseKeys, baseCeiling := parseLedgerRows(base)
	headKeys, headCeiling := parseLedgerRows(head)
	for key := range headKeys {
		if !baseKeys[key] {
			added = append(added, key)
		}
	}
	sort.Strings(added)
	if baseCeiling >= 0 && (headCeiling < 0 || headCeiling > baseCeiling) {
		raisedCeiling = true
	}
	return added, raisedCeiling
}

// shardProblems evaluates growth for a single shard against its base text.
func shardProblems(rel, base string, baseText string, ok bool, headBytes []byte) []string {
	if !ok {
		return []string{fmt.Sprintf("%s is not in the merge base %s: a new shard is all growth; justify it beside the rule it measures", rel, base[:9])}
	}
	var problems []string
	added, raised := ledgerShardGrowth(baseText, string(headBytes))
	for _, key := range added {
		problems = append(problems, fmt.Sprintf("%s adds the row %q, which its merge base %s lacks; the ledgers only shrink: fix the code and drop the row", rel, key, base[:9]))
	}
	if raised {
		problems = append(problems, fmt.Sprintf("%s raises its ceiling over the merge base %s; a ceiling only falls", rel, base[:9]))
	}
	return problems
}

// parseSlowRows reads a slowtests ledger's text into its row keys (package and
// test, the first two fields) and the budget (in seconds) each row was granted.
func parseSlowRows(text string) (keys map[string]bool, budgets map[string]float64) {
	keys, budgets = map[string]bool{}, map[string]float64{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		key := fields[0] + " " + fields[1]
		keys[key] = true
		if secs, err := strconv.ParseFloat(fields[2], 64); err == nil {
			budgets[key] = secs
		}
	}
	return keys, budgets
}

// slowLedgerGrowth compares a slowtests ledger's base text with its head text:
// every head row key the base lacks, and every head budget above the base's.
func slowLedgerGrowth(base, head string) (added []string, raisedBudget []string) {
	baseKeys, baseBudgets := parseSlowRows(base)
	headKeys, headBudgets := parseSlowRows(head)
	for key := range headKeys {
		if !baseKeys[key] {
			added = append(added, key)
		}
	}
	for key, budget := range headBudgets {
		if base, ok := baseBudgets[key]; ok && budget > base {
			raisedBudget = append(raisedBudget, key)
		}
	}
	sort.Strings(added)
	sort.Strings(raisedBudget)
	return added, raisedBudget
}

// parseSleepRows reads a sleeps-skips ledger's text into its row keys (package
// and function, the first two fields).
func parseSleepRows(text string) map[string]bool {
	keys := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if fields := strings.Fields(line); len(fields) >= 2 {
			keys[fields[0]+" "+fields[1]] = true
		}
	}
	return keys
}

// sleepLedgerGrowth compares a sleeps-skips ledger's base text with its head
// text: every head row key the base lacks.
func sleepLedgerGrowth(base, head string) (added []string) {
	baseKeys := parseSleepRows(base)
	headKeys := parseSleepRows(head)
	for key := range headKeys {
		if !baseKeys[key] {
			added = append(added, key)
		}
	}
	sort.Strings(added)
	return added
}

// ledgerRatchetShards walks every counted shard: the .txt files under
// internal/ci/testdata/<rule>/..., at least one directory deep, so the
// top-level single-file lists and fixtures under testdata/ itself stay out.
func ledgerRatchetShards(root string) ([]string, error) {
	var shards []string
	base := filepath.Join(root, "internal", "ci", "testdata")
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".txt") {
			return err
		}
		rel, relErr := filepath.Rel(base, path)
		if relErr != nil {
			return relErr
		}
		if len(strings.Split(filepath.ToSlash(rel), "/")) < 2 {
			return nil // a top-level single file, not a rule's shard
		}
		shards = append(shards, filepath.ToSlash(filepath.Join("internal/ci/testdata", rel)))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking internal/ci/testdata: %w", err)
	}
	sort.Strings(shards)
	return shards, nil
}

// checkCountedShards checks every counted shard against its merge base version.
func checkCountedShards(root, base string, shards []string) ([]string, error) {
	var problems []string
	for _, rel := range shards {
		headBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		baseText, ok, err := ListAtCommit(root, base, rel)
		if err != nil {
			return nil, fmt.Errorf("%s at %s: %w", rel, base, err)
		}
		problems = append(problems, shardProblems(rel, base, baseText, ok, headBytes)...)
	}
	return problems, nil
}

// checkSlowTestsLedger checks slow-tests_allowlist.txt against the merge base.
func checkSlowTestsLedger(t testing.TB, root, base string) ([]string, error) {
	headBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(slowTestsAllowlistPath)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", slowTestsAllowlistPath, err)
	}
	baseText, ok, err := ListAtCommit(root, base, slowTestsAllowlistPath)
	if err != nil {
		return nil, fmt.Errorf("%s at %s: %w", slowTestsAllowlistPath, base, err)
	}
	if !ok {
		if t != nil {
			t.Logf("%s is not in the merge base %s: this change is the allowlist seed", slowTestsAllowlistPath, base[:9])
		}
		return nil, nil
	}
	var problems []string
	added, raised := slowLedgerGrowth(baseText, string(headBytes))
	for _, key := range added {
		problems = append(problems, fmt.Sprintf("%s adds the row %q, which its merge base %s lacks; the slowtests budgets only shrink", slowTestsAllowlistPath, key, base[:9]))
	}
	for _, key := range raised {
		problems = append(problems, fmt.Sprintf("%s raises %q over the budget its merge base %s granted; a budget only falls", slowTestsAllowlistPath, key, base[:9]))
	}
	return problems, nil
}

// checkSleepsLedger checks sleeps-skips_allowlist.txt against the merge base.
func checkSleepsLedger(t testing.TB, root, base string) ([]string, error) {
	headBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(sleepsSkipsAllowlistPath)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", sleepsSkipsAllowlistPath, err)
	}
	baseText, ok, err := ListAtCommit(root, base, sleepsSkipsAllowlistPath)
	if err != nil {
		return nil, fmt.Errorf("%s at %s: %w", sleepsSkipsAllowlistPath, base, err)
	}
	if !ok {
		if t != nil {
			t.Logf("%s is not in the merge base %s: this change is the allowlist seed", sleepsSkipsAllowlistPath, base[:9])
		}
		return nil, nil
	}
	var problems []string
	for _, key := range sleepLedgerGrowth(baseText, string(headBytes)) {
		problems = append(problems, fmt.Sprintf("%s adds the row %q, which its merge base %s lacks; the slowtests budgets only shrink", sleepsSkipsAllowlistPath, key, base[:9]))
	}
	return problems, nil
}

// ledgerRatchetProblems answers every growth this branch adds over its merge
// base, one sentence a finding: a counted shard or a slowtests ledger with a
// row key the base lacks, a ceiling the base did not set, or a budget the
// base did not grant. A shard the base does not hold is all growth.
func ledgerRatchetProblems(t testing.TB, root string) ([]string, error) {
	base, err := deprecatedImportsAllowlistBase(root)
	if err != nil {
		return nil, fmt.Errorf("the ledger ratchet needs the same base the deprecated-imports ratchet resolves: %w", err)
	}
	shards, err := ledgerRatchetShards(root)
	if err != nil {
		return nil, err
	}
	if len(shards) == 0 {
		return nil, fmt.Errorf("no counted shards under internal/ci/testdata: the walk is broken, not the tree")
	}
	shardProbs, err := checkCountedShards(root, base, shards)
	if err != nil {
		return nil, err
	}
	slowProbs, err := checkSlowTestsLedger(t, root, base)
	if err != nil {
		return nil, err
	}
	sleepProbs, err := checkSleepsLedger(t, root, base)
	if err != nil {
		return nil, err
	}
	return append(append(shardProbs, slowProbs...), sleepProbs...), nil
}

// TestClassRuleLedgersOnlyShrinkAgainstMergeBase asserts that this branch adds
// no row to any counted class-rule shard or slowtests ledger that its merge
// base with origin/dev lacks, and raises no ceiling or budget the base set.
func TestClassRuleLedgersOnlyShrinkAgainstMergeBase(t *testing.T) {
	t.Parallel()

	problems, err := ledgerRatchetProblems(t, repoRoot(t))
	require.NoError(t, err, "the ledger ratchet could not read its base or its shards")
	for _, problem := range problems {
		t.Errorf("%s", problem)
	}
}

func testCountedShardInMemory(t *testing.T) {
	t.Helper()
	base := "# a rule's shard\nrow-a  reason\nrow-b  reason\n# ceiling: 2\n"
	grown := "# a rule's shard\nrow-a  reason\nrow-b  reason\nrow-c  reason\n# ceiling: 3\n"
	added, raised := ledgerShardGrowth(base, grown)
	require.Equal(t, []string{"row-c"}, added, "an added row reports as growth")
	require.Equal(t, true, raised, "a ceiling raised from 2 to 3 reports")

	shrunk := "# a rule's shard\nrow-a  reason\n# ceiling: 1\n"
	added, raised = ledgerShardGrowth(base, shrunk)
	require.Empty(t, added, "a deleted row is a shrink, not growth")
	require.Equal(t, false, raised, "a ceiling lowered from 2 to 1 is clean")

	uncapped := "# a rule's shard\nrow-a  reason\nrow-b  reason\n"
	added, raised = ledgerShardGrowth(base, uncapped)
	require.Empty(t, added, "the same rows with no ceiling change no keys")
	require.Equal(t, true, raised, "removing the ceiling is raising it")

	noCeil, withCeil := "# shard\nrow-a r\n", "# shard\nrow-a r\n# ceiling: 1\n"
	added, raised = ledgerShardGrowth(noCeil, withCeil)
	require.Empty(t, added)
	require.Equal(t, false, raised, "adding a ceiling when base had none is not a raise")

	absent := shardProblems("internal/ci/testdata/rule/shard.txt", "123456789abc", "", false, []byte(grown))
	require.Equal(t, []string{
		"internal/ci/testdata/rule/shard.txt is not in the merge base 123456789: a new shard is all growth; justify it beside the rule it measures",
	}, absent)
}

func testSlowAndSleepInMemory(t *testing.T) {
	t.Helper()
	slowBase := "# slow\ninternal/pkg\t-\t14.1\t9.4s@studio\ninternal/pkg\tTestB\t28.5\t19s@run1\n"
	slowGrown := "# slow\ninternal/pkg\t-\t14.1\t9.4s@studio\ninternal/pkg\tTestB\t30.0\t19s@run1\ninternal/pkg\tTestC\t1.0\t0.5s@bench\n"
	sAdded, sRaised := slowLedgerGrowth(slowBase, slowGrown)
	require.Equal(t, []string{"internal/pkg TestC"}, sAdded, "an added slowtests row reports as growth")
	require.Equal(t, []string{"internal/pkg TestB"}, sRaised, "a budget raised from 28.5 to 30.0 reports")

	slowShrunk := "# slow\ninternal/pkg\t-\t10.0\t9.4s@studio\n"
	sAdded, sRaised = slowLedgerGrowth(slowBase, slowShrunk)
	require.Empty(t, sAdded, "a deleted slowtests row is a shrink")
	require.Empty(t, sRaised, "a budget lowered from 14.1 to 10.0 is clean")

	sleepBase := "# sleeps\ninternal/pkg\tFuncA\twhere.go:10\ninternal/pkg\tFuncB\twhere.go:20\n"
	sleepGrown := "# sleeps\ninternal/pkg\tFuncA\twhere.go:10\ninternal/pkg\tFuncB\twhere.go:20\ninternal/pkg\tFuncC\twhere.go:30\n"
	slAdded := sleepLedgerGrowth(sleepBase, sleepGrown)
	require.Equal(t, []string{"internal/pkg FuncC"}, slAdded, "an added sleeps row reports as growth")

	sleepShrunk := "# sleeps\ninternal/pkg\tFuncA\twhere.go:10\n"
	slAdded = sleepLedgerGrowth(sleepBase, sleepShrunk)
	require.Empty(t, slAdded, "a deleted sleeps row is clean")
}

// TestLedgerGrowthComparisonInMemory proves the comparison's both sides on
// texts held in the test: a grown ceiling and an added row report growth, a
// shrunk ceiling, a lowered budget and a deleted row read clean, and a base a
// shard is absent from is all growth.
func TestLedgerGrowthComparisonInMemory(t *testing.T) {
	t.Parallel()

	t.Run("CountedShard", testCountedShardInMemory)
	t.Run("SlowAndSleep", testSlowAndSleepInMemory)
}
