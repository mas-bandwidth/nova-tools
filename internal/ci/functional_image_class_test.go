package ci

import (
	"go/ast"
	"go/token"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// THE CLASS RULE: THE FUNCTIONAL-TIER IMAGE STAYS HONEST.
//
// The functional tests run in one container built from
// infra/functional-image/Containerfile. The image is only worth running in if
// it holds what the tests exec and is built from pinned inputs, so this file
// reads the Containerfile and its lists as text, in the unit tier, with no
// container:
//
//   - every base image is pinned by digest;
//   - the Go toolchain is the version go.mod pins;
//   - every download is checked against a sha256 and every apt install leaves
//     no lists behind and takes no recommended packages;
//   - the run is as a non-root user with the toolchain and the module proxy
//     switched off and the variables the fixtures read set;
//   - every external program a Go file in the tree execs by name (a string
//     literal, or a package-level constant holding one, given to exec.Command,
//     exec.CommandContext or exec.LookPath) is a row of binaries.txt, and every
//     row is still such a name: a new exec of a new program is red until the
//     list, and so the image or a stated reason, carries it;
//   - a row that says the image carries a program is checked against the
//     Containerfile, which must name what carries it.
//
// The scan reads names, not scripts: a program run inside a `sh -c` string, or
// through a variable, is not seen. binaries.txt's [unscanned] section lists the
// ones the tier is known to need that the scan cannot see; the run of the tier
// in the image is what catches the rest (a missing binary fails or skips loudly
// under NOVA_CI=1).

const (
	functionalImageDir    = "infra/functional-image"
	functionalImageFile   = functionalImageDir + "/Containerfile"
	functionalImageList   = functionalImageDir + "/binaries.txt"
	functionalImageReadme = functionalImageDir + "/README.md"
	containerRuntimeRole  = "fleet/roles/container-runtime"
)

var (
	sha256Re         = regexp.MustCompile(`^[0-9a-f]{64}$`)
	imageDigestRe    = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)
	snapshotStampRe  = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z$`)
	containerArgRe   = regexp.MustCompile(`(?m)^ARG ([A-Z0-9_]+)=(\S*)\s*$`)
	containerFromRe  = regexp.MustCompile(`(?m)^FROM\s+(\S+)(?:\s+AS\s+(\S+))?\s*$`)
	goModGoLineRe    = regexp.MustCompile(`(?m)^go\s+(\S+)\s*$`)
	goModToolchainRe = regexp.MustCompile(`(?m)^toolchain\s+go(\S+)\s*$`)
	curlCallRe       = regexp.MustCompile(`\bcurl\s+(\S+)`)
	curlPipeRe       = regexp.MustCompile(`\bcurl\s[^;&|]*\|[^|]`)
	curlFailFlagRe   = regexp.MustCompile(`(?:^|\s)-[a-zA-Z]*f[a-zA-Z]*(?:\s|$)`)
	curlOutputRe     = regexp.MustCompile(`(?:^|\s)(?:-[a-zA-Z]*o|--output)(?:\s|$)`)
	wgetRe           = regexp.MustCompile(`\bwget\b`)
	shaCommandRe     = regexp.MustCompile(`\bsha256sum\b`)
	shaStdinCheckRe  = regexp.MustCompile(`\bsha256sum\s+-c\s+-(?:\s|$|;|&|\)|\})`)
	curlViaVarRe     = regexp.MustCompile(`(=\s*["']?(?:/usr/bin/)?curl\b|\balias\s+curl\b)`)
	softFailRe       = regexp.MustCompile(`\|\|\s*(?:true|:|exit\s+0)\b`)
	otherFetcherRe   = regexp.MustCompile(`\b(?:git\s+(?:clone|fetch|pull|submodule)|go\s+(?:install|get|mod\s+download)|pip3?\s+install|npm\s+(?:i|install|ci)|gem\s+install|cargo\s+install|nc|ncat|scp|rsync|ssh)\b`)
	shaArgVarRe      = regexp.MustCompile(`\$\{?[A-Z0-9_]*SHA256[A-Z0-9_]*`)
)

// containerRuns returns the instruction bodies of the Containerfile with line
// continuations joined and comment lines dropped, one string per instruction.
func containerInstructions(src string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(src, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") {
			continue
		}
		if trim == "" && cur.Len() == 0 {
			continue
		}
		if strings.HasSuffix(line, "\\") {
			cur.WriteString(strings.TrimSuffix(line, "\\"))
			cur.WriteString(" ")
			continue
		}
		cur.WriteString(line)
		out = append(out, strings.Join(strings.Fields(cur.String()), " "))
		cur.Reset()
	}
	if cur.Len() > 0 {
		out = append(out, strings.Join(strings.Fields(cur.String()), " "))
	}
	return out
}

