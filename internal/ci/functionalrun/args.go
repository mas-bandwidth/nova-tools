package functionalrun

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The labels this tool puts on every container it creates. The reaper and the
// leftover check select on labelRun, never on a name.
const (
	labelRun      = "nova.functional.run"
	labelStart    = "nova.functional.start"
	labelDeadline = "nova.functional.deadline"
	labelOwner    = "nova.functional.owner"
	// labelCache marks a cache volume. Caches carry no run label, so the
	// reaper and the leftover check never count or remove them.
	labelCache = "nova.functional.cache"

	namePrefix = "nova-functional-"
	imageRepo  = "localhost/nova-functional"

	// The image's non-root user's home, a tmpfs here.
	benchHome = "/home/bench"

	// modStamp records, inside the module cache volume, the hash of the go.mod
	// and go.sum it was filled for, so a warm cache is not downloaded again.
	modStamp = "/gomodcache/.functionalrun-stamp"

	// prefillDeadline bounds the networked module download.
	prefillDeadline = 5 * time.Minute

	// The inner bounds, under the deadline: the in-container timeout (a clean
	// end with 124, KILL after killAfter to what ignores TERM) and go test's
	// own -timeout (the stack of a hung test is printed first).
	innerMargin  = 10 * time.Second
	goTestMargin = 20 * time.Second
	killAfter    = 5 * time.Second

	// clientGrace is how long past the deadline this process waits for the
	// runtime's own bound to end the container before it removes it itself.
	clientGrace = 5 * time.Second
)

// runLabels are the labels of one container of a run: its id, its start and
// its deadline in unix seconds, and the user it belongs to.
func runLabels(runID string, start, deadline time.Time, ownerID string) []string {
	return []string{
		"--label", labelRun + "=" + runID,
		"--label", labelStart + "=" + strconv.FormatInt(start.Unix(), 10),
		"--label", labelDeadline + "=" + strconv.FormatInt(deadline.Unix(), 10),
		"--label", labelOwner + "=" + ownerID,
	}
}

// timeoutSeconds is the runtime's --timeout for a bound: whole seconds,
// rounded up, and never 0, which the runtime reads as no bound at all.
func timeoutSeconds(d time.Duration) int {
	s := int((d + time.Second - 1) / time.Second)
	return max(s, 1)
}

func containerName(runID string) string { return namePrefix + runID }

// isDocker is whether the config's runtime is docker; anything else is podman.
func (c runConfig) isDocker() bool { return c.kind == kindDocker }

// boundArgs is how the runtime holds the deadline of a container
// (docs/SPEC-CI.md, "functional-container"; tla/ContainerRun.tla, RuntimeBound).
// podman: `run --timeout`, enforced by the runtime's monitor outside the
// container, which holds when this process is killed. docker has no `run
// --timeout`, so nothing is passed for it here: the bound there is the
// in-container `timeout -k` under --init (innerCommand, in the argv of both
// engines), this process's own removal at the deadline plus clientGrace
// (runContainer), and the reaper by the deadline label (reap).
func boundArgs(c runConfig, d time.Duration) []string {
	if c.isDocker() {
		return nil
	}
	return []string{"--timeout", strconv.Itoa(timeoutSeconds(d))}
}

// isolationArgs is every isolation and resource bound of a container, spelled
// out and never left to a default (docs/SPEC-CI.md, "functional-container"):
// no capability and no privilege gained through a setuid binary, memory with
// no swap, pids and cpus, and private IPC and PID namespaces. net is the
// network mode: none for the tests. docker's --pid takes no "private" (its
// private namespace is the default, and it refuses the word), so for docker
// the flag is left out and the test asserts that no --pid host is there.
func isolationArgs(c runConfig, net string, cpus bool) []string {
	args := []string{
		"--security-opt", "no-new-privileges",
		"--cap-drop", "all",
	}
	if net != "" {
		args = append(args, "--network", net)
	}
	args = append(args, "--ipc", "private")
	if !c.isDocker() {
		args = append(args, "--pid", "private")
	}
	args = append(args,
		"--pids-limit", strconv.Itoa(c.pids),
		"--memory", c.memory,
		"--memory-swap", c.memory,
	)
	if cpus {
		args = append(args, "--cpus", strconv.Itoa(c.cpus))
	}
	return args
}

