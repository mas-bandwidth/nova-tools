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
	curlRe           = regexp.MustCompile(`\bcurl -`)
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
	for _, in := range containerInstructions(src) {
		if !strings.HasPrefix(in, "RUN ") {
			continue
		}
		if curlRe.MatchString(in) && !strings.Contains(in, "sha256sum -c") {
			t.Errorf("%s: a RUN downloads with curl and never checks a sha256: %.120s", functionalImageFile, in)
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
	if user == "" || user == "root" || user == "0" {
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
// to exec.Command, exec.CommandContext or exec.LookPath as a string literal or
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
			if !ok || pkg.Name != "exec" {
				return true
			}
			arg := -1
			switch sel.Sel.Name {
			case "Command", "LookPath":
				arg = 0
			case "CommandContext":
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
			if !strings.Contains(containerfile, r.provider) {
				t.Errorf("%s:%d: %s says %q carries it, and %s never mentions that", functionalImageList, r.line, r.name, r.provider, functionalImageFile)
			}
		case "absent":
			if len(strings.Fields(r.provider)) < 3 {
				t.Errorf("%s:%d: %s is absent from the image and the reason %q is not a sentence", functionalImageList, r.line, r.name, r.provider)
			}
		}
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
	tasks := readFile(t, filepath.Join(root, filepath.FromSlash(containerRuntimeRole+"/tasks/main.yml")))
	readme := readFile(t, filepath.Join(root, filepath.FromSlash(functionalImageReadme)))
	for _, flag := range []string{"--network", "--pids-limit", "--memory", "--cpus", "--read-only", "--tmpfs", "--timeout", "--ipc"} {
		if !strings.Contains(tasks, "- "+flag) {
			t.Errorf("%s/tasks/main.yml: the probe does not run with %s", containerRuntimeRole, flag)
		}
		if !strings.Contains(readme, flag) {
			t.Errorf("%s: the run command does not carry %s, which the runtime probe proves", functionalImageReadme, flag)
		}
	}
	if !strings.Contains(readme, "cat /image-manifest.txt") {
		t.Errorf("%s: no one-line command prints /image-manifest.txt", functionalImageReadme)
	}
}
