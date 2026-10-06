package ci

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
// Non-ledger fixtures carrying no ceiling in either head or base produce no
// findings.
func shardProblems(rel, base string, baseText string, ok bool, headBytes []byte) []string {
	_, headCeiling := parseLedgerRows(string(headBytes))
	baseCeiling := -1
	if ok {
		_, baseCeiling = parseLedgerRows(baseText)
	}
	if headCeiling < 0 && baseCeiling < 0 {
		return nil
	}
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

// ledgerRatchetShards walks every counted shard under internal/ci/testdata:
// the .txt files at least one directory deep whose head text or base text
// carries a "# ceiling:" line (fixtures without a ceiling stay out). A brand-new
// shard with a ceiling is kept so the ratchet reports it as all growth.
func ledgerRatchetShards(root, base string) ([]string, error) {
	texts, err := readLedgerBase(root, base, []string{"internal/ci/testdata"}, ledgerGit)
	if err != nil {
		return nil, err
	}
	return ledgerRatchetShardsAtBase(root, texts)
}

func ledgerRatchetShardsAtBase(root string, texts map[string]string) ([]string, error) {
	var shards []string
	testdata := filepath.Join(root, "internal", "ci", "testdata")
	err := filepath.WalkDir(testdata, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".txt") {
			return err
		}
		rel, relErr := filepath.Rel(testdata, path)
		if relErr != nil {
			return relErr
		}
		if len(strings.Split(filepath.ToSlash(rel), "/")) < 2 {
			return nil // a top-level single file, not a rule's shard
		}
		relShard := filepath.ToSlash(filepath.Join("internal/ci/testdata", rel))
		headBytes, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, headCeil := parseLedgerRows(string(headBytes))
		baseCeil := -1
		if baseText, ok := texts[relShard]; ok {
			_, baseCeil = parseLedgerRows(baseText)
		}
		if headCeil < 0 && baseCeil < 0 {
			return nil
		}
		shards = append(shards, relShard)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking internal/ci/testdata: %w", err)
	}
	sort.Strings(shards)
	return shards, nil
}

// ledgerGitOut is the injectable Git seam for the card's command-count proof.
type ledgerGitOut func(root, input string, args ...string) (string, error)

func ledgerGit(root, input string, args ...string) (string, error) {
	return gitOutInput(root, input, args...)
}

// checkCountedShards checks every counted shard against its merge base version.
func checkCountedShards(root, base string, shards []string) ([]string, error) {
	return checkCountedShardsWithGit(root, base, shards, ledgerGit)
}

