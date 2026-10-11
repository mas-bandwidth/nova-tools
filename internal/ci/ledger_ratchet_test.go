package ci

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/testgit"
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
	unitSocketsSeedDir       = "internal/ci/testdata/unit-sockets/"
	refusalGrammarSeedDir    = "internal/ci/testdata/refusal-grammar/"
)

// seedLedgerDirs are the counted ledgers allowed their one-time seed: each is
// exempt only while the merge base holds no shard of it (docs/SPEC-CI.md,
// `unit-sockets` and `refusal-grammar`).
var seedLedgerDirs = []string{unitSocketsSeedDir, refusalGrammarSeedDir}

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

// ledgerGit runs one git command in root, with stdin when stdin is not empty,
// and returns its standard output: gitOutIn in the ratchet, a counting fake in
// TestTheLedgerTestReadsTheBaseOnce.
type ledgerGit func(root, stdin string, args ...string) (string, error)

// baseLookup answers a file as the merge base holds it, and false when the base
// does not carry it (the change introduces it): ListAtCommit's answer.
type baseLookup func(rel string) (text string, ok bool, err error)

// ledgerBasePaths are what the ratchet reads at the merge base: every counted
// shard lives under the first, and the two slowtests ledgers are the others.
var ledgerBasePaths = []string{"internal/ci/testdata", slowTestsAllowlistPath, sleepsSkipsAllowlistPath}

// readMergeBase reads every file the ratchet compares, as the merge base holds
// it, with two git processes whatever the number of shards: one
// `git ls-tree -r -z` names each blob under ledgerBasePaths, and one
// `git cat-file --batch` prints the .txt ones. It returns the lookup, and the
// complete set of base paths the listing named, so the seed check can see a
// shard this change deletes. Two processes per shard (an
// ls-tree and a show, ListAtCommit's way) were some 1,200 git starts a run,
// which on a self-hosted runner's reused workspace took the test past 1m30s
// and its CI shard past the two-minute cap. The lookup answers what
// ListAtCommit answers for each of those paths (TestTheSinglePassReadsWhatTheTwoCallPathRead);
// a path the base carries but this read did not print is an error, never a
// silent "absent".
func readMergeBase(git ledgerGit, root, base string) (baseLookup, []string, error) {
	listing, err := git(root, "", append([]string{"ls-tree", "-r", "-z", "--full-tree", base, "--"}, ledgerBasePaths...)...)
	if err != nil {
		return nil, nil, fmt.Errorf("listing the merge base %s: %w", base, err)
	}
	inBase := map[string]bool{}     // every blob path the base carries under ledgerBasePaths
	objectOf := map[string]string{} // the .txt ones the batch reads, by path
	var order []string
	for _, entry := range strings.Split(listing, "\x00") {
		meta, rel, ok := strings.Cut(entry, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta) // <mode> <type> <object>
		if len(fields) != 3 || fields[1] != "blob" {
			continue
		}
		inBase[rel] = true
		if strings.HasSuffix(rel, ".txt") {
			objectOf[rel] = fields[2]
			order = append(order, rel)
		}
	}
	texts := make(map[string]string, len(order))
	basePaths := make([]string, 0, len(inBase))
	for rel := range inBase {
		basePaths = append(basePaths, rel)
	}
	sort.Strings(basePaths)
	if len(order) > 0 {
		var names strings.Builder
		for _, rel := range order {
			names.WriteString(objectOf[rel] + "\n")
		}
		batch, err := git(root, names.String(), "cat-file", "--batch")
		if err != nil {
			return nil, nil, fmt.Errorf("reading the merge base %s: %w", base, err)
		}
		for _, rel := range order {
			header, rest, ok := strings.Cut(batch, "\n")
			fields := strings.Fields(header) // <object> blob <size>
			if !ok || len(fields) != 3 || fields[0] != objectOf[rel] || fields[1] != "blob" {
				return nil, nil, fmt.Errorf("reading %s at the merge base %s: git cat-file --batch printed %q, want %q's blob header", rel, base, header, objectOf[rel])
			}
			size, err := strconv.Atoi(fields[2])
			if err != nil || size < 0 || size+1 > len(rest) || rest[size] != '\n' {
				return nil, nil, fmt.Errorf("reading %s at the merge base %s: git cat-file --batch printed a %q body that does not fit what remains", rel, base, header)
			}
			texts[rel], batch = rest[:size], rest[size+1:]
		}
	}
	return func(rel string) (string, bool, error) {
		if !inBase[rel] {
			return "", false, nil
		}
		text, ok := texts[rel]
		if !ok {
			return "", false, fmt.Errorf("%s is at the merge base %s but the single pass reads only .txt files", rel, base)
		}
		return text, true, nil
	}, basePaths, nil
}