func containerArgs(src string) map[string]string {
	args := map[string]string{}
	for _, m := range containerArgRe.FindAllStringSubmatch(src, -1) {
		args[m[1]] = m[2]
	}
	return args
}

// downloadProblems reads the instructions for a download that is not checked
// against a pinned sha256. A download is a `curl` call (never `wget`, and never
// an ADD of a URL) that writes a file with -o, fails on an HTTP error with -f
// and pipes into nothing, in a RUN whose every `sha256sum` is `sha256sum -c -`:
// the expected sum comes in on stdin from an ARG's value, and is never computed
// from the download it checks. Every ARG *SHA256* is used by some RUN.
func downloadProblems(ins []string) []string {
	var out []string
	used := strings.Builder{}
	stage := 0
	stages := map[string]bool{}
	for _, in := range ins {
		if strings.HasPrefix(in, "FROM ") {
			stage++
			if f := strings.Fields(in); len(f) == 4 && strings.EqualFold(f[2], "AS") {
				stages[f[3]] = true
			}
			continue
		}
		low := strings.ToLower(in)
		for _, w := range []string{"trusted: yes", "trusted=yes", "allow-unauthenticated", "allowunauthenticated", "allowinsecure", "allow-insecure", "--force-yes", "verify-host=false", "check-valid-until=false", "check-date=false"} {
			if strings.Contains(low, w) {
				out = append(out, "an instruction switches off a package check ("+w+"): "+shorten(in))
			}
		}
		if strings.Contains(low, "verify-peer=false") && stage != 1 {
			out = append(out, "TLS peer verification is off outside the first stage's bootstrap install: "+shorten(in))
		}
		if strings.HasPrefix(in, "RUN ") && strings.Contains(in, "sources.list.d") && strings.Contains(in, "Types: deb") && !strings.Contains(in, "Signed-By:") {
			out = append(out, "an apt source is written with no Signed-By key: "+shorten(in))
		}
		if strings.HasPrefix(in, "COPY ") {
			for _, f := range strings.Fields(in) {
				if v, ok := strings.CutPrefix(f, "--from="); ok && !stages[v] {
					out = append(out, "a COPY takes files from "+v+", which is not a stage of this file (an outside image by tag moves and is not checked): "+shorten(in))
				}
			}
		}
		short := in
		if len(short) > 110 {
			short = short[:110]
		}
		if strings.HasPrefix(in, "ADD ") && (strings.Contains(in, "http://") || strings.Contains(in, "https://")) {
			out = append(out, "an ADD fetches a URL with no checksum; download with curl and check a pinned sha256: "+short)
		}
		if !strings.HasPrefix(in, "RUN ") {
			continue
		}
		used.WriteString(in)
		used.WriteString("\n")
		if wgetRe.MatchString(in) {
			out = append(out, "a RUN downloads with wget; use curl -fsSL -o <file> and check a pinned sha256: "+short)
		}
		if otherFetcherRe.MatchString(in) {
			out = append(out, "a RUN fetches with a tool that has no pinned checksum (git, go get, pip, npm and the like); download a release with curl and check a pinned sha256: "+short)
		}
		if curlViaVarRe.MatchString(in) {
			out = append(out, "curl is reached through a variable or an alias, so its flags are not read: "+short)
		}
		downloads := false
		for _, m := range curlCallRe.FindAllStringSubmatch(in, -1) {
			arg := m[1]
			if !strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "http") {
				continue // the word "curl" in an apt-get install list
			}
			downloads = true
			seg := in[strings.Index(in, m[0])+len("curl"):]
			if i := strings.IndexAny(seg, ";&|"); i >= 0 {
				seg = seg[:i]
			}
			if !curlFailFlagRe.MatchString(seg) {
				out = append(out, "a curl call has no -f, so an HTTP error page is taken for the file: "+short)
			}
			if !curlOutputRe.MatchString(seg) {
				out = append(out, "a curl call has no -o <file>: the download must be a file the checksum reads: "+short)
			}
		}
		if curlPipeRe.MatchString(in) {
			out = append(out, "a curl call pipes its output into another program: "+short)
		}
		if !downloads {
			continue
		}
		if n := len(shaCommandRe.FindAllString(in, -1)); n == 0 || n != len(shaStdinCheckRe.FindAllString(in, -1)) {
			out = append(out, "a RUN downloads and its sha256sum is not always `sha256sum -c -` fed a pinned sum on stdin (a sum computed from the download checks nothing): "+short)
		}
		if !shaArgVarRe.MatchString(in) {
			out = append(out, "a RUN downloads and never reads a pinned ARG *_SHA256 value: "+short)
		}
		if softFailRe.MatchString(in) {
			out = append(out, "a RUN that downloads goes on after a failure (|| true, || :, || exit 0): "+short)
		}
		if strings.Contains(strings.ReplaceAll(strings.ReplaceAll(in, ";;", ""), "; }", ""), ";") {
			out = append(out, "a RUN that downloads uses ';' between commands, so it goes on after a failed checksum; chain with &&: "+short)
		}
	}
	for name := range containerArgsFromInstructions(ins) {
		if !strings.Contains(name, "SHA256") {
			continue
		}
		if !strings.Contains(used.String(), "${"+name+"}") && !strings.Contains(used.String(), "$"+name) {
			out = append(out, "ARG "+name+" is pinned and no RUN reads it")
		}
	}
	sort.Strings(out)
	return out
}