// readLedgerBase implements the card's one tree read and one blob batch.
// Libraries considered: bufio.Reader and io.ReadFull preserve blob bytes,
// including embedded newlines; no line scanner parses blob bodies.
func readLedgerBase(root, base string, paths []string, git ledgerGitOut) (map[string]string, error) {
	texts := map[string]string{}
	if base == "" {
		return texts, nil
	}
	args := append([]string{"ls-tree", "-r", "-z", base, "--"}, paths...)
	tree, err := git(root, "", args...)
	if err != nil {
		return nil, err
	}
	var names, objects []string
	for _, entry := range strings.Split(tree, "\x00") {
		if entry == "" {
			continue
		}
		header, name, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(header)
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("malformed ledger tree entry %q", entry)
		}
		if fields[1] != "blob" || !strings.HasSuffix(name, ".txt") {
			continue
		}
		names = append(names, name)
		objects = append(objects, fields[2])
	}
	if len(objects) == 0 {
		return texts, nil
	}
	batch, err := git(root, strings.Join(objects, "\n")+"\n", "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	reader := bufio.NewReader(strings.NewReader(batch))
	for n, name := range names {
		header, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("%s batch header: %w", name, err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[0] != objects[n] || fields[1] != "blob" {
			return nil, fmt.Errorf("%s invalid batch header %q", name, header)
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil || size < 0 || size > len(batch) {
			return nil, fmt.Errorf("%s invalid batch size %q", name, fields[2])
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(reader, body); err != nil {
			return nil, fmt.Errorf("%s batch body: %w", name, err)
		}
		end, err := reader.ReadByte()
		if err != nil || end != '\n' {
			return nil, fmt.Errorf("%s batch lacks body terminator", name)
		}
		texts[name] = string(body)
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		return nil, fmt.Errorf("ledger batch has trailing data")
	}
	return texts, nil
}

func checkCountedShardsWithGit(root, base string, shards []string, git ledgerGitOut) ([]string, error) {
	texts, err := readLedgerBase(root, base, shards, git)
	if err != nil {
		return nil, err
	}
	return checkCountedShardsAtBase(root, base, shards, texts)
}

func checkCountedShardsAtBase(root, base string, shards []string, texts map[string]string) ([]string, error) {
	var problems []string
	for _, rel := range shards {
		headBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		baseText, ok := texts[rel]
		problems = append(problems, shardProblems(rel, base, baseText, ok, headBytes)...)
	}
	return problems, nil
}

// checkSlowTestsLedger checks slow-tests_allowlist.txt against the merge base.
func checkSlowTestsLedger(t testing.TB, root, base string, texts map[string]string) ([]string, error) {
	headBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(slowTestsAllowlistPath)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", slowTestsAllowlistPath, err)
	}
	baseText, ok := texts[slowTestsAllowlistPath]
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
func checkSleepsLedger(t testing.TB, root, base string, texts map[string]string) ([]string, error) {
	headBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(sleepsSkipsAllowlistPath)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", sleepsSkipsAllowlistPath, err)
	}
	baseText, ok := texts[sleepsSkipsAllowlistPath]
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
	return ledgerRatchetProblemsWithGit(t, root, base, ledgerGit)
}

func ledgerRatchetProblemsWithGit(t testing.TB, root, base string, git ledgerGitOut) ([]string, error) {
	texts, err := readLedgerBase(root, base, []string{"internal/ci/testdata", slowTestsAllowlistPath, sleepsSkipsAllowlistPath}, git)
	if err != nil {
		return nil, err
	}
	shards, err := ledgerRatchetShardsAtBase(root, texts)
	if err != nil {
		return nil, err
	}
	if len(shards) == 0 {
		return nil, fmt.Errorf("no counted shards under internal/ci/testdata: the walk is broken, not the tree")
	}
	shardProbs, err := checkCountedShardsAtBase(root, base, shards, texts)
	if err != nil {
		return nil, err
	}
	slowProbs, err := checkSlowTestsLedger(t, root, base, texts)
	if err != nil {
		return nil, err
	}
	sleepProbs, err := checkSleepsLedger(t, root, base, texts)
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

	// A ceiling-less .txt fixture is skipped whether new or existing.
	fixtureTxt := "package main\n\nfunc main() {}\n"
	skippedNew := shardProblems("internal/ci/testdata/waits/fixture.go.txt", "123456789abc", "", false, []byte(fixtureTxt))
	require.Empty(t, skippedNew, "a new ceiling-less .txt is not a counted shard and is skipped")

	skippedExisting := shardProblems("internal/ci/testdata/waits/fixture.go.txt", "123456789abc", fixtureTxt, true, []byte(fixtureTxt))
	require.Empty(t, skippedExisting, "an existing ceiling-less .txt is not a counted shard and is skipped")

	// A new shard that does carry a ceiling is reported as all growth.
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

// TestCountedShardsRatchetInGitRepo verifies that checkCountedShards detects
// added rows and raised ceilings against a base commit in a real git repository,
// flags brand-new ceiling-bearing shards as all growth, and skips ceiling-less
// fixture .txt files.
func TestCountedShardsRatchetInGitRepo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}

	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "user.name", "Alex"},
		{"config", "user.email", "alex@example.com"},
		{"config", "commit.gpgSign", "false"},
	} {
		_, err := gitOut(dir, args...)
		require.NoError(t, err, "git %v", args)
	}

	// Base commit has an established shard and a ceiling-less fixture.
	shardRel := "internal/ci/testdata/sample/shard.txt"
	fixtureRel := "internal/ci/testdata/waits/fixture.go.txt"
	baseShard := "# a rule's shard\nrow-a  reason\nrow-b  reason\n# ceiling: 2\n"
	fixtureCode := "package waits\n\nfunc Wait() {}\n"

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "internal/ci/testdata/sample"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "internal/ci/testdata/waits"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(shardRel)), []byte(baseShard), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(fixtureRel)), []byte(fixtureCode), 0o644))

	_, err := gitOut(dir, "add", ".")
	require.NoError(t, err, "git add")
	_, err = gitOut(dir, "commit", "-m", "base")
	require.NoError(t, err, "git commit")

	baseCommitOut, err := gitOut(dir, "rev-parse", "HEAD")
	require.NoError(t, err, "git rev-parse HEAD")
	baseCommit := strings.TrimSpace(baseCommitOut)

	// In the working copy:
	// 1. Existing shard adds a row and raises its ceiling.
	grownShard := "# a rule's shard\nrow-a  reason\nrow-b  reason\nrow-c  reason\n# ceiling: 3\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(shardRel)), []byte(grownShard), 0o644))

	// 2. Existing ceiling-less fixture adds lines (must not be treated as a shard).
	modifiedFixture := "package waits\n\nfunc Wait() {}\nfunc More() {}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(fixtureRel)), []byte(modifiedFixture), 0o644))

	// 3. Brand-new ceiling-bearing shard is added (all growth).
	newShardRel := "internal/ci/testdata/newrule/shard.txt"
	newShard := "# brand-new rule shard\nrow-z  reason\n# ceiling: 1\n"
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "internal/ci/testdata/newrule"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(newShardRel)), []byte(newShard), 0o644))

	// 4. Brand-new ceiling-less fixture is added (must be skipped).
	newFixtureRel := "internal/ci/testdata/waits/new_fixture.go.txt"
	newFixtureCode := "package waits\n\nfunc NewHelper() {}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(newFixtureRel)), []byte(newFixtureCode), 0o644))

	// ledgerRatchetShards discovers only counted shards: new and modified shards with ceilings.
	shards, err := ledgerRatchetShards(dir, baseCommit)
	require.NoError(t, err, "ledgerRatchetShards")
	require.Equal(t, []string{newShardRel, shardRel}, shards, "only counted shards with ceilings are selected")

	// checkCountedShards reports the expected growth problems.
	problems, err := checkCountedShards(dir, baseCommit, shards)
	require.NoError(t, err, "checkCountedShards")
	require.ElementsMatch(t, []string{
		fmt.Sprintf("%s is not in the merge base %s: a new shard is all growth; justify it beside the rule it measures", newShardRel, baseCommit[:9]),
		fmt.Sprintf("%s adds the row %q, which its merge base %s lacks; the ledgers only shrink: fix the code and drop the row", shardRel, "row-c", baseCommit[:9]),
		fmt.Sprintf("%s raises its ceiling over the merge base %s; a ceiling only falls", shardRel, baseCommit[:9]),
	}, problems)

	// Explicitly passing ceiling-less fixtures to checkCountedShards yields no problems.
	fixtureProblems, err := checkCountedShards(dir, baseCommit, []string{fixtureRel, newFixtureRel})
	require.NoError(t, err, "checkCountedShards on fixtures")
	require.Empty(t, fixtureProblems, "ceiling-less fixtures produce no ratchet problems")
}