// ledgerRatchetShards walks every counted shard under internal/ci/testdata:
// the .txt files at least one directory deep whose head text or base text
// carries a "# ceiling:" line (fixtures without a ceiling stay out). A brand-new
// shard with a ceiling is kept so the ratchet reports it as all growth.
func ledgerRatchetShards(root string, atBase baseLookup) ([]string, error) {
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
		if atBase != nil {
			if baseText, ok, err := atBase(relShard); err == nil && ok {
				_, baseCeil = parseLedgerRows(baseText)
			}
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

// checkCountedShards checks every counted shard against its merge base version.
// A ledger named in seedLedgerDirs is its one seed when no shard of it exists in
// the base (docs/SPEC-CI.md, `unit-sockets` and `refusal-grammar`); after that,
// every shard ratchets.
func checkCountedShards(root, base string, shards, baseShards []string, atBase baseLookup) ([]string, error) {
	seeds := map[string]bool{}
	for _, dir := range seedLedgerDirs {
		seeds[dir] = ledgerSeedIsNew(shards, baseShards, dir)
	}
	var problems []string
	for _, rel := range shards {
		seeded := false
		for _, dir := range seedLedgerDirs {
			if seeds[dir] && strings.HasPrefix(rel, dir) {
				seeded = true
			}
		}
		if seeded {
			continue
		}
		headBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		baseText, ok, err := atBase(rel)
		if err != nil {
			return nil, fmt.Errorf("%s at %s: %w", rel, base, err)
		}
		if old, moved := shardBeforePkgMove(rel); !ok && moved {
			baseText, ok, err = atBase(old)
			if err != nil {
				return nil, fmt.Errorf("%s at %s: %w", old, base, err)
			}
			baseText = rowsAfterPkgMove(baseText, rel)
		}
		problems = append(problems, shardProblems(rel, base, baseText, ok, headBytes)...)
	}
	return problems, nil
}

// shardBeforePkgMove names the path a shard of a pkg/ package had before its package
// moved from internal/ (split L1): <ledger>/pkg/<x>.txt was <ledger>/internal/<x>.txt.
// The moved shard is the same ledger, so it is compared with the base's shard at the old
// path, and a row it gained is still growth. A shard path with no pkg/ part is not moved.
func shardBeforePkgMove(rel string) (string, bool) {
	const dir = "internal/ci/testdata/"
	ledger, rest, ok := strings.Cut(strings.TrimPrefix(rel, dir), "/")
	if !ok || !strings.HasPrefix(rel, dir) || !strings.HasPrefix(rest, "pkg/") {
		return "", false
	}
	return dir + ledger + "/internal/" + strings.TrimPrefix(rest, "pkg/"), true
}

// rowsAfterPkgMove reads a moved shard's base rows as the move wrote them: the package's
// own paths, internal/<x>/<file> and internal/<x> standing alone, name pkg/<x>, where
// <ledger>/pkg/<x>.txt is the shard. Nothing else in the text changes.
func rowsAfterPkgMove(baseText, rel string) string {
	_, rest, _ := strings.Cut(strings.TrimPrefix(rel, "internal/ci/testdata/"), "/")
	x := strings.TrimSuffix(strings.TrimPrefix(rest, "pkg/"), ".txt")
	from, to := "internal/"+x, "pkg/"+x
	var out strings.Builder
	for i := 0; i < len(baseText); {
		j := strings.Index(baseText[i:], from)
		if j < 0 {
			out.WriteString(baseText[i:])
			break
		}
		at, end := i+j, i+j+len(from)
		before := at == 0 || strings.ContainsRune(" \t\n`\"(", rune(baseText[at-1]))
		after := end == len(baseText) || strings.ContainsRune("/: \t\n`\")", rune(baseText[end]))
		out.WriteString(baseText[i:at])
		if before && after {
			out.WriteString(to)
		} else {
			out.WriteString(from)
		}
		i = end
	}
	return out.String()
}

// ledgerSeedIsNew reports whether this tree introduces the first shard of the
// ledger under dir; the initial seed is exempt only while the merge base holds
// no shard of it (docs/SPEC-CI.md, `unit-sockets` and `refusal-grammar`).
// baseShards is the complete set the base carries, not only the head's names,
// so a base shard this change deletes still counts as a seed that happened.
func ledgerSeedIsNew(shards, baseShards []string, dir string) bool {
	for _, rel := range baseShards {
		if strings.HasSuffix(rel, ".txt") && strings.HasPrefix(rel, dir) {
			return false
		}
	}
	for _, rel := range shards {
		if strings.HasPrefix(rel, dir) {
			return true
		}
	}
	return false
}

// checkSlowTestsLedger checks slow-tests_allowlist.txt against the merge base.
func checkSlowTestsLedger(t testing.TB, root, base string, atBase baseLookup) ([]string, error) {
	headBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(slowTestsAllowlistPath)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", slowTestsAllowlistPath, err)
	}
	baseText, ok, err := atBase(slowTestsAllowlistPath)
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
func checkSleepsLedger(t testing.TB, root, base string, atBase baseLookup) ([]string, error) {
	headBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(sleepsSkipsAllowlistPath)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", sleepsSkipsAllowlistPath, err)
	}
	baseText, ok, err := atBase(sleepsSkipsAllowlistPath)
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
	return ledgerRatchetFindings(t, gitOutIn, root, base)
}

// ledgerRatchetFindings is the ratchet over a resolved base: the merge base is
// read once through git (readMergeBase), then every counted shard and the two
// slowtests ledgers are compared with what that one read returned.
func ledgerRatchetFindings(t testing.TB, git ledgerGit, root, base string) ([]string, error) {
	atBase, baseShards, err := readMergeBase(git, root, base)
	if err != nil {
		return nil, err
	}
	shards, err := ledgerRatchetShards(root, atBase)
	if err != nil {
		return nil, err
	}
	if len(shards) == 0 {
		return nil, fmt.Errorf("no counted shards under internal/ci/testdata: the walk is broken, not the tree")
	}
	shardProbs, err := checkCountedShards(root, base, shards, baseShards, atBase)
	if err != nil {
		return nil, err
	}
	slowProbs, err := checkSlowTestsLedger(t, root, base, atBase)
	if err != nil {
		return nil, err
	}
	sleepProbs, err := checkSleepsLedger(t, root, base, atBase)
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

// TestUnitSocketsLedgerSeedsOnlyWhenTheBaseHasNoShard pins the one-time seed
// exception for the newly introduced rule (docs/SPEC-CI.md, `unit-sockets`).
func TestUnitSocketsLedgerSeedsOnlyWhenTheBaseHasNoShard(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	newShard := unitSocketsSeedDir + "cmd/nova-friend.txt"
	existingShard := unitSocketsSeedDir + "cmd/nova-sandbox.txt"
	writeLedgerGuardFixture(t, root, newShard, "# ceiling: 1\ncmd/nova-friend/inbox_test.go:socket 1 reason\n")
	writeLedgerGuardFixture(t, root, existingShard, "# ceiling: 1\ncmd/nova-sandbox/main_test.go:socket 1 reason\n")
	shards := []string{existingShard, newShard}
	missing := func(string) (string, bool, error) { return "", false, nil }

	problems, err := checkCountedShards(root, "123456789abcdef", shards, nil, missing)
	require.NoError(t, err)
	require.Empty(t, problems, "the unit-sockets ledger is seeded at its introduction")

	baseHasAnotherShard := func(rel string) (string, bool, error) {
		if rel == existingShard {
			return "# ceiling: 1\ncmd/nova-sandbox/main_test.go:socket 1 reason\n", true, nil
		}
		return "", false, nil
	}
	seededBase := []string{existingShard}
	problems, err = checkCountedShards(root, "123456789abcdef", shards, seededBase, baseHasAnotherShard)
	require.NoError(t, err)
	require.Equal(t, []string{
		newShard + " is not in the merge base 123456789: a new shard is all growth; justify it beside the rule it measures",
	}, problems, "a later new package shard remains growth after the ledger has a base")

	// Deleting the ledger's last base shard is a shrink, not a re-seed.
	problems, err = checkCountedShards(root, "123456789abcdef", nil, seededBase, baseHasAnotherShard)
	require.NoError(t, err)
	require.Empty(t, problems, "deleting the ledger's last shard adds nothing to judge and does not reopen the seed")

	// A base shard deleted and replaced by a differently named head shard is
	// not a new ledger: the seed does not reopen, so the new shard is growth.
	replaced := []string{newShard}
	problems, err = checkCountedShards(root, "123456789abcdef", replaced, seededBase, baseHasAnotherShard)
	require.NoError(t, err)
	require.Equal(t, []string{
		newShard + " is not in the merge base 123456789: a new shard is all growth; justify it beside the rule it measures",
	}, problems, "replacing the base's last shard does not reopen the seed")
}

// TestRefusalGrammarLedgerSeedsOnlyWhenTheBaseHasNoShard pins the one-time seed
// exception for the newly introduced rule (docs/SPEC-CI.md, `refusal-grammar`).
func TestRefusalGrammarLedgerSeedsOnlyWhenTheBaseHasNoShard(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	newShard := refusalGrammarSeedDir + "cmd/nova-redis.txt"
	existingShard := refusalGrammarSeedDir + "cmd/nova-sandbox.txt"
	writeLedgerGuardFixture(t, root, newShard, "# ceiling: 1\ncmd/nova-redis:verb-no-flags 1 reason\n")
	writeLedgerGuardFixture(t, root, existingShard, "# ceiling: 1\ncmd/nova-sandbox:verb-no-flags 1 reason\n")
	shards := []string{existingShard, newShard}
	missing := func(string) (string, bool, error) { return "", false, nil }

	problems, err := checkCountedShards(root, "123456789abcdef", shards, nil, missing)
	require.NoError(t, err)
	require.Empty(t, problems, "the refusal-grammar ledger is seeded at its introduction")

	baseHasAnotherShard := func(rel string) (string, bool, error) {
		if rel == existingShard {
			return "# ceiling: 1\ncmd/nova-sandbox:verb-no-flags 1 reason\n", true, nil
		}
		return "", false, nil
	}
	seededBase := []string{existingShard}
	problems, err = checkCountedShards(root, "123456789abcdef", shards, seededBase, baseHasAnotherShard)
	require.NoError(t, err)
	require.Equal(t, []string{
		newShard + " is not in the merge base 123456789: a new shard is all growth; justify it beside the rule it measures",
	}, problems, "a later new package shard remains growth after the ledger has a base")

	// A base shard deleted and replaced by a differently named head shard is
	// not a new ledger: the seed does not reopen, so the new shard is growth.
	replaced := []string{newShard}
	problems, err = checkCountedShards(root, "123456789abcdef", replaced, seededBase, baseHasAnotherShard)
	require.NoError(t, err)
	require.Equal(t, []string{
		newShard + " is not in the merge base 123456789: a new shard is all growth; justify it beside the rule it measures",
	}, problems, "replacing the base's last shard does not reopen the seed")
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

// ratchetGit runs git in dir under the shared test identity (pkg/testgit)
// and returns its standard output; a failure ends the test.
func ratchetGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = testgit.Environ()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	require.NoError(t, err, "git %v: %s", args, stderr.String())
	return string(out)
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

	ratchetGit(t, dir, "init", "--quiet")
	ratchetGit(t, dir, "config", "commit.gpgSign", "false")

	// Base commit has an established shard and a ceiling-less fixture.
	shardRel := "internal/ci/testdata/sample/shard.txt"
	fixtureRel := "internal/ci/testdata/waits/fixture.go.txt"
	baseShard := "# a rule's shard\nrow-a  reason\nrow-b  reason\n# ceiling: 2\n"
	fixtureCode := "package waits\n\nfunc Wait() {}\n"

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "internal/ci/testdata/sample"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "internal/ci/testdata/waits"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(shardRel)), []byte(baseShard), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(fixtureRel)), []byte(fixtureCode), 0o644))

	ratchetGit(t, dir, "add", ".")
	ratchetGit(t, dir, "commit", "--quiet", "-m", "base")
	baseCommit := strings.TrimSpace(ratchetGit(t, dir, "rev-parse", "HEAD"))

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
	atBase, baseShards, err := readMergeBase(gitOutIn, dir, baseCommit)
	require.NoError(t, err, "readMergeBase")
	shards, err := ledgerRatchetShards(dir, atBase)
	require.NoError(t, err, "ledgerRatchetShards")
	require.Equal(t, []string{newShardRel, shardRel}, shards, "only counted shards with ceilings are selected")

	// checkCountedShards reports the expected growth problems.
	problems, err := checkCountedShards(dir, baseCommit, shards, baseShards, atBase)
	require.NoError(t, err, "checkCountedShards")
	require.ElementsMatch(t, []string{
		fmt.Sprintf("%s is not in the merge base %s: a new shard is all growth; justify it beside the rule it measures", newShardRel, baseCommit[:9]),
		fmt.Sprintf("%s adds the row %q, which its merge base %s lacks; the ledgers only shrink: fix the code and drop the row", shardRel, "row-c", baseCommit[:9]),
		fmt.Sprintf("%s raises its ceiling over the merge base %s; a ceiling only falls", shardRel, baseCommit[:9]),
	}, problems)

	// Explicitly passing ceiling-less fixtures to checkCountedShards yields no problems.
	fixtureProblems, err := checkCountedShards(dir, baseCommit, []string{fixtureRel, newFixtureRel}, baseShards, atBase)
	require.NoError(t, err, "checkCountedShards on fixtures")
	require.Empty(t, fixtureProblems, "ceiling-less fixtures produce no ratchet problems")
}