func shorten(in string) string {
	if len(in) > 110 {
		return in[:110]
	}
	return in
}

func containerArgsFromInstructions(ins []string) map[string]string {
	args := map[string]string{}
	for _, in := range ins {
		if m := containerArgRe.FindStringSubmatch(in); m != nil {
			args[m[1]] = m[2]
		}
	}
	return args
}

// TestFunctionalImageDownloadCheckSeesEveryWayAroundIt: the check above holds
// on a checked download and reddens, with the reason, on each way a download
// can go unchecked.
func TestFunctionalImageDownloadCheckSeesEveryWayAroundIt(t *testing.T) {
	t.Parallel()
	arg := "ARG X_SHA256=" + strings.Repeat("0", 64)
	ok := `RUN curl -fsSL -o /tmp/x https://example.invalid/x && printf '%s  %s\n' "${X_SHA256}" /tmp/x | sha256sum -c -`
	if got := downloadProblems([]string{arg, ok}); len(got) != 0 {
		t.Fatalf("a checked download is refused: %v", got)
	}
	cases := []struct{ name, run, want string }{
		{"a sum computed from the download", `RUN curl -fsSL -o /tmp/x https://example.invalid/x && sha256sum /tmp/x > /tmp/x.sum && sha256sum -c /tmp/x.sum && echo "${X_SHA256}"`, "sha256sum -c -"},
		{"curl piped into sh", `RUN curl -fsSL https://example.invalid/i.sh | sh`, "pipes"},
		{"curl piped into sh, no flags", `RUN curl https://example.invalid/i.sh | sh && echo "${X_SHA256}"`, "pipes"},
		{"wget", `RUN wget -O /tmp/x https://example.invalid/x && echo "${X_SHA256}"`, "wget"},
		{"ADD of a URL", `ADD https://example.invalid/x /usr/local/bin/x`, "ADD"},
		{"no -f", `RUN curl -sSL -o /tmp/x https://example.invalid/x && printf '%s  %s\n' "${X_SHA256}" /tmp/x | sha256sum -c -`, "no -f"},
		{"no -o", `RUN curl -fsSL https://example.invalid/x && printf '%s  %s\n' "${X_SHA256}" /tmp/x | sha256sum -c -`, "no -o"},
		{"no checksum", `RUN curl -fsSL -o /tmp/x https://example.invalid/x && echo "${X_SHA256}"`, "sha256sum -c -"},
		{"a failed checksum ignored", `RUN curl -fsSL -o /tmp/x https://example.invalid/x && printf '%s  %s\n' "${X_SHA256}" /tmp/x | sha256sum -c - || true`, "goes on after a failure"},
		{"; after the download", `RUN curl -fsSL -o /tmp/x https://example.invalid/x && printf '%s  %s\n' "${X_SHA256}" /tmp/x | sha256sum -c - ; tar -xzf /tmp/x`, "';'"},
		{"git clone", `RUN git clone https://example.invalid/r.git /r && echo "${X_SHA256}"`, "no pinned checksum"},
		{"go get", `RUN go install example.invalid/x@latest && echo "${X_SHA256}"`, "no pinned checksum"},
		{"an outside image by tag", `COPY --from=docker.io/library/busybox:latest /bin/busybox /bin/busybox`, "not a stage"},
		{"a trusted apt source", `RUN printf '%s\n' 'Types: deb' 'Trusted: yes' 'Signed-By: /k.gpg' > /etc/apt/sources.list.d/x.sources`, "trusted: yes"},
		{"an apt source with no key", `RUN printf '%s\n' 'Types: deb' > /etc/apt/sources.list.d/x.sources`, "Signed-By"},
		{"peer checks off after the bootstrap", `RUN apt-get -o Acquire::https::Verify-Peer=false update`, "TLS peer verification is off"},
		{"curl through a variable", `RUN c=curl && $c -sSL -o /tmp/x https://example.invalid/x && printf '%s  %s\n' "${X_SHA256}" /tmp/x | sha256sum -c -`, "through a variable"},
		{"a sum that is not a pinned ARG", `RUN curl -fsSL -o /tmp/x https://example.invalid/x && printf '%s  %s\n' "abc" /tmp/x | sha256sum -c -`, "pinned ARG"},
	}
	for _, c := range cases {
		got := strings.Join(downloadProblems([]string{arg, c.run}), "\n")
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: the download check does not say %q: %q", c.name, c.want, got)
		}
	}
	if got := downloadProblems([]string{arg, "ARG UNUSED_SHA256=" + strings.Repeat("0", 64), ok}); len(got) != 1 || !strings.Contains(got[0], "UNUSED_SHA256") {
		t.Errorf("a pinned ARG no RUN reads is not named: %v", got)
	}
}