// TestTheLedgerTestReadsTheBaseOnce pins the card's two-command base read,
// independent of the number of counted shards.
func TestTheLedgerTestReadsTheBaseOnce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	shards := []string{"a.txt", "b.txt", "new.txt"}
	for _, rel := range shards {
		require.NoError(t, os.WriteFile(filepath.Join(root, rel), []byte("row-a reason\n# ceiling: 1\n"), 0600))
	}
	var calls []string
	full := false
	fake := func(root, input string, args ...string) (string, error) {
		calls = append(calls, args[0])
		switch args[0] {
		case "ls-tree":
			prefix := ""
			if full {
				prefix = "internal/ci/testdata/rule/"
			}
			return "100644 blob aaaa\t" + prefix + "a.txt\x00100644 blob bbbb\t" + prefix + "b.txt\x00", nil
		case "show":
			return "row-a reason\n# ceiling: 1\n", nil
		case "cat-file":
			var out strings.Builder
			for _, oid := range strings.Fields(input) {
				body := "row-a reason\n# ceiling: 1\n"
				fmt.Fprintf(&out, "%s blob %d\n%s\n", oid, len(body), body)
			}
			return out.String(), nil
		}
		return "", fmt.Errorf("unexpected git command %v", args)
	}
	problems, err := checkCountedShardsWithGit(root, "0123456789abcdef", shards, fake)
	require.NoError(t, err)
	assert.Len(t, problems, 1, "new shard remains growth")
	assert.Equal(t, []string{"ls-tree", "cat-file"}, calls)
	// Exercise the actual discovery plus counted and slow-ledger path too.
	full = true
	calls = nil
	dir := filepath.Join(root, "internal/ci/testdata/rule")
	require.NoError(t, os.MkdirAll(dir, 0700))
	for _, rel := range shards {
		require.NoError(t, os.WriteFile(filepath.Join(dir, rel), []byte("row-a reason\n# ceiling: 1\n"), 0600))
	}
	for _, rel := range []string{slowTestsAllowlistPath, sleepsSkipsAllowlistPath} {
		require.NoError(t, os.WriteFile(filepath.Join(root, rel), []byte("# no rows\n"), 0600))
	}
	problems, err = ledgerRatchetProblemsWithGit(t, root, "0123456789abcdef", fake)
	require.NoError(t, err)
	assert.Len(t, problems, 1)
	assert.Equal(t, []string{"ls-tree", "cat-file"}, calls)
}

// TestLedgerBaseBatchPreservesBlobBytes pins byte framing and fails closed on
// missing, mismatched and truncated objects rather than excusing ledger growth.
func TestLedgerBaseBatchPreservesBlobBytes(t *testing.T) {
	t.Parallel()
	body := "# ceiling: 1\nrow-a café\n\x00\n"
	for _, row := range []struct {
		name, batch string
		bad         bool
	}{
		{"bytes", fmt.Sprintf("aaaa blob %d\n%s\n", len(body), body), false},
		{"missing", "aaaa missing\n", true},
		{"wrong object", "bbbb blob 0\n\n", true},
		{"truncated", "aaaa blob 1000\nshort\n", true},
		{"missing terminator", "aaaa blob 1\nx", true},
		{"trailing data", "aaaa blob 0\n\nextra", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			fake := func(root, input string, args ...string) (string, error) {
				if args[0] == "ls-tree" {
					return "100644 blob aaaa\tledger.txt\x00", nil
				}
				assert.Equal(t, "aaaa\n", input)
				assert.Equal(t, []string{"cat-file", "--batch"}, args)
				return row.batch, nil
			}
			texts, err := readLedgerBase("unused", "0123456789abcdef", []string{"ledger.txt"}, fake)
			if row.bad {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, body, texts["ledger.txt"])
		})
	}
}
