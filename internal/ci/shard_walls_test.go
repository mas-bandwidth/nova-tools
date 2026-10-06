package ci

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/slowtests"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const shardWallsPath = "internal/ci/testdata/shard-walls.tsv"

// shardWallRows reads measured package walls with their green-run provenance.
func shardWallRows(text string) (map[string]string, error) {
	rows := map[string]string{}
	for n, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 4 {
			return nil, fmt.Errorf("shard walls line %d wants package, seconds, green run and head", n+1)
		}
		seconds, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			return nil, fmt.Errorf("shard walls line %d has invalid seconds", n+1)
		}
		if _, err := strconv.ParseUint(fields[2], 10, 64); err != nil || len(fields[3]) != 40 {
			return nil, fmt.Errorf("shard walls line %d lacks green-run provenance", n+1)
		}
		if _, exists := rows[fields[0]]; exists {
			return nil, fmt.Errorf("shard walls repeats %s", fields[0])
		}
		rows[fields[0]] = line
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("shard walls has no measurements")
	}
	return rows, nil
}

// shardWallProblems applies the card's 60-second review boundary to completed
// package events: crossing it requires this package's measured row to change.
func shardWallProblems(events []slowtests.Event, head, base map[string]string) []string {
	var problems []string
	for _, event := range events {
		if event.Test != "" || event.Action != "pass" {
			continue
		}
		pkg := strings.TrimPrefix(event.Package, "github.com/mas-bandwidth/nova-tools/")
		row, ok := head[pkg]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s has no measured shard-wall row", pkg))
			continue
		}
		if event.Elapsed > 60 && row == base[pkg] {
			problems = append(problems, fmt.Sprintf("%s grew to %.3fs past 60s without a shard-wall ledger change", pkg, event.Elapsed))
		}
	}
	return problems
}

// TestEveryCLPackageFitsItsShardWall checks the ledger locally and, after each
// CI shard, its completed JSON stream against the merge-base ledger.
func TestEveryCLPackageFitsItsShardWall(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, shardWallsPath))
	require.NoError(t, err)
	rows, err := shardWallRows(string(raw))
	require.NoError(t, err)
	stream := os.Getenv("NOVA_CI_SHARD_WALLS_JSON")
	if stream == "" {
		return
	} // local gates validate provenance; CI supplies measured events
	file, err := os.Open(stream)
	require.NoError(t, err)
	defer file.Close() // ignored: closing a read-only completed test stream has no write to flush
	var events []slowtests.Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	needsBase := false
	completed := 0
	for scanner.Scan() {
		var event slowtests.Event
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &event))
		events = append(events, event)
		if event.Action == "pass" && event.Test == "" {
			completed++
		}
		if event.Action == "pass" && event.Test == "" && event.Elapsed > 60 {
			needsBase = true
		}
	}
	require.NoError(t, scanner.Err())
	require.Positive(t, completed, "CI needs completed package events")
	baseRows := rows
	if needsBase {
		base, err := deprecatedImportsAllowlistBase(root)
		require.NoError(t, err)
		baseText, exists, err := ListAtCommit(root, base, shardWallsPath)
		require.NoError(t, err)
		baseRows = map[string]string{}
		if exists {
			baseRows, err = shardWallRows(baseText)
			require.NoError(t, err)
		}
	}
	assert.Empty(t, shardWallProblems(events, rows, baseRows))
}

// TestShardWallGrowthNeedsItsOwnMeasuredRow pins unchanged, changed, missing,
// per-test and exactly-at-boundary observations.
func TestShardWallGrowthNeedsItsOwnMeasuredRow(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name       string
		seconds    float64
		head, base map[string]string
		test       string
		want       int
	}{
		{"unchanged", 61, map[string]string{"p": "old"}, map[string]string{"p": "old"}, "", 1},
		{"changed", 61, map[string]string{"p": "new"}, map[string]string{"p": "old"}, "", 0},
		{"other row changed", 61, map[string]string{"p": "old", "q": "new"}, map[string]string{"p": "old", "q": "old"}, "", 1},
		{"boundary", 60, map[string]string{"p": "old"}, map[string]string{"p": "old"}, "", 0},
		{"missing", 1, map[string]string{}, map[string]string{}, "", 1},
		{"test observation", 61, map[string]string{}, map[string]string{}, "TestOne", 0},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			assert.Len(t, shardWallProblems([]slowtests.Event{{Action: "pass", Package: "p", Test: row.test, Elapsed: row.seconds}}, row.head, row.base), row.want)
		})
	}
}