// TestFunctionalImageBaseIsPinnedByDigest: the base image is named by its
// index digest, and every FROM is that base or an earlier stage of the file.
func TestFunctionalImageBaseIsPinnedByDigest(t *testing.T) {
	t.Parallel()
	src := readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(functionalImageFile)))
	args := containerArgs(src)
	base := args["BASE"]
	if !imageDigestRe.MatchString(base) {
		t.Fatalf("%s: ARG BASE=%q is not an image pinned by @sha256:<digest>; a tag moves, a digest does not", functionalImageFile, base)
	}
	stages := map[string]bool{}
	froms := containerFromRe.FindAllStringSubmatch(src, -1)
	if len(froms) == 0 {
		t.Fatalf("%s has no FROM", functionalImageFile)
	}
	for _, m := range froms {
		ref := m[1]
		switch {
		case ref == "${BASE}":
		case stages[ref]:
		default:
			t.Errorf("%s: FROM %s is neither ${BASE} (pinned by digest) nor an earlier stage", functionalImageFile, ref)
		}
		if m[2] != "" {
			stages[m[2]] = true
		}
	}
}

// TestFunctionalImageGoIsTheModulesPin: the toolchain in the image is the one
// go.mod asks for, so a test run in the image is built as it is built anywhere
// else, and GOTOOLCHAIN=local never has to fetch another.
func TestFunctionalImageGoIsTheModulesPin(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	mod := readFile(t, filepath.Join(root, "go.mod"))
	want := ""
	if m := goModToolchainRe.FindStringSubmatch(mod); m != nil {
		want = m[1]
	} else if m := goModGoLineRe.FindStringSubmatch(mod); m != nil {
		want = m[1]
	}
	if strings.Count(want, ".") != 2 {
		t.Fatalf("go.mod pins Go %q; the image needs an exact three-part version to install", want)
	}
	src := readFile(t, filepath.Join(root, filepath.FromSlash(functionalImageFile)))
	args := containerArgs(src)
	if got := args["GO_VERSION"]; got != want {
		t.Errorf("%s: GO_VERSION=%s but go.mod pins %s; change the version and both GO_SHA256 lines together", functionalImageFile, got, want)
	}
}

