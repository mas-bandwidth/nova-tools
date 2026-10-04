package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/dogfood"
)

// SumsFile is the name of the checksum file in every artifact directory, in the
// format `sha256sum -c` reads, because the person verifying a copy by hand
// should not need this tool to do it.
const SumsFile = "SHA256SUMS"

// DigestFile holds the sha256 OF SumsFile, written beside it by `release build`.
//
// `adopt` fetching a release from another machine must check it against a digest that
// did NOT travel with the bits, and the two ways to have one -- the annotated tag and
// the CHANGELOG entry -- both belong to a TAGGED release. A dev build has no tag, so
// the only other place to get a digest is the machine being adopted FROM, which is that machine
// vouching for its own bytes and is not evidence at all. This file is written where
// the build ran, on the coordinator, out of the SHA256SUMS the build had just verified;
// `adopt --expect-sums-from` reads it from there. A digest computed on the machine
// being adopted from is not evidence about a fetch.
const DigestFile = "SUMS.digest"

// Platform is the goos-goarch an artifact directory is named for. A release
// built here for this host is the fleet's common case, and --platform is the
// flag for the other one: cross-compiling to a bench from wherever the release
// was cut.
func Platform(flagValue string) (string, string, error) {
	if flagValue == "" {
		return runtime.GOOS, runtime.GOARCH, nil
	}
	goos, goarch, ok := strings.Cut(flagValue, "-")
	if !ok || goos == "" || goarch == "" {
		return "", "", refuse("pass --platform <goos>-<goarch>, for example linux-amd64",
			"%q is not a goos-goarch", flagValue)
	}
	return goos, goarch, nil
}

