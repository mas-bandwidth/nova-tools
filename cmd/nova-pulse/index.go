package main

import (
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/ctxindex"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// cmdIndex builds the per-repo context index at the clone's HEAD (#2498 S2).
func cmdIndex(args []string, stdout, stderr io.Writer) int {
	f := newFlags("index")
	repo := f.fs.String("repo", "", "")
	out := f.fs.String("out", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*repo, "repo", "the git clone at the landed tip")
	f.want(*out, "out", "the index directory")
	if f.refused(stderr) {
		return 2
	}
	st, err := ctxindex.Build(*repo, *out)
	if err != nil {
		fmt.Fprintf(stderr, "INDEX REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	fmt.Fprintf(stdout, "INDEX OK head=%s specs=%d tests=%d symbols=%d guarded=%d out=%s\n",
		st.Head[:12], st.Specs, st.Tests, st.Symbols, st.Guarded, oneline.Field(*out))
	return 0
}