// TestFunctionalImageInputsArePinned: every input has a fixed value and every
// download a checksum, so two builds hold the same bytes.
func TestFunctionalImageInputsArePinned(t *testing.T) {
	t.Parallel()
	src := readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(functionalImageFile)))
	args := containerArgs(src)
	if !snapshotStampRe.MatchString(args["APT_SNAPSHOT"]) {
		t.Errorf("%s: ARG APT_SNAPSHOT=%q is not a snapshot instant like 20260101T000000Z; an unpinned archive moves the package versions", functionalImageFile, args["APT_SNAPSHOT"])
	}
	sums := 0
	for name, val := range args {
		if !strings.Contains(name, "SHA256") {
			continue
		}
		sums++
		if !sha256Re.MatchString(val) {
			t.Errorf("%s: ARG %s=%q is not a lowercase hex sha256", functionalImageFile, name, val)
		}
	}
	if sums == 0 {
		t.Errorf("%s carries no ARG *_SHA256 at all", functionalImageFile)
	}
	if args["REDIS_VERSION"] == "" || args["SOPS_VERSION"] == "" || args["AGE_VERSION"] == "" {
		t.Errorf("%s: REDIS_VERSION, SOPS_VERSION and AGE_VERSION are all pinned ARGs; one is missing", functionalImageFile)
	}
	ins := containerInstructions(src)
	for _, problem := range downloadProblems(ins) {
		t.Errorf("%s: %s", functionalImageFile, problem)
	}
	for _, in := range ins {
		if !strings.HasPrefix(in, "RUN ") {
			continue
		}
		if strings.Contains(in, "apt-get install") {
			if !strings.Contains(in, "--no-install-recommends") {
				t.Errorf("%s: an apt-get install without --no-install-recommends: %.120s", functionalImageFile, in)
			}
			if !strings.Contains(in, "rm -rf /var/lib/apt/lists") {
				t.Errorf("%s: an apt-get install that leaves /var/lib/apt/lists in the layer: %.120s", functionalImageFile, in)
			}
		}
	}
}

// isRootUser reports whether a USER value is empty or is root by name or by any
// spelling of uid 0 (`0`, `00`, `root:root`, `0:0`).
func isRootUser(user string) bool {
	name, _, _ := strings.Cut(strings.TrimSpace(user), ":")
	if name == "" || name == "root" {
		return true
	}
	if n, err := strconv.Atoi(name); err == nil && n == 0 {
		return true
	}
	return false
}

func TestFunctionalImageRootUserSpellings(t *testing.T) {
	t.Parallel()
	for _, u := range []string{"", "root", "0", "00", "000", "root:root", "0:0", "00:0", " root "} {
		if !isRootUser(u) {
			t.Errorf("USER %q is root and the test does not see it", u)
		}
	}
	for _, u := range []string{"bench", "10001", "bench:bench", "10001:10001"} {
		if isRootUser(u) {
			t.Errorf("USER %q is not root", u)
		}
	}
}

// TestFunctionalImageRunsAsTheTierExpects: a non-root user, no toolchain
// fetch, no module proxy, and the variables the fixtures read.
func TestFunctionalImageRunsAsTheTierExpects(t *testing.T) {
	t.Parallel()
	src := readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(functionalImageFile)))
	ins := containerInstructions(src)
	user := ""
	env := strings.Builder{}
	for _, in := range ins {
		switch {
		case strings.HasPrefix(in, "USER "):
			user = strings.TrimSpace(strings.TrimPrefix(in, "USER "))
		case strings.HasPrefix(in, "ENV "):
			env.WriteString(in)
			env.WriteString(" ")
		}
	}
	if isRootUser(user) {
		t.Errorf("%s ends as user %q; postgres refuses root and the tier runs as a non-root user", functionalImageFile, user)
	}
	for _, want := range []string{"GOTOOLCHAIN=local", "GOPROXY=off", "NOVA_CI=1", "NOVA_FUNCTIONAL_RUN=container"} {
		if !strings.Contains(env.String(), want) {
			t.Errorf("%s: ENV lacks %s", functionalImageFile, want)
		}
	}
	if !strings.Contains(src, "/image-manifest.txt") {
		t.Errorf("%s writes no /image-manifest.txt; the manifest is how two builds are compared", functionalImageFile)
	}
}