// ExeSuffix is what a tool's FILE is called on one platform: `nova-bus` on unix,
// `nova-bus.exe` on windows. It takes the TARGET's goos, never the host's,
// because every one of these names is decided for the machine the binary will
// run on rather than for the machine deciding it: a release cut for a windows
// bench names windows files, and a release built ON windows names them the same
// way.
//
// Reading runtime.GOOS at each of these sites instead is the defect this exists
// to make impossible, and it is a defect that hides: it is right on the host
// that happens to match and silently wrong on every other, so the artifacts end
// up called one thing while everything looking for them asks for another. The
// product half is `adopt` composing the remote command as a bare `nova-update`,
// which names a path that does not exist on a windows bench.
func ExeSuffix(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

// ToolFile is the file one tool installs under on the target platform. Every
// place in this package that turns a tool NAME into a FILE goes through it.
func ToolFile(tool, goos string) string { return tool + ExeSuffix(goos) }

// ArtifactDir is where one platform's binaries for one version live, under the
// root --out or --from names. The version and the platform are both in the path
// so that one root can hold several releases and several platforms at once,
// which is what a build bench serving four benches actually holds.
func ArtifactDir(root, version, goos, goarch string) string {
	return filepath.Join(root, version, goos+"-"+goarch)
}

// Tools lists the shipped set: every cmd/nova-* directory in the source tree,
// discovered by walking it rather than from a list. release.yml makes the same
// argument at greater length -- a tool added tomorrow ships on the day it
// appears rather than on the day somebody remembers a list.
func Tools(source string) ([]string, error) {
	// One definition, shared with the dogfood gate's shipped set: what is
	// compiled is what is judged.
	return dogfood.CmdTools(filepath.Join(source, "cmd"))
}

func build(ctx context.Context, o options, deps Deps, out, errs io.Writer) int {
	// THE DEFINITION OF DONE, FIRST -- before a single tool is compiled. A dev
	// build has no tag and no changelog, which is exactly why it needs the
	// gate rather than exactly why it escapes one: the fourth dogfood's
	// releases reached four benches as dev builds, and `adopt` never asks what
	// a release was gated on. Twenty-one binaries compiled and then refused is
	// also twenty-one compiles nobody needed.
	gate, err := dogfoodCheck("BUILD", o, deps, o.source, out, errs)
	if err != nil {
		if errors.Is(err, errDogfood) {
			return 2
		}
		return refusal(errs, "BUILD", err)
	}
	// EVERY PLATFORM IS RESOLVED BEFORE THE FIRST COMPILE. A list whose fourth
	// entry is a typo must not be found out after three platforms have been
	// built: that is a half-built release root somebody then has to reason
	// about.
	wanted := []string(o.platforms)
	if len(wanted) == 0 {
		wanted = []string{""} // this host
	}
	type target struct{ goos, goarch string }
	targets := make([]target, 0, len(wanted))
	for _, p := range wanted {
		goos, goarch, err := Platform(p)
		if err != nil {
			return refusal(errs, "BUILD", err)
		}
		targets = append(targets, target{goos, goarch})
	}
	tc := deps.Toolchain
	if tc == nil {
		tc = GoBuild{}
	}
	// AND EVERY PAIR IS ONE THE COMPILER KNOWS, asked of the compiler. The
	// fourth dogfood's `--platform darwin-arm64,darwin-amd64` reached a build
	// that took --platform as one string, failed at tool 1 of 21 with the
	// compiler's own `unsupported GOOS/GOARCH pair`, and left an empty
	// directory of that name in the release tree. A pair nobody can build is a
	// refusal here, before a directory exists to be left behind.
	supported, err := tc.Platforms(ctx)
	if err != nil {
		return refusal(errs, "BUILD", fmt.Errorf("cannot ask the Go toolchain which platforms it supports: %w (is `go` on PATH?)", err))
	}
	known := make(map[string]bool, len(supported))
	for _, p := range supported {
		known[strings.ReplaceAll(strings.TrimSpace(p), "/", "-")] = true
	}
	var unsupported []string
	for _, tgt := range targets {
		if name := tgt.goos + "-" + tgt.goarch; !known[name] {
			unsupported = append(unsupported, name)
		}
	}
	if len(unsupported) > 0 {
		return refusal(errs, "BUILD", refuse("name a pair `go tool dist list` prints, as <goos>-<goarch>",
			"this toolchain cannot build %s: %s", plural(len(unsupported), "platform"), strings.Join(unsupported, ", ")))
	}
	tools, err := Tools(o.source)
	// A --source with no cmd/ at all is the same mistake as one with no
	// cmd/nova-*: somebody named a directory that is not a nova-tools
	// checkout. One refusal says so, rather than two that read differently
	// for the same error.
	if err != nil && !os.IsNotExist(err) {
		return refusal(errs, "BUILD", fmt.Errorf("cannot read %s: %w (name a nova-tools checkout with --source)", filepath.Join(o.source, "cmd"), err))
	}
	if len(tools) == 0 {
		return refusal(errs, "BUILD", refuse("name a nova-tools checkout with --source",
			"%s holds no cmd/nova-* directory, so this build would ship an empty set", o.source))
	}
	// THE STAMP IS COMPOSED ONCE, before the first target, the way
	// tools/ghrelease's ldflags verb composes it once for the release
	// workflow: `-X main.version=` with an empty value is a legal linker flag
	// that stamps nothing, and nothing downstream notices.
	args := []string{"-trimpath", "-ldflags", Ldflags(o.version)}
	// What the checkout is, once: the record every platform writes, and the
	// base an --incremental build diffs against.
	src := deps.Source
	if src == nil {
		src = ExecSource{}
	}
	commit, clean, err := src.Head(ctx, o.source)
	if err != nil {
		commit = ""
	}
	goVersion, _ := src.GoVersion(ctx)
	// What the summary line names: every platform built, and every platform's
	// digest, in the order they were asked for. The summary must name every
	// platform or the missing one is silent: a release half a platform short
	// is a release whose receipt agrees with what was built and disagrees with
	// what was promised.
	var names, digests []string
	for _, tgt := range targets {
		goos, goarch := tgt.goos, tgt.goarch
		dir := ArtifactDir(o.out, o.version, goos, goarch)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return refusal(errs, "BUILD", fmt.Errorf("cannot create %s: %w (name a writable --out)", dir, err))
		}
		p := plan{rebuild: tools}
		if o.incremental {
			p = planIncremental(ctx, src, o, tools, goos, goarch, commit, goVersion, clean)
			incrementalLine(out, o.version, goos+"-"+goarch, p)
			for _, tool := range p.reuse {
				if err := reuseFrom(p, tool, goos, dir); err != nil {
					return refusal(errs, "BUILD", fmt.Errorf("cannot reuse %s from %s: %w (build without --incremental)", tool, p.base, err))
				}
			}
		}
		for i, tool := range p.rebuild {
			progress(errs, "building %s for %s/%s (%d/%d)", tool, goos, goarch, i+1, len(p.rebuild))
			output, err := tc.Build(ctx, o.source, "./cmd/"+tool, filepath.Join(dir, ToolFile(tool, goos)), goos, goarch, args)
			if err != nil {
				fmt.Fprintf(errs, "BUILD FAIL tool=%s platform=%s version=%s: %s (fix the compile error and build again; no %s was written)\n",
					field(tool), field(goos+"-"+goarch), field(o.version), oneLine(output, err), SumsFile)
				return 1
			}
		}
		// SHA256SUMS LAST, over the whole set, in the same step that finished
		// it. A checksum file written beside a half-built directory is a file
		// that agrees with itself and with nothing anybody released.
		sums, err := writeSums(dir)
		if err != nil {
			return refusal(errs, "BUILD", fmt.Errorf("cannot write %s: %w (name a writable --out)", filepath.Join(dir, SumsFile), err))
		}
		// AND READ BACK IMMEDIATELY. release.yml has verified its own
		// SHA256SUMS since the beginning, for the reason it gives in place: a
		// checksum file nobody has ever checked is a file whose first reader
		// is the person it was supposed to reassure. This is that check, one
		// step earlier, where the remedy is still `build again`.
		arts, err := ReadSums(dir)
		if err != nil {
			return refusal(errs, "BUILD", fmt.Errorf("the %s just written cannot be read back: %w", SumsFile, err))
		}
		progress(errs, "verifying the %d artifacts just written for %s-%s", len(arts), goos, goarch)
		verified, err := VerifyArtifacts(dir, arts)
		if err != nil {
			return refusal(errs, "BUILD", fmt.Errorf("the %s just written does not describe what was built: %w", SumsFile, err))
		}
		// THE DIGEST OF THE CHECKSUM FILE, computed HERE and written HERE.
		// It is the one number `adopt` needs about a release that has no tag,
		// and the one place it can be got honestly is the machine that built
		// the bits rather than the machine holding a copy of them.
		digest, err := fileSum(sums)
		if err != nil {
			return refusal(errs, "BUILD", fmt.Errorf("cannot hash the %s just written: %w", SumsFile, err))
		}
		digestPath := filepath.Join(dir, DigestFile)
		if err := writeNoFollow("write digest", digestPath, []byte(digest+"\n"), 0o644); err != nil {
			return refusal(errs, "BUILD", fmt.Errorf("cannot write %s: %w (name a writable --out)", digestPath, err))
		}
		// THE RECORD, LAST, beside the platform directory and never in it: what
		// a later --incremental build diffs against. A dirty or non-git
		// checkout records no commit, and such a record is never a base.
		rec := buildRecord{Go: goVersion, Ldflags: ldflagsShape(), Base: p.base, Rebuilt: p.rebuild, Reused: p.reuse, Gate: gate, Reason: o.reason}
		if clean {
			rec.Commit = commit
		}
		if err := writeNoFollow("write build record", recordPath(o.out, o.version, goos+"-"+goarch), rec.encode(), 0o644); err != nil {
			return refusal(errs, "BUILD", fmt.Errorf("cannot write the build record: %w (name a writable --out)", err))
		}
		names = append(names, goos+"-"+goarch)
		digests = append(digests, digest)
		fmt.Fprintf(out, "RELEASE BUILT version=%s platform=%s tools=%d verified=%d out=%s sums=%s digest=%s\n",
			field(o.version), field(goos+"-"+goarch), len(tools), verified, field(dir), field(digest), field(digestPath))
	}
	// LAST, and never a reason to fail: every platform is built and verified,
	// and the version directories under --out that prune.go's rule does not keep are
	// removed. The version just built stays, and so does the version of the
	// nova-update running this build, which is what this machine has installed.
	self := ""
	if deps.Self != nil {
		self = deps.Self()
	}
	pruned, pruneFailed := pruneDefault(o.out, func(name string) bool { return name == o.version || name == self }, errs)
	// ONE LINE THAT NAMES EVERY PLATFORM. Every --platform is recorded, in the
	// order it was given; `platforms=` and `sums=` are the same list in the same
	// order, one token each, so a summary line with one of them shorter than the
	// other names the platform that was never built.
	fmt.Fprintf(out, "RELEASE BUILD OK version=%s platforms=%s tools=%d sums=%s dogfood=%s out=%s pruned=%d prune-failed=%d\n",
		field(o.version), field(strings.Join(names, ",")), len(tools), field(strings.Join(digests, ",")), gate, field(o.out), pruned, pruneFailed)
	return 0
}

// writeSums hashes every file in dir except the checksum file itself and writes
// the result in `sha256sum` format. The file cannot list itself: a checksum over
// a file that is being written is a number nobody can reproduce.
func writeSums(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var lines []string
	for _, e := range entries {
		// Neither the checksum file nor its own digest: a checksum over a
		// file being written is a number nobody can reproduce, and a checksum
		// over the digest OF the checksum file is a number that depends on
		// the last time this directory was built.
		if e.IsDir() || e.Name() == SumsFile || e.Name() == DigestFile {
			continue
		}
		sum, err := fileSum(filepath.Join(dir, e.Name()))
		if err != nil {
			return "", err
		}
		lines = append(lines, sum+"  "+e.Name())
	}
	sort.Strings(lines)
	path := filepath.Join(dir, SumsFile)
	return path, writeNoFollow("write sums", path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// oneLine folds a child's output and its error into one readable token, bounded,
// so a compiler that printed 400 lines does not print 400 lines here.
func oneLine(output string, err error) string {
	text := strings.TrimSpace(output)
	if text == "" {
		text = err.Error()
	} else {
		text = err.Error() + ": " + text
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 300 {
		text = text[:300] + "..."
	}
	return text
}