// testArgs is the argv of the test container: the functional tier of the
// packages through the Makefile's own target, so the selection and the go test
// flags are the ones make test-functional runs anywhere.
func testArgs(c runConfig, image, runID string, start time.Time) []string {
	deadline := start.Add(c.deadline)
	args := []string{
		"run", "--rm", "--init",
		"--name", containerName(runID),
	}
	args = append(args, runLabels(runID, start, deadline, c.ownerID)...)
	gocacheMount := c.gocache + ":/gocache"
	if c.freshGocache {
		// An anonymous volume: --rm and every removal here take it with the
		// container, so nothing this run writes to its build cache outlives it.
		gocacheMount = "/gocache"
	}
	args = append(args, boundArgs(c, c.deadline)...)
	// The tests run as the image's non-root user and need no capability.
	args = append(args, isolationArgs(c, "none", true)...)
	args = append(args,
		"--read-only",
		"--tmpfs", "/tmp:rw,exec,size="+c.scratch,
		"--tmpfs", benchHome+":rw,size=1g,mode=1777",
		"-v", c.src+":/src:ro",
		"-v", c.gomod+":/gomodcache:ro",
		"-v", gocacheMount,
		"-w", "/src",
		image,
		"timeout", "-k", strconv.Itoa(timeoutSeconds(killAfter)),
		strconv.Itoa(timeoutSeconds(c.deadline-innerMargin)),
		"make", "test-functional",
		"PKGS="+strings.Join(c.packages, " "),
		"GOTEST_P="+strconv.Itoa(c.cpus),
		"FUNCTIONAL_TIMEOUT="+strconv.Itoa(timeoutSeconds(c.deadline-goTestMargin))+"s",
	)
	return args
}

// prefillScript fills the module cache for the go.mod and go.sum whose hash
// is $1, and does nothing when the volume was already filled for them.
const prefillScript = `set -e
if [ "$(cat ` + modStamp + ` 2>/dev/null)" = "$1" ]; then echo "functionalrun: module cache is warm"; exit 0; fi
go mod download
printf '%s\n' "$1" > ` + modStamp

// prefillArgs is the argv of the one networked step: go mod download into the
// module cache volume, which every test container then mounts read-only.
func prefillArgs(c runConfig, image, runID, stamp, proxy string, start time.Time) []string {
	id := runID + "-mod"
	args := []string{"run", "--rm", "--init", "--name", containerName(id)}
	args = append(args, runLabels(id, start, start.Add(prefillDeadline), c.ownerID)...)
	args = append(args, boundArgs(c, prefillDeadline)...)
	// The one networked step: the default network, every other bound kept.
	args = append(args, isolationArgs(c, "", false)...)
	args = append(args,
		"--read-only",
		"--tmpfs", "/tmp:rw,exec,size=1g",
		"--tmpfs", benchHome+":rw,size=256m,mode=1777",
		"-v", c.src+":/src:ro",
		"-v", c.gomod+":/gomodcache",
		"-e", "GOPROXY="+proxy,
		"-w", "/src",
		image,
	)
	if c.isDocker() {
		// No `run --timeout` on docker: the step's own bound, inside.
		args = append(args, "timeout", "-k", strconv.Itoa(timeoutSeconds(killAfter)), strconv.Itoa(timeoutSeconds(prefillDeadline-innerMargin)))
	}
	args = append(args, "sh", "-c", prefillScript, "functionalrun", stamp)
	return args
}