// execNames is every program name a Go file under cmd, internal or tools gives
// to exec.Command, exec.CommandContext, exec.LookPath or internal/subproc's Command,
// CommandFor, Context and Long as a string literal or
// as a package-level constant or variable initialised with one. The value is
// the base name, and the sites that give it, as file:line.
func execNames(t *testing.T) map[string][]string {
	t.Helper()
	tree := repoTree(t)
	strs := map[string]string{} // dir|name -> literal
	files := tree.GoFilesUnder(false, "cmd", "internal", "tools")
	files = append(files, tree.GoFilesUnder(true, "cmd", "internal", "tools")...)
	var use []*treeFile
	for _, f := range files {
		if f.AST == nil || f.HasDirNamed("testdata") {
			continue
		}
		use = append(use, f)
		for _, decl := range f.AST.Decls {
			g, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range g.Specs {
				v, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, n := range v.Names {
					if i >= len(v.Values) {
						continue
					}
					if lit, ok := v.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if s, err := strconv.Unquote(lit.Value); err == nil {
							strs[path.Dir(f.Rel)+"|"+n.Name] = s
						}
					}
				}
			}
		}
	}
	out := map[string][]string{}
	for _, f := range use {
		ast.Inspect(f.AST, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || (pkg.Name != "exec" && pkg.Name != "subproc") {
				return true
			}
			arg := -1
			switch {
			case pkg.Name == "exec" && (sel.Sel.Name == "Command" || sel.Sel.Name == "LookPath"):
				arg = 0
			case pkg.Name == "exec" && sel.Sel.Name == "CommandContext":
				arg = 1
			// internal/subproc is the door every child now goes through: Command and
			// CommandFor take (ctx, kind-or-budget, name, ...), Context and Long take
			// (ctx, name, ...).
			case pkg.Name == "subproc" && (sel.Sel.Name == "Command" || sel.Sel.Name == "CommandFor"):
				arg = 2
			case pkg.Name == "subproc" && (sel.Sel.Name == "Context" || sel.Sel.Name == "Long"):
				arg = 1
			}
			if arg < 0 || arg >= len(call.Args) {
				return true
			}
			name := ""
			switch a := call.Args[arg].(type) {
			case *ast.BasicLit:
				if a.Kind == token.STRING {
					name, _ = strconv.Unquote(a.Value)
				}
			case *ast.Ident:
				name = strs[path.Dir(f.Rel)+"|"+a.Name]
			}
			if name == "" {
				return true
			}
			name = path.Base(filepath.ToSlash(name))
			pos := tree.FSet.Position(call.Pos())
			out[name] = append(out[name], f.Rel+":"+strconv.Itoa(pos.Line))
			return true
		})
	}
	return out
}

type imageRow struct {
	name, how, provider, note string
	unscanned                 bool
	line                      int
}

// imageRows parses binaries.txt: `<name> <TAB> <how> <TAB> <provider or reason>`
// with `how` one of apt, source, base, tree or absent, and a `[unscanned]`
// section for names the scan cannot see.
func imageRows(t *testing.T, text string) []imageRow {
	t.Helper()
	var rows []imageRow
	unscanned := false
	for i, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if trim == "[unscanned]" {
			unscanned = true
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 3 || f[0] == "" || f[2] == "" {
			t.Errorf("%s:%d: %q is not `<name>\\t<how>\\t<provider or reason>`", functionalImageList, i+1, line)
			continue
		}
		switch f[1] {
		case "apt", "source", "base", "tree", "absent":
		default:
			t.Errorf("%s:%d: how %q is not apt, source, base, tree or absent", functionalImageList, i+1, f[1])
			continue
		}
		rows = append(rows, imageRow{name: f[0], how: f[1], provider: f[2], unscanned: unscanned, line: i + 1})
	}
	return rows
}

// TestFunctionalImageCarriesEveryBinaryTheTierExecs is the class test the
// image is kept honest by.
func TestFunctionalImageCarriesEveryBinaryTheTierExecs(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	containerfile := readFile(t, filepath.Join(root, filepath.FromSlash(functionalImageFile)))
	rows := imageRows(t, readFile(t, filepath.Join(root, filepath.FromSlash(functionalImageList))))
	found := execNames(t)

	listed := map[string]imageRow{}
	for _, r := range rows {
		if prev, dup := listed[r.name]; dup {
			t.Errorf("%s:%d: %s is listed twice (first at line %d)", functionalImageList, r.line, r.name, prev.line)
		}
		listed[r.name] = r
	}

	var names []string
	for n := range found {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		r, ok := listed[n]
		if !ok || r.unscanned {
			sites := found[n]
			if len(sites) > 3 {
				sites = sites[:3]
			}
			t.Errorf("%s is run by name (%s) and is not a row of %s: add `%s<TAB>apt|source|base|tree<TAB><what carries it>` and install it in %s, or `%s<TAB>absent<TAB><why the functional tier can do without it>`",
				n, strings.Join(sites, ", "), functionalImageList, n, functionalImageFile, n)
		}
	}
	for _, r := range rows {
		if !r.unscanned && found[r.name] == nil {
			t.Errorf("%s:%d: %s is no longer run by name anywhere under cmd, internal or tools; delete the row (the list follows the tree)", functionalImageList, r.line, r.name)
		}
		if r.unscanned && found[r.name] != nil {
			t.Errorf("%s:%d: %s is [unscanned] but the scan sees it now; move it above the section", functionalImageList, r.line, r.name)
		}
		switch r.how {
		case "apt":
			if !aptInstalls(containerfile, r.provider) {
				t.Errorf("%s:%d: %s says apt package %q carries it, and no apt-get install in %s names that package", functionalImageList, r.line, r.name, r.provider, functionalImageFile)
			}
		case "source":
			if problem := sourceProblem(containerfile, r.name, r.provider); problem != "" {
				t.Errorf("%s:%d: %s says %q carries it, and %s: %s", functionalImageList, r.line, r.name, r.provider, functionalImageFile, problem)
			}
		case "absent":
			if len(strings.Fields(r.provider)) < 3 {
				t.Errorf("%s:%d: %s is absent from the image and the reason %q is not a sentence", functionalImageList, r.line, r.name, r.provider)
			}
		}
	}
}