// fakeBaseGit is a merge base held in memory: it answers the git commands the
// ratchet's read of the base may start (the per-file `ls-tree --name-only` and
// `show`, the tree-wide `ls-tree -r -z` and `cat-file --batch`) and records
// every one it was asked to run.
type fakeBaseGit struct {
	files map[string]string // path at the base -> its text
	calls []string
}

// fakeBlobName is the object name the fake gives a path's blob.
func fakeBlobName(rel string) string {
	return fmt.Sprintf("%040x", len(rel)) + "-" + rel
}

func (f *fakeBaseGit) run(_, stdin string, args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch {
	case len(args) == 5 && args[0] == "ls-tree" && args[1] == "--name-only":
		if _, ok := f.files[args[4]]; ok {
			return args[4] + "\n", nil
		}
		return "", nil
	case len(args) == 2 && args[0] == "show":
		_, rel, _ := strings.Cut(args[1], ":")
		return f.files[rel], nil
	case len(args) > 2 && args[0] == "ls-tree" && args[1] == "-r":
		var prefixes []string
		for i, a := range args {
			if a == "--" {
				prefixes = args[i+1:]
			}
		}
		var rels []string
		for rel := range f.files {
			for _, p := range prefixes {
				if rel == p || strings.HasPrefix(rel, p+"/") {
					rels = append(rels, rel)
					break
				}
			}
		}
		sort.Strings(rels)
		var out strings.Builder
		for _, rel := range rels {
			fmt.Fprintf(&out, "100644 blob %s\t%s\x00", fakeBlobName(rel), rel)
		}
		return out.String(), nil
	case len(args) == 2 && args[0] == "cat-file" && args[1] == "--batch":
		var out strings.Builder
		for _, name := range strings.Fields(stdin) {
			_, rel, _ := strings.Cut(name, "-")
			text, ok := f.files[rel]
			if !ok {
				fmt.Fprintf(&out, "%s missing\n", name)
				continue
			}
			fmt.Fprintf(&out, "%s blob %d\n%s\n", name, len(text), text)
		}
		return out.String(), nil
	}
	return "", fmt.Errorf("fake git: unexpected command %q", strings.Join(args, " "))
}