// buildArgs is the argv that builds the image from its context under a tag
// named by the context's hash, so an unchanged context is never built twice.
func buildArgs(contextDir, tag string) []string {
	return []string{
		"build", "--tag", tag,
		"--file", filepath.Join(contextDir, "Containerfile"),
		contextDir,
	}
}

// removeArgs force-removes one container, with its anonymous volumes, at once.
// --ignore: a container the runtime already removed is not an error.
func removeArgs(nameOrID string) []string {
	return []string{"rm", "--force", "--ignore", "--volumes", "--time", "0", nameOrID}
}

// removeArgsFor is removeArgs for one runtime: docker's rm has neither --ignore
// nor --time (rm --force kills at once), and says "No such container" for one
// already gone, which isGone reads as removed.
func removeArgsFor(kind, nameOrID string) []string {
	if kind == kindDocker {
		return []string{"rm", "--force", "--volumes", nameOrID}
	}
	return removeArgs(nameOrID)
}

// isGone is whether a removal's error says the container did not exist: the
// state the removal was after.
func isGone(err error) bool {
	return err != nil && strings.Contains(err.Error(), "No such container")
}

// leftoverArgs lists every container, in any state, of one run: by its label.
func leftoverArgs(runID string) []string {
	return []string{"ps", "--all", "--filter", "label=" + labelRun + "=" + runID, "--format", "{{.ID}}"}
}

// reapListArgs lists every container of this tool, in any state: by the
// presence of the run label, never by a name.
func reapListArgs() []string {
	return []string{"ps", "--all", "--filter", "label=" + labelRun, "--format", "json"}
}

// dockerListFormat is docker's listing: one container a line, tab-separated
// (docker prints a container's labels as one string, not a map): id, state,
// then the four labels this tool writes.
const dockerListFormat = `{{.ID}}` + "\t" + `{{.State}}` + "\t" +
	`{{.Label "` + labelRun + `"}}` + "\t" + `{{.Label "` + labelStart + `"}}` + "\t" +
	`{{.Label "` + labelDeadline + `"}}` + "\t" + `{{.Label "` + labelOwner + `"}}`

// reapListArgsFor is reapListArgs for one runtime.
func reapListArgsFor(kind string) []string {
	if kind == kindDocker {
		return []string{"ps", "--all", "--filter", "label=" + labelRun, "--format", dockerListFormat}
	}
	return reapListArgs()
}

func volumeInspectArgs(name string) []string {
	return []string{"volume", "inspect", "--format", `{{index .Labels "` + labelOwner + `"}}|{{index .Labels "` + labelCache + `"}}`, name}
}

func volumeCreateArgs(name, kind, ownerID string) []string {
	return []string{"volume", "create", "--label", labelCache + "=" + kind, "--label", labelOwner + "=" + ownerID, name}
}

// newRunID is a run's id: the start time, to read, and random hex, so two runs
// never share one.
func newRunID(start time.Time) string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return start.UTC().Format("20060102t150405") + "-" + hex.EncodeToString(b[:])
}

// contextHash is the sha256 of the build context: every regular file's path
// relative to the context and its bytes, in path order.
func contextHash(dir string) (string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", f, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func imageTag(hash string) string { return imageRepo + ":ctx-" + hash[:16] }

// modHash is the hash of go.mod and go.sum: what the module cache is filled for.
func modHash(src string) (string, error) {
	h := sha256.New()
	for _, name := range []string{"go.mod", "go.sum"} {
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil && (name != "go.sum" || !os.IsNotExist(err)) {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// defaultProxy is the public module proxy, for a caller whose GOPROXY names none.
const defaultProxy = "https://proxy.golang.org,direct"

// moduleProxy is the proxy the networked step uses: the caller's GOPROXY when
// it names one, the public default otherwise (the image's own is off).
func moduleProxy(getenv func(string) string) string {
	p := strings.TrimSpace(getenv("GOPROXY"))
	if p == "" || p == "off" {
		return defaultProxy
	}
	return p
}