// containerStages splits the instructions by FROM: the stage names, and the
// instructions of every stage but the last (build) and of the last (final).
func containerStages(containerfile string) (names map[string]bool, build, final []string) {
	names = map[string]bool{}
	var cur []string
	stages := 0
	for _, in := range containerInstructions(containerfile) {
		if strings.HasPrefix(in, "FROM ") {
			if stages > 0 {
				build = append(build, cur...)
			}
			cur = nil
			stages++
			if f := strings.Fields(in); len(f) == 4 && strings.EqualFold(f[2], "AS") {
				names[f[3]] = true
			}
			continue
		}
		cur = append(cur, in)
	}
	return names, build, cur
}

// hasWord reports whether text holds name as a whole path element or word
// (`redis-cli` is not `redis-cli-x`, `age` is not `age.tgz`).
func hasWord(text, name string) bool {
	return regexp.MustCompile(`(^|[^A-Za-z0-9_.-])` + regexp.QuoteMeta(name) + `($|[^A-Za-z0-9_.-])`).MatchString(text)
}

// sourceProblem reports why the Containerfile does not build and carry a
// program a binaries.txt row calls `source`, or "". Comments never count. The
// final stage must copy or install the program by name (the manifest's own
// version lines do not count), and the provider is either a stage the final
// stage copies that program from, or a word in a RUN of an earlier stage that
// builds or downloads it.
func sourceProblem(containerfile, name, provider string) string {
	stages, build, final := containerStages(containerfile)
	var installs []string
	for _, in := range final {
		if (strings.HasPrefix(in, "COPY ") || strings.HasPrefix(in, "RUN ")) && !strings.Contains(in, "/image-manifest.txt") {
			installs = append(installs, in)
		}
	}
	named := false
	for _, in := range installs {
		if hasWord(in, name) {
			named = true
		}
	}
	if !named {
		return "the final stage copies or installs no `" + name + "`"
	}
	if stages[provider] {
		for _, in := range installs {
			if strings.HasPrefix(in, "COPY --from="+provider+" ") && hasWord(in, name) {
				return ""
			}
		}
		return "the final stage has no `COPY --from=" + provider + "` of `" + name + "`"
	}
	for _, in := range build {
		if strings.HasPrefix(in, "RUN ") && hasWord(in, provider) {
			return ""
		}
	}
	return "no RUN of an earlier stage builds or downloads `" + provider + "`"
}

