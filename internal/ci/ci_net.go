package ci

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ci_net.go is the machine behind the `net` class test in docs/SPEC-CI.md. It
// reads every _test.go under internal/ and cmd/ -- the two trees the waits
// checker already walks -- and refuses a string literal that names a real
// network host: an http(s) URL whose host is not a local endpoint, or a bare
// host:port whose host is not one. Glenn's hard rule (2026-09-17) is that a
// unit test tests LOGIC, not the network: every endpoint is mocked locally with
// httptest or a local fake, and only the nightly, soak and fuzz suites may
// reach the real network, which is why a file carrying a `//go:build nightly`
// or `//go:build soak` constraint is skipped whole. It writes nothing. Its only
// input besides the tree is an allowlist of existing offenders, each with the
// file, the line, the kind, a date and a reason; a row allows one offender of
// its kind in its file and an entry that names none is refused, so the file
// only ever shrinks. Like the waits list it is matched by FILE and KIND, never
// by line.

// NetVerbLine is the help line the class test is entered under, word for word
// as docs/SPEC-CI.md prints it.
const NetVerbLine = "net     read every _test.go on the CI path; refuse a real network host or host:port"

const (
	// NetRemedy is the one thing to do about a real host in a unit test.
	NetRemedy = "mock the endpoint with httptest or a local fake"
	// NetRemedyAllow is the one thing to do about an allowlist entry that names
	// no offender: the file only shrinks.
	NetRemedyAllow = "fix the host; the allowlist only shrinks"
)

// NetFinding is one real host, with its file, line, host, kind and the one
// thing to do about it. Kind is "url" for an http(s) URL, "hostport" for a bare
// host:port, or "allowlist" for a stale entry.
type NetFinding struct {
	File   string
	Line   int
	Host   string
	Kind   string
	Remedy string
}

// Render is the one-line refusal for this finding.
func (f NetFinding) Render() string {
	return fmt.Sprintf("CI-NET file=%s line=%d host=%s remedy=%q", f.File, f.Line, f.Host, f.Remedy)
}

// NetResult is one run of the checker: the tests read, the allowlist entries
// still honored, the real hosts left, and the allowlist entries that name no
// offender.
type NetResult struct {
	Tests       int
	Allowlisted int
	Findings    []NetFinding
	Stale       []NetFinding
}

// Refused is the number of lines the run would print: offenders plus stale
// allowlist entries. The count is the truth about the CI path whether or not
// the lines printed.
func (r NetResult) Refused() int { return len(r.Findings) + len(r.Stale) }

// OKLine is the one line a clean run prints.
func (r NetResult) OKLine() string {
	return fmt.Sprintf("CI-NET OK tests=%d allowlisted=%d refused=0", r.Tests, r.Allowlisted)
}

// FailLine closes a refusal with the counts.
func (r NetResult) FailLine() string {
	return fmt.Sprintf("CI-NET FAIL tests=%d allowlisted=%d refused=%d", r.Tests, r.Allowlisted, r.Refused())
}

// ExitCode is the status the check would exit with: 2 when anything is refused,
// 0 when the path is clean.
func (r NetResult) ExitCode() int {
	if r.Refused() > 0 {
		return 2
	}
	return 0
}

// CheckNet reads every _test.go under root/internal and root/cmd and returns
// the real hosts, the allowlist entries honored, and any allowlist entry that
// names no offender. The tree comes from the caller, never from a walk of the
// repository; it reuses the waits checker's file walk (testdata directories are
// skipped so the fixtures are never read as offenders) and its allowlist
// reader, whose `file:line kind date reason` rows are general enough for both.
func CheckNet(root, allowlistPath string) (NetResult, error) {
	var res NetResult
	entries, err := readWaitAllowlist(allowlistPath)
	if err != nil {
		return res, err
	}
	matched := make([]bool, len(entries))

	err = walkCITestFiles(root, func(rel string, raw []byte) error {
		res.Tests++
		findings, ok := scanNetFile(rel, raw)
		if ok {
			res.Findings = append(res.Findings, findings...)
		}
		return nil
	})
	if err != nil {
		return res, err
	}

	var remaining []NetFinding
	for _, f := range res.Findings {
		if i := matchNetAllow(entries, matched, f); i >= 0 {
			matched[i] = true
			res.Allowlisted++
			continue
		}
		remaining = append(remaining, f)
	}
	res.Findings = remaining
	for i, e := range entries {
		if matched[i] {
			continue
		}
		res.Stale = append(res.Stale, NetFinding{File: e.file, Line: e.line, Kind: "allowlist", Remedy: NetRemedyAllow})
	}
	return res, nil
}

// matchNetAllow returns the index of an unused entry that allows this finding,
// or -1.
//
// A row allows ONE offender of its kind in its file. The line in the row is
// where the offender stood when the row was written, for a reader; it is not
// matched on. Matching on the line turned dev red the moment any merge shifted
// lines in a listed file (2026-09-17: #1073 moved cmd/nova-swarm/native_test.go
// and every group after it failed). The count per file and kind is what the
// list holds still: a new real host in a listed file exceeds its rows and is
// refused, a fixed one leaves a row unused and the stale rule makes the list
// shrink. An exact line match is preferred so the stale row reported is the one
// a reader expects.
func matchNetAllow(entries []waitAllow, used []bool, f NetFinding) int {
	loose := -1
	for i, e := range entries {
		if used[i] || e.file != f.File || e.kind != f.Kind {
			continue
		}
		if e.line == f.Line {
			return i
		}
		if loose < 0 {
			loose = i
		}
	}
	return loose
}

