package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	exitUsage       = 2
	exitCannotRun   = 125
	exitDeadline    = 124
	exitInterrupted = 130

	// minDeadline leaves room for the two inner bounds under the deadline:
	// the in-container timeout ends 10 s before it and go test's own -timeout
	// 20 s before it, so go test prints the stack of a hung test first.
	minDeadline = 30 * time.Second
)

// runConfig is everything one run needs, from flags. Nothing is guessed: every
// default is stated in the usage.
type runConfig struct {
	src      string
	context  string
	image    string
	deadline time.Duration
	grace    time.Duration
	cpus     int
	memory   string
	pids     int
	scratch  string
	gocache  string
	// freshGocache: a throwaway build cache, an anonymous volume that goes
	// with the container, instead of this user's shared one.
	freshGocache bool
	gomod        string
	podman       string
	packages     []string
	ownerID      string
}

type reapConfig struct {
	grace  time.Duration
	dryRun bool
	podman string
}

// packageRE is the allowlist for a package argument. It reaches a shell inside
// the container (make's recipe), so it admits only what a package directory is
// made of: ./ then segments of [A-Za-z0-9_.-], and an optional /... at the end.
var packageRE = regexp.MustCompile(`^\.(/[A-Za-z0-9_.-]+)*/?(\.\.\.)?$`)

var sizeRE = regexp.MustCompile(`^[1-9][0-9]*[kmgKMG]?$`)

// volumeNameRE is podman's own rule for a volume name.
var volumeNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

func parseRun(args []string) (runConfig, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var c runConfig
	fs.StringVar(&c.src, "src", ".", "")
	fs.StringVar(&c.context, "context", "", "")
	fs.StringVar(&c.image, "image", "", "")
	fs.DurationVar(&c.deadline, "deadline", 10*time.Minute, "")
	fs.DurationVar(&c.grace, "grace", 30*time.Second, "")
	fs.IntVar(&c.cpus, "cpus", 4, "")
	fs.StringVar(&c.memory, "memory", "4g", "")
	fs.IntVar(&c.pids, "pids", 1024, "")
	fs.StringVar(&c.scratch, "scratch", "2g", "")
	fs.StringVar(&c.gocache, "gocache-volume", "", "")
	fs.StringVar(&c.gomod, "gomod-volume", "", "")
	fs.BoolVar(&c.freshGocache, "fresh-gocache", false, "")
	fs.StringVar(&c.podman, "podman", "podman", "")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	c.packages = fs.Args()
	if len(c.packages) == 0 {
		return c, errors.New("no package directory given (./internal/ntable/... and so on)")
	}
	for _, p := range c.packages {
		if strings.HasPrefix(p, "-") {
			return c, fmt.Errorf("flag %q after the packages; flags come first", p)
		}
		if !packageRE.MatchString(p) || slices.Contains(strings.Split(p, "/"), "..") {
			return c, fmt.Errorf("package %q is not a package directory of the source tree: ./ then path segments of letters, digits, _ . and -, an optional trailing /..., and no ..", p)
		}
	}
	if c.deadline < minDeadline {
		return c, fmt.Errorf("--deadline %s is under %s", c.deadline, minDeadline)
	}
	if c.grace < 0 {
		return c, fmt.Errorf("--grace %s is negative", c.grace)
	}
	if c.cpus < 1 {
		return c, fmt.Errorf("--cpus %d is under 1", c.cpus)
	}
	if c.pids < 64 {
		return c, fmt.Errorf("--pids %d is under 64", c.pids)
	}
	if !sizeRE.MatchString(c.memory) {
		return c, fmt.Errorf("--memory %q is not a size such as 4g", c.memory)
	}
	if !sizeRE.MatchString(c.scratch) {
		return c, fmt.Errorf("--scratch %q is not a size such as 2g", c.scratch)
	}
	src, err := filepath.Abs(c.src)
	if err != nil {
		return c, fmt.Errorf("--src: %v", err)
	}
	if _, err := os.Stat(filepath.Join(src, "go.mod")); err != nil {
		return c, fmt.Errorf("--src %s holds no go.mod", src)
	}
	c.src = src
	if c.image == "" {
		if c.context == "" {
			c.context = filepath.Join(src, "infra", "functional-image")
		}
		ctxDir, err := filepath.Abs(c.context)
		if err != nil {
			return c, fmt.Errorf("--context: %v", err)
		}
		if _, err := os.Stat(filepath.Join(ctxDir, "Containerfile")); err != nil {
			return c, fmt.Errorf("--context %s holds no Containerfile; pass --context <dir> or --image <ref>", ctxDir)
		}
		c.context = ctxDir
	}
	c.ownerID = strconv.Itoa(os.Getuid())
	if c.gocache == "" {
		c.gocache = cacheVolumeName("gocache", c.ownerID)
	}
	if c.gomod == "" {
		c.gomod = cacheVolumeName("gomod", c.ownerID)
	}
	for _, v := range []string{c.gocache, c.gomod} {
		if !volumeNameRE.MatchString(v) {
			return c, fmt.Errorf("volume name %q is not a podman volume name", v)
		}
	}
	if c.freshGocache && c.gocache != cacheVolumeName("gocache", c.ownerID) {
		return c, errors.New("--fresh-gocache and --gocache-volume name two different build caches; give one")
	}
	if c.gocache == c.gomod {
		return c, errors.New("the build cache and the module cache must be two volumes")
	}
	return c, nil
}

func parseReap(args []string) (reapConfig, error) {
	fs := flag.NewFlagSet("reap", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var c reapConfig
	fs.DurationVar(&c.grace, "grace", 30*time.Second, "")
	fs.BoolVar(&c.dryRun, "dry-run", false, "")
	fs.StringVar(&c.podman, "podman", "podman", "")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if fs.NArg() > 0 {
		return c, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if c.grace < 0 {
		return c, fmt.Errorf("--grace %s is negative", c.grace)
	}
	return c, nil
}

// cacheVolumeName is the default name of one of the two cache volumes of a
// user: one per user and per kind, so no two users share a cache and a run
// never writes another user's.
func cacheVolumeName(kind, ownerID string) string {
	return "nova-functional-" + kind + "-uid" + ownerID
}