// TestFunctionalImageSourceCheckSeesADeletedInstall: the source check reads
// instructions, so a comment that names a program does not carry it, and a
// program whose download or COPY is deleted is red.
func TestFunctionalImageSourceCheckSeesADeletedInstall(t *testing.T) {
	t.Parallel()
	containerfile := readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(functionalImageFile)))
	drop := func(substr string) string {
		var keep []string
		for _, l := range strings.Split(containerfile, "\n") {
			if !strings.Contains(l, substr) {
				keep = append(keep, l)
			}
		}
		return strings.Join(keep, "\n")
	}
	for _, c := range []struct{ name, provider, cut string }{
		{"redis-server", "redis-build", "COPY --from=redis-build"},
		{"redis-cli", "redis-build", "COPY --from=redis-build"},
		{"sops", "sops", "sops"},
		{"age-keygen", "age", "age"},
		{"go", "fetch", "COPY --from=fetch /usr/local/go"},
	} {
		if p := sourceProblem(containerfile, c.name, c.provider); p != "" {
			t.Errorf("%s: the real Containerfile is refused: %s", c.name, p)
		}
		mutated := drop(c.cut)
		if mutated == containerfile {
			t.Fatalf("%s: nothing to delete for %q", c.name, c.cut)
		}
		if p := sourceProblem(mutated, c.name, c.provider); p == "" {
			t.Errorf("%s: deleting every line with %q stays green", c.name, c.cut)
		}
	}
	// A comment alone does not carry a program.
	if p := sourceProblem("FROM x\n# sops is installed elsewhere\n", "sops", "sops"); p == "" {
		t.Errorf("a comment that names sops carries it")
	}
}

// aptInstalls reports whether some apt-get install in the Containerfile lists
// pkg as an argument.
func aptInstalls(containerfile, pkg string) bool {
	for _, in := range containerInstructions(containerfile) {
		i := strings.Index(in, "apt-get install")
		if i < 0 {
			continue
		}
		for _, w := range strings.Fields(in[i:]) {
			if w == pkg {
				return true
			}
		}
	}
	return false
}

// TestFunctionalImageReadmeNamesEveryWritablePlace: --read-only leaves a
// writable /var/tmp and /dev/shm (and a /run the test user cannot write), so
// the README names them beside the mounts the run command makes, and never
// says only the tmpfs mounts and the cache are writable.
func TestFunctionalImageReadmeNamesEveryWritablePlace(t *testing.T) {
	t.Parallel()
	readme := readFile(t, filepath.Join(repoRoot(t), filepath.FromSlash(functionalImageReadme)))
	i := strings.Index(readme, "### Where a run can write")
	if i < 0 {
		t.Fatalf("%s has no \"Where a run can write\" section", functionalImageReadme)
	}
	section := readme[i:]
	if j := strings.Index(section[3:], "\n### "); j >= 0 {
		section = section[:j+3]
	}
	for _, want := range []string{"`/tmp`", "`/home/bench`", "`/gocache`", "`/var/tmp`", "`/dev/shm`", "`/run`", "Postgres"} {
		if !strings.Contains(section, want) {
			t.Errorf("%s: the writable places do not name %s", functionalImageReadme, want)
		}
	}
	if strings.Contains(readme, "only the tmpfs mounts and the named cache volume are") {
		t.Errorf("%s still says only the tmpfs mounts and the cache volume are writable", functionalImageReadme)
	}
}

// TestFunctionalImageRuntimeAndReadmeAgree: the runtime role probes with the
// image's base and with the flags the README's run command uses, so what the
// probe proves is what a run relies on.
func TestFunctionalImageRuntimeAndReadmeAgree(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	containerfile := readFile(t, filepath.Join(root, filepath.FromSlash(functionalImageFile)))
	base := containerArgs(containerfile)["BASE"]
	defaults := readFile(t, filepath.Join(root, filepath.FromSlash(containerRuntimeRole+"/defaults/main.yml")))
	if !strings.Contains(defaults, `container_runtime_probe_image: "`+base+`"`) {
		t.Errorf("%s/defaults/main.yml: container_runtime_probe_image is not the Containerfile's BASE (%s)", containerRuntimeRole, base)
	}
	_, argv, _ := probeTask(t)
	have := map[string]bool{}
	for _, a := range argv {
		have[a] = true
	}
	readme := readFile(t, filepath.Join(root, filepath.FromSlash(functionalImageReadme)))
	cmd := readmeRunCommand(t)
	for _, flag := range []string{"--network", "--pids-limit", "--memory", "--memory-swap", "--cpus", "--read-only", "--tmpfs", "--timeout", "--ipc", "--security-opt", "--cap-drop"} {
		if !have[flag] {
			t.Errorf("%s/tasks/main.yml: the probe does not run with %s", containerRuntimeRole, flag)
		}
		if !strings.Contains(cmd+" ", flag+" ") {
			t.Errorf("%s: the run command does not carry %s, which the runtime probe proves", functionalImageReadme, flag)
		}
	}
	if !strings.Contains(readme, "cat /image-manifest.txt") {
		t.Errorf("%s: no one-line command prints /image-manifest.txt", functionalImageReadme)
	}
}
