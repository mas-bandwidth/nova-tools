package cardstatus

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/redis/go-redis/v9"
)

// CardPrefix is the Redis key prefix for card status hashes: card:<label>.
const CardPrefix = "card:"

// Card is one card's status read from its RESULT.md.
type Card struct {
	Label string
	SHA   string
	Line1 string
}

// WriteResults reads card results from resultsDir (each <label>/RESULT.md),
// writes each card's status to a Redis hash card:<label>, and creates empty
// posted.tsv and reports.tsv in outDir. resultsDir is required; outDir "" means
// no output files.
func WriteResults(ctx context.Context, resultsDir, outDir string, rdb *redis.Client) error {
	entries, err := os.ReadDir(resultsDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		label := entry.Name()
		c, err := readCard(resultsDir, label)
		if err != nil {
			continue
		}
		key := CardPrefix + label
		if err := rdb.HSet(ctx, key, map[string]string{
			"label": c.Label,
			"sha":   c.SHA,
			"line1": c.Line1,
		}).Err(); err != nil {
			return fmt.Errorf("hset %s: %w", key, err)
		}
	}

	if outDir != "" {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return err
		}
		for _, name := range []string{"posted.tsv", "reports.tsv"} {
			f, err := os.Create(filepath.Join(outDir, name))
			if err != nil {
				return err
			}
			f.Close()
		}
	}

	return nil
}

// ScanLabels returns every label whose card:<label> hash exists in Redis,
// sorted. The set is what an HSCAN over card:* keys finds: every key is one
// card's hash, and reading all of them answers what the set of cards is.
func ScanLabels(ctx context.Context, rdb *redis.Client) ([]string, error) {
	var labels []string
	var cursor uint64
	for {
		keys, next, err := rdb.Scan(ctx, cursor, CardPrefix+"*", 128).Result()
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			labels = append(labels, strings.TrimPrefix(k, CardPrefix))
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return labels, nil
}

// readCard reads one label's RESULT.md from the results directory.
func readCard(resultsDir, label string) (Card, error) {
	path := filepath.Join(resultsDir, label, "RESULT.md")
	f, err := os.Open(path)
	if err != nil {
		return Card{}, err
	}
	defer f.Close()

	c := Card{Label: label}
	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		c.Line1 = scanner.Text()
		for _, f := range strings.Fields(c.Line1) {
			if strings.HasPrefix(f, "sha=") {
				c.SHA = strings.TrimPrefix(f, "sha=")
			}
		}
	}
	return c, scanner.Err()
}
