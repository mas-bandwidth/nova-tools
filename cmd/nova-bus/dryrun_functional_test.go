//go:build functional

package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every verb that writes takes --dry-run, and a dry run prints the plan the real run would
// carry out, from the same path, and writes nothing: the checkout, its status and the
// remote are what they were (docs/STANDARD.md, "A verb that writes has a dry run").
func TestEveryDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		args  func(checkout, dir string) []string
		wants []string
	}{
		{"inbox --advance", func(c, _ string) []string {
			return []string{"inbox", "--bus", c, "--as", "Ada", "--receipt-max-words", "3", "--carry-history", "--advance", "--remote", "origin", "--branch", "main", "--dry-run"}
		}, []string{"INBOX CURSOR commit=", "pushed=false attempts=0 dry_run=true"}},
		{"receipt", func(c, _ string) []string {
			return []string{"receipt", "--bus", c, "--as", "Ada", "--note", "bo-abcdef012345", "--remote", "origin", "--branch", "main", "--dry-run"}
		}, []string{"RECEIPT RECORD note=bo-abcdef012345 lane=from-ada", "RECEIPT OK recorded=1 already=0 commit=- pushed=false attempts=0 dry_run=true"}},
		{"check --rebuild-index", func(c, _ string) []string {
			return []string{"check", "--bus", c, "--full", "--rebuild-index", "--dry-run"}
		}, []string{"BUS INDEX lane=from-bo notes=2 dry_run=true"}},
		{"draft --out", func(c, dir string) []string {
			return []string{"draft", "--bus", c, "--as", "Ada", "--to", "Bo", "--subject", "x", "--out", filepath.Join(dir, "d.md"), "--dry-run"}
		}, []string{"DRAFT OK path=", "dry_run=true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkout, bare := busDir(t)
			dir := t.TempDir()
			head, remote := gitIn(t, checkout, "rev-parse", "HEAD"), gitIn(t, bare, "rev-parse", "main")
			r := invoke(t, "", tc.args(checkout, dir)...).mustCode(t, 0)
			for _, want := range tc.wants {
				r.mustContain(t, "stdout", want)
			}
			assert.Equal(t, head, gitIn(t, checkout, "rev-parse", "HEAD"), "the checkout moved")
			assert.Equal(t, remote, gitIn(t, bare, "rev-parse", "main"), "the remote moved")
			assert.Empty(t, gitIn(t, checkout, "status", "--porcelain"), "the checkout has changes")
			assert.NoFileExists(t, filepath.Join(dir, "d.md"))
		})
	}
	// The reply form fetches to resolve, so it has no run that writes nothing.
	checkout, _ := busDir(t)
	r := invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--reply-to", "bo-abcdef012345", "--dry-run").mustCode(t, 2)
	require.Contains(t, r.stderr, "DRAFT REFUSED: --dry-run is for --out")
}