// writeRepoFile writes text at rel under root, making its directories.
func writeRepoFile(t *testing.T, root, rel, text string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(text), 0o644))
}

// TestTheLedgerTestReadsTheBaseOnce holds the ratchet to two git processes for
// the whole merge base, however many shards the tree carries: on the
// self-hosted runners each git start cost tens of milliseconds, and two per
// file over some 300 files ran TestClassRuleLedgersOnlyShrinkAgainstMergeBase
// to 1m40s and its shard past the two-minute cap. The findings are the ones the
// per-file read gave: one added row in one grown shard, nothing else.
func TestTheLedgerTestReadsTheBaseOnce(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	base := strings.Repeat("ab", 20)
	fake := &fakeBaseGit{files: map[string]string{}}
	const shards = 60
	for i := range shards {
		rel := fmt.Sprintf("internal/ci/testdata/rule%02d/shard.txt", i)
		text := fmt.Sprintf("# rule %d\nrow-a  reason\n# ceiling: 1\n", i)
		fake.files[rel] = text
		if i == 7 {
			text = fmt.Sprintf("# rule %d\nrow-a  reason\nrow-b  reason\n# ceiling: 1\n", i)
		}
		writeRepoFile(t, root, rel, text)
	}
	fixture := "internal/ci/testdata/waits/fixture.go.txt"
	fake.files[fixture] = "package waits\n"
	writeRepoFile(t, root, fixture, "package waits\n\nfunc More() {}\n")
	slow := "# slow\ninternal/pkg\t-\t14.1\t9.4s@studio\n"
	sleeps := "# sleeps\ninternal/pkg\tFuncA\twhere.go:10\n"
	fake.files[slowTestsAllowlistPath], fake.files[sleepsSkipsAllowlistPath] = slow, sleeps
	writeRepoFile(t, root, slowTestsAllowlistPath, slow)
	writeRepoFile(t, root, sleepsSkipsAllowlistPath, sleeps)

	problems, err := ledgerRatchetFindings(t, fake.run, root, base)
	require.NoError(t, err)
	require.Equal(t, []string{
		fmt.Sprintf("internal/ci/testdata/rule07/shard.txt adds the row %q, which its merge base %s lacks; the ledgers only shrink: fix the code and drop the row", "row-b", base[:9]),
	}, problems)
	require.Len(t, fake.calls, 2, "the ratchet read the merge base of %d shards with %d git processes, want one ls-tree and one cat-file --batch; the first: %q", shards, len(fake.calls), fake.calls[:min(4, len(fake.calls))])
	require.True(t, strings.HasPrefix(fake.calls[0], "ls-tree -r"), "first call %q, want the tree-wide ls-tree", fake.calls[0])
	require.Equal(t, "cat-file --batch", fake.calls[1])
}

