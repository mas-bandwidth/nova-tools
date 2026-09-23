// nova-swarm launch: ONE launch verb replaces 27 launcher scripts (10 linux,
// 7 darwin twins, 5 Studio, rr/rrpro/rr-run). A registry of providers exists;
// the label's hash selects which row runs, and the verb RUNS the card on that
// row. No script per provider.
//
//	nova-swarm launch --providers <tsv> --label <text> --card <file> [--dry-run] [--ssh <bin>] -- <native flags>
//
// The registry is a TSV of provider<TAB>model<TAB>harness[<TAB>host] rows. The
// row at hash(label) mod len(rows) is selected. A row with no host runs the
// card here, through the same `native` path every bench already trusts (its
// slot lease, wall, deadline, and the results root that keeps the output after
// the job directory is swept). A row with a host runs the same `native` verb on
// that host over ssh, with the card text on standard input, so the card is
// never copied to a path that can be lost. Everything after `--` is handed to
// `native` unchanged (--slot, --root, --deadline, --tokens, --slots-store,
// --owner, --results-root, ...); launch supplies only --harness, --model,
// --label and --card from the selected row.
//
// --dry-run selects and prints the row without running anything; it prints
// LAUNCH DRY, never LAUNCH OK. LAUNCH OK is printed only after the card ran
// and `native` exited 0.
package main

import (
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// launchRow is one row of the providers registry the --providers flag names.
type launchRow struct {
	provider string
	model    string
	harness  string
	host     string // empty = run here; otherwise the ssh destination
}

// cmdLaunch selects a provider from a registry by label hash and runs the card
// on it. It replaces the 27 launcher scripts the issue describes:
// providers-flash.txt, providers-pro.txt, and their darwin/Studio/rr twins.
func cmdLaunch(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	own, nativeArgs := splitLaunchArgs(args)
	f := newFlags("launch")
	providers := f.fs.String("providers", "", "")
	label := f.fs.String("label", "", "")
	cardPath := f.fs.String("card", "", "")
	dryRun := f.fs.Bool("dry-run", false, "")
	sshBin := f.fs.String("ssh", "ssh", "")
	if !f.parse(own, stderr) {
		return 2
	}
	f.want(*providers, "providers", "a TSV of provider<TAB>model<TAB>harness[<TAB>host] rows, chosen by label hash")
	f.want(*label, "label", "the label whose hash selects the provider row")
	f.want(*cardPath, "card", "the card file this launch runs")
	for _, a := range nativeArgs {
		switch strings.SplitN(strings.TrimLeft(a, "-"), "=", 2)[0] {
		case "harness", "model", "label", "card":
			if strings.HasPrefix(a, "-") {
				f.add(fmt.Sprintf("%s after -- is set by launch from the selected row; remove it", oneline.Field(a)))
			}
		}
	}
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
	card, err := os.ReadFile(*cardPath)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm launch: --card wants a readable file: %s\n", oneline.Err(err))
		return 2
	}
	// THE HASH SELECTION: the label's FNV-1a hash mod the row count picks the
	// provider. The same label always selects the same row, so a fill loop
	// that names the same label twice does not drift across providers.
	idx := launchHash(*label, len(rows))
	row := rows[idx]
	where := "local"
	if row.host != "" {
		where = "ssh:" + row.host
	}
	fields := fmt.Sprintf("label=%s provider=%s model=%s harness=%s where=%s registry=%s rows=%d selected=%d",
		oneline.Field(*label), oneline.Field(row.provider), oneline.Field(row.model),
		oneline.Field(row.harness), oneline.Field(where), oneline.Field(*providers), len(rows), idx)
	if *dryRun {
		fmt.Fprintf(stdout, "LAUNCH DRY %s ran=no\n", fields)
		return 0
	}

	rowArgs := []string{"--harness", row.harness, "--model", row.model, "--label", *label}
	var rc int
	if row.host == "" {
		rc = cmdNative(append(append(rowArgs, "--card", *cardPath), nativeArgs...), stdout, stderr)
	} else {
		// The remote side reads the card from its standard input; the card text
		// is never an argument, so it never shows in a process table.
		remote := append([]string{"nova-swarm", "native"}, rowArgs...)
		remote = append(append(remote, "--card", "/dev/stdin"), nativeArgs...)
		quoted := make([]string, len(remote))
		for i, a := range remote {
			quoted[i] = shellQuote(a)
		}
		cmd := exec.Command(*sshBin, "-o", "BatchMode=yes", row.host, strings.Join(quoted, " "))
		cmd.Stdin = strings.NewReader(string(card))
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		rc = 0
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				rc = ee.ExitCode()
			} else {
				fmt.Fprintf(stderr, "nova-swarm launch: ssh to %s did not start: %s\n", oneline.Field(row.host), oneline.Err(err))
				rc = 1
			}
		}
	}
	if rc != 0 {
		fmt.Fprintf(stdout, "LAUNCH FAILED %s ran=yes rc=%d\n", fields, rc)
		return rc
	}
	fmt.Fprintf(stdout, "LAUNCH OK %s ran=yes rc=0\n", fields)
	return 0
}

// splitLaunchArgs splits launch's own flags from the native flags after `--`.
func splitLaunchArgs(args []string) (own, native []string) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

// shellQuote quotes one word for the remote POSIX shell ssh hands the command to.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// loadLaunchRegistry reads a TSV of provider<TAB>model<TAB>harness[<TAB>host]
// rows. Empty lines and lines starting with # are skipped. Any other line that
// is not three or four nonempty fields is refused by line number: a malformed
// row never selects, so it can never produce LAUNCH OK.
func loadLaunchRegistry(path string) ([]launchRow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--providers wants a readable TSV file: %w", err)
	}
	var rows []launchRow
	for n, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 && len(fields) != 4 {
			return nil, fmt.Errorf("%s line %d has %d fields; it wants provider<TAB>model<TAB>harness[<TAB>host]", path, n+1, len(fields))
		}
		for i, v := range fields {
			if strings.TrimSpace(v) == "" {
				return nil, fmt.Errorf("%s line %d field %d is empty; every field of provider<TAB>model<TAB>harness[<TAB>host] is required", path, n+1, i+1)
			}
			fields[i] = strings.TrimSpace(v)
		}
		r := launchRow{provider: fields[0], model: fields[1], harness: fields[2]}
		if len(fields) == 4 {
			r.host = fields[3]
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// launchHash selects a provider index from the label's FNV-1a hash.
func launchHash(label string, n int) int {
	h := fnv.New32a()
	h.Write([]byte(label))
	return int(h.Sum32() % uint32(n))
}
