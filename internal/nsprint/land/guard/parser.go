package guard

import (
	"context"
	"fmt"
	"go/scanner"
	"go/token"
	"sort"
	"strings"
)

// ParserAllow is every non-test Go file that may carry the wire form of a
// typed review line, the `who=` and `verdict=` keys as code (string
// literals, not comments): the readers and writers of that line measured on
// dev ac1dfd2e. The strict parser (internal/nsprint/disposition, #3092 rev 7)
// reads the keys by name and is not on this list because it never spells
// the wire form. A file added here is a decision; a file that starts
// handling typed lines without being here is #3497 meeting #3092 again (two
// parsers, two answers for one line). The list only shrinks: a listed file
// that no longer carries the keys is dropped at the next measure.
var ParserAllow = []string{
	"cmd/nova-merge/verbs.go",
	"cmd/nova-review/reads.go",
	"cmd/nova-sprint/result.go",
	"internal/merge/status.go",
	// The sprint fold (#2618) reads the disposition hash by field, never the
	// wire line; its own refinement lines print who= and verdict= keys.
	"internal/nsprint/sprint/fold.go",
	"internal/pulse/recut.go",
}

// typedLineKeys are the two keys of a DISPOSITION line whose `<key>=` form
// in a string literal marks a file as handling typed lines.
var typedLineKeys = []string{"who", "verdict"}

// parsesTypedLine reports whether a Go source handles typed review lines in
// code: one string literal carries the `who=` key and one the `verdict=`
// key of a DISPOSITION line. Comments do not count, so a file may describe
// the line without being a parser of it.
func parsesTypedLine(src string) bool {
	fset := token.NewFileSet()
	f := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(f, []byte(src), nil, 0)
	seen := map[string]bool{}
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.STRING {
			continue
		}
		for _, k := range typedLineKeys {
			if strings.Contains(lit, k+"=") {
				seen[k] = true
			}
		}
		if len(seen) == len(typedLineKeys) {
			return true
		}
	}
	return false
}

// oneParser walks every non-test .go file outside testdata and fails on the
// first typed-line parser that is not in ParserAllow.
func oneParser(_ context.Context, root string) Result {
	allow := map[string]bool{}
	for _, f := range ParserAllow {
		allow[f] = true
	}
	var parsers []string
	err := walkGo(root, func(file, src string) {
		if parsesTypedLine(src) {
			parsers = append(parsers, file)
		}
	})
	if err != nil {
		return Result{Err: err}
	}
	sort.Strings(parsers)
	for _, f := range parsers {
		if !allow[f] {
			return Result{OK: false, File: f, Why: "handles typed review lines (the who and verdict keys as code) and is not in the parser allow list; use internal/nsprint/disposition (the one parser) or add the file to guard.ParserAllow with the reason"}
		}
	}
	return Result{OK: true, Why: fmt.Sprintf("%d parser files, all allowed: %s", len(parsers), strings.Join(parsers, ","))}
}