// TestTheSinglePassReadsWhatTheTwoCallPathRead builds a repository with
// shards, fixtures, an empty file, a file with no final newline, a file with
// spaces in its name, and the two slowtests ledgers, and holds the single-pass
// read of the base to ListAtCommit's answer for every path: present or not,
// byte for byte, the same length; then the shard list and the findings the
// ratchet derives from each read are the same.
func TestTheSinglePassReadsWhatTheTwoCallPathRead(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	ratchetGit(t, dir, "init", "--quiet")
	ratchetGit(t, dir, "config", "commit.gpgSign", "false")
	baseFiles := map[string]string{
		"internal/ci/testdata/sample/shard.txt":         "# shard\nrow-a  reason\nrow-b  reason\n# ceiling: 2\n",
		"internal/ci/testdata/sample/deep/nested.txt":   "# nested\nrow-n  reason\n# ceiling: 1\n",
		"internal/ci/testdata/sample/no newline.txt":    "# spaced name\nrow-s  reason\n# ceiling: 1",
		"internal/ci/testdata/sample/empty.txt":         "",
		"internal/ci/testdata/waits/fixture.go.txt":     "package waits\n\nfunc Wait() {}\n",
		"internal/ci/testdata/waits/binary.bin":         "\x00\x01\x02",
		"internal/ci/testdata/top-level.txt":            "# ceiling: 9\n",
		"internal/ci/testdata/gone/removed.txt":         "# removed\nrow-r  reason\n# ceiling: 1\n",
		slowTestsAllowlistPath:                          "# slow\ninternal/pkg\t-\t14.1\t9.4s@studio\n",
		sleepsSkipsAllowlistPath:                        "# sleeps\ninternal/pkg\tFuncA\twhere.go:10\n",
		"internal/ci/testdata/sample/shard-ceiling.txt": "# ceiling: 3\nrow-x  reason\n",
	}
	for rel, text := range baseFiles {
		writeRepoFile(t, dir, rel, text)
	}
	ratchetGit(t, dir, "add", ".")
	ratchetGit(t, dir, "commit", "--quiet", "-m", "base")
	base := strings.TrimSpace(ratchetGit(t, dir, "rev-parse", "HEAD"))

	// The head: a grown shard, a raised ceiling, a new shard, a removed one.
	writeRepoFile(t, dir, "internal/ci/testdata/sample/shard.txt", "# shard\nrow-a  reason\nrow-b  reason\nrow-c  reason\n# ceiling: 3\n")
	writeRepoFile(t, dir, "internal/ci/testdata/newrule/shard.txt", "# new\nrow-z  reason\n# ceiling: 1\n")
	writeRepoFile(t, dir, slowTestsAllowlistPath, "# slow\ninternal/pkg\t-\t15.0\t9.4s@studio\ninternal/pkg\tTestC\t1.0\t0.5s@bench\n")
	require.NoError(t, os.Remove(filepath.Join(dir, filepath.FromSlash("internal/ci/testdata/gone/removed.txt"))))

	twoCall := func(rel string) (string, bool, error) { return ListAtCommit(dir, base, rel) }
	twoCallShards := make([]string, 0, len(baseFiles))
	for rel := range baseFiles {
		twoCallShards = append(twoCallShards, rel)
	}
	sort.Strings(twoCallShards)
	single, singleShards, err := readMergeBase(gitOutIn, dir, base)
	require.NoError(t, err)

	paths := []string{slowTestsAllowlistPath, sleepsSkipsAllowlistPath, "internal/ci/testdata/newrule/shard.txt", "internal/ci/testdata/absent/nothing.txt"}
	for rel := range baseFiles {
		if strings.HasSuffix(rel, ".txt") {
			paths = append(paths, rel)
		}
	}
	sort.Strings(paths)
	for _, rel := range paths {
		wantText, wantOK, wantErr := twoCall(rel)
		require.NoError(t, wantErr, "%s by ListAtCommit", rel)
		gotText, gotOK, gotErr := single(rel)
		require.NoError(t, gotErr, "%s by the single pass", rel)
		require.Equal(t, wantOK, gotOK, "%s: present at the base by ListAtCommit %v, by the single pass %v", rel, wantOK, gotOK)
		require.Equal(t, len(wantText), len(gotText), "%s: size by ListAtCommit %d, by the single pass %d", rel, len(wantText), len(gotText))
		require.Equal(t, wantText, gotText, "%s: text differs", rel)
	}

	wantShards, err := ledgerRatchetShards(dir, twoCall)
	require.NoError(t, err)
	gotShards, err := ledgerRatchetShards(dir, single)
	require.NoError(t, err)
	require.Equal(t, wantShards, gotShards, "the shard list")
	require.NotEmpty(t, gotShards)

	for _, check := range []func(baseLookup, []string) ([]string, error){
		func(at baseLookup, bs []string) ([]string, error) {
			return checkCountedShards(dir, base, gotShards, bs, at)
		},
		func(at baseLookup, bs []string) ([]string, error) { return checkSlowTestsLedger(t, dir, base, at) },
		func(at baseLookup, bs []string) ([]string, error) { return checkSleepsLedger(t, dir, base, at) },
	} {
		want, err := check(twoCall, twoCallShards)
		require.NoError(t, err)
		got, err := check(single, singleShards)
		require.NoError(t, err)
		require.Equal(t, want, got, "the findings")
	}
}

