// nova-swarm launch: ONE launch verb replaces 27 launcher scripts (10 linux,
// 7 darwin twins, 5 Studio, rr/rrpro/rr-run). A registry of providers exists;
// the label's hash selects which row runs. No script per provider.
//
// The verb reads a TSV of provider<TAB>model<TAB>harness rows, selects the
// row whose index is hash(label) mod len(rows), and prints one LAUNCH OK line
// naming the selected provider and model. The caller invokes the harness with
// those fields.
package main

import (
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// launchRow is one row of the providers registry the --providers flag names.
type launchRow struct {
	provider string
	model    string
	harness  string
}

// cmdLaunch selects a provider from a registry by label hash. It replaces
// the 27 launcher scripts the issue describes: providers-flash.txt,
// providers-pro.txt, and their darwin/Studio/rr twins.
func cmdLaunch(args []string, stdout, stderr io.Writer) int {
	f := newFlags("launch")
	providers := f.fs.String("providers", "", "")
	label := f.fs.String("label", "", "")
	cardPath := f.fs.String("card", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*providers, "providers", "a TSV of provider<TAB>model<TAB>harness rows, chosen by label hash")
	f.want(*label, "label", "the label whose hash selects the provider row")
	f.want(*cardPath, "card", "the card file this launch runs")
	if f.refused(stderr) {
		return 2
	}
	rows, err := loadLaunchRegistry(*providers)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm launch: %s\n", oneline.Err(err))
		return 2
	}
	if len(rows) == 0 {
		fmt.Fprintf(stderr, "nova-swarm launch: %s has no provider rows; it wants at least one provider<TAB>model<TAB>harness line\n", oneline.Field(*providers))
		return 2
	}
	// THE HASH SELECTION: the label's FNV-1a hash mod the row count picks the
	// provider. The same label always selects the same row, so a fill loop
	// that names the same label twice does not drift across providers.
	idx := launchHash(*label, len(rows))
	row := rows[idx]
	fmt.Fprintf(stdout, "LAUNCH OK label=%s provider=%s model=%s harness=%s registry=%s rows=%d selected=%d\n",
		oneline.Field(*label), oneline.Field(row.provider), oneline.Field(row.model),
		oneline.Field(row.harness), oneline.Field(*providers), len(rows), idx)
	return 0
}

// loadLaunchRegistry reads a TSV of provider<TAB>model<TAB>harness rows.
// Empty lines and lines starting with # are skipped.
func loadLaunchRegistry(path string) ([]launchRow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--providers wants a readable TSV file: %w", err)
	}
	var rows []launchRow
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		r := launchRow{provider: fields[0], model: fields[1]}
		if len(fields) >= 3 {
			r.harness = fields[2]
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// launchHash selects a provider index from the label's FNV-1a hash.
func launchHash(label string, n int) int {
	h := fnv.New32a()
	h.Write([]byte(label))
	return int(h.Sum32()) % n
}