// scanNetFile parses one _test.go and returns its real-host findings. The
// second result is false when the file does not parse: a file that is not Go
// cannot carry the shapes this check reads. A file whose header carries a
// nightly or soak build constraint is skipped whole.
func scanNetFile(rel string, src []byte) ([]NetFinding, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, parser.ParseComments)
	if err != nil {
		return nil, false
	}
	if netBuildTagExempt(file) {
		return nil, true
	}
	var out []NetFinding
	seen := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		val, unqErr := strconv.Unquote(lit.Value)
		if unqErr != nil {
			return true
		}
		pos := fset.Position(lit.Pos())
		for _, hit := range netHostsIn(val) {
			key := strconv.Itoa(pos.Line) + ":" + hit.kind + ":" + hit.host
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, NetFinding{File: rel, Line: pos.Line, Host: hit.host, Kind: hit.kind, Remedy: NetRemedy})
		}
		return true
	})
	return out, true
}

// netBuildTagExempt reports whether the file's header build constraint carries
// a nightly or soak tag. A file that only builds in one of those suites is
// skipped whole: soak, fuzz and nightly are where Glenn's rule allows the real
// network. The constraint is read with go/build/constraint rather than a
// hand-rolled parser, so `//go:build nightly` and the legacy `// +build` form
// are both understood.
func netBuildTagExempt(file *ast.File) bool {
	for _, cg := range file.Comments {
		if cg.Pos() > file.Package {
			continue
		}
		for _, c := range cg.List {
			if !constraint.IsGoBuild(c.Text) && !constraint.IsPlusBuild(c.Text) {
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				continue
			}
			if constraintTag(expr, "nightly") || constraintTag(expr, "soak") {
				return true
			}
		}
	}
	return false
}

// constraintTag reports whether a build expression names tag. A negated tag
// does not count: `!soak` is the file that builds in the ordinary suite, which
// this check reads.
func constraintTag(expr constraint.Expr, tag string) bool {
	switch e := expr.(type) {
	case *constraint.TagExpr:
		return e.Tag == tag
	case *constraint.AndExpr:
		return constraintTag(e.X, tag) || constraintTag(e.Y, tag)
	case *constraint.OrExpr:
		return constraintTag(e.X, tag) || constraintTag(e.Y, tag)
	}
	return false
}

// netHit is one host named by a string literal, with the kind of literal it was.
type netHit struct {
	host string
	kind string
}

// netHostsIn returns the real hosts a single string literal names: every
// http(s) URL token in it and, when the whole literal is one, a bare host:port.
func netHostsIn(v string) []netHit {
	var out []netHit
	for _, scheme := range []string{"http://", "https://"} {
		rest := v
		for {
			i := strings.Index(rest, scheme)
			if i < 0 {
				break
			}
			rest = rest[i:]
			tok := urlToken(rest)
			rest = rest[len(tok):]
			if len(tok) <= len(scheme) {
				continue
			}
			u, err := url.Parse(tok)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
				continue
			}
			if h := u.Hostname(); h != "" && !allowedNetHost(h) {
				out = append(out, netHit{host: h, kind: "url"})
			}
		}
	}
	if h, ok := bareHostPort(v); ok && !allowedNetHost(h) {
		out = append(out, netHit{host: h, kind: "hostport"})
	}
	return out
}

// urlToken returns the prefix of s that is one URL: it stops at whitespace or a
// character that cannot be part of a URL in a Go test literal, so a URL embedded
// in prose, JSON or a shell template is still read whole.
func urlToken(s string) string {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\n', '\r', '"', '\'', '`', '<', '>', '\\':
			return s[:i]
		}
	}
	return s
}

// bareHostPort returns the host of a literal that is exactly host:port, so a
// time format, a duration or a sentence with a colon is never read as a host.
func bareHostPort(v string) (string, bool) {
	t := strings.TrimSpace(v)
	if strings.ContainsAny(t, " \t\n/\\") {
		return "", false
	}
	host, port, err := net.SplitHostPort(t)
	if err != nil || host == "" || port == "" {
		return "", false
	}
	for i := 0; i < len(port); i++ {
		if port[i] < '0' || port[i] > '9' {
			return "", false
		}
	}
	if !plausibleHost(host) {
		return "", false
	}
	return host, true
}

// plausibleHost reports whether a bare literal's host looks like a host name
// rather than the left side of a time, key:value or file:line string: a bare
// localhost, an IP, or a lowercase dotted name with a subdomain and an
// alphabetic TLD. The subdomain requirement keeps `SPEC-TOKENS.md:436` and
// `notes.md:1` from being read as a host and a port.
func plausibleHost(h string) bool {
	if h == "localhost" || net.ParseIP(h) != nil {
		return true
	}
	if strings.ToLower(h) != h {
		return false
	}
	labels := strings.Split(h, ".")
	if len(labels) < 3 {
		return false
	}
	for _, l := range labels {
		if l == "" {
			return false
		}
	}
	tld := labels[len(labels)-1]
	if len(tld) < 2 {
		return false
	}
	for i := 0; i < len(tld); i++ {
		if tld[i] < 'a' || tld[i] > 'z' {
			return false
		}
	}
	return true
}

// allowedNetHost reports whether a host is a local endpoint or an RFC 2606 /
// RFC 6761 reserved test domain: localhost, the loopback addresses, example.*
// and the *.invalid and *.test names. Those can never reach a real service, so
// a test may name them.
func allowedNetHost(h string) bool {
	h = strings.ToLower(strings.Trim(h, "[]"))
	switch h {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	if strings.HasPrefix(h, "example.") {
		return true
	}
	return strings.HasSuffix(h, ".invalid") || strings.HasSuffix(h, ".test")
}