// TestAShardThatMovedToPkgIsComparedWithItsOldPath is the moved-shard reading's reversed
// witness: a shard under <ledger>/pkg/ whose base holds the same rows under
// <ledger>/internal/ is no growth, a row it adds is growth, and a pkg/ shard with no shard
// at either path in the base is still all growth.
func TestAShardThatMovedToPkgIsComparedWithItsOldPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const moved, fresh = "internal/ci/testdata/errcheck/pkg/x.txt", "internal/ci/testdata/errcheck/pkg/y.txt"
	baseRows := "# ceiling: 1\ninternal/x/a.go:1 reason\n"
	headRows := "# ceiling: 1\npkg/x/a.go:1 reason\n"
	writeLedgerGuardFixture(t, root, moved, headRows)
	writeLedgerGuardFixture(t, root, fresh, headRows)
	atBase := func(rel string) (string, bool, error) {
		if rel == "internal/ci/testdata/errcheck/internal/x.txt" {
			return baseRows, true, nil
		}
		return "", false, nil
	}
	const base = "0123456789abcdef"
	problems, err := checkCountedShards(root, base, []string{moved, fresh}, nil, atBase)
	require.NoError(t, err)
	require.Len(t, problems, 1, "%q", problems)
	require.Contains(t, problems[0], fresh+" is not in the merge base")

	writeLedgerGuardFixture(t, root, moved, headRows+"pkg/x/b.go:2 reason\n")
	problems, err = checkCountedShards(root, base, []string{moved}, nil, atBase)
	require.NoError(t, err)
	require.Len(t, problems, 1, "%q", problems)
	require.Contains(t, problems[0], moved+" adds the row")

	old, ok := shardBeforePkgMove("internal/ci/testdata/errcheck/internal/x.txt")
	require.False(t, ok, old)
	require.Equal(t, "internal/xy/a.go:1 r\npkg/x/a.go:1 r\npkg/x 1 r\n", rowsAfterPkgMove("internal/xy/a.go:1 r\ninternal/x/a.go:1 r\ninternal/x 1 r\n", moved))
}
