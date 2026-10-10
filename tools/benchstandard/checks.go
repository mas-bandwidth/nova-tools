package main

import (
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// NOVA_TOOLCHAIN_ROOTS BEGIN
//
// The toolchain roots the sandbox wall grants a card, under the bench user's
// home. This list and internal/swarm/toolchain.go are ONE list: a class test in
// internal/ci reads both and fails when they differ, because the same paths
// named in two places are how a standard and a wall come to contradict each
// other, and the contradiction is found by a card that dies. The KIND of each
// grant (sdk is read and execute, go/pkg/mod is read without execute) is the
// wall's decision and lives in internal/swarm/toolchain.go; a bench only has to
// HAVE the directories.
var toolchainRoots = []string{"sdk", "go/pkg/mod"}

// NOVA_TOOLCHAIN_ROOTS END

// NOVA_WALL_READ_ROOTS BEGIN
//
// The wall's whole linux read table. Every entry is landlock's read subset,
// which carries EXECUTE, so a tool under any of them can run inside the wall.
// This list and linuxReadRoots in internal/sandbox/wrap_linux.go are ONE list,
// in the same order: a class test in internal/ci reads both, because a subset
// picked by hand reports a tool under /etc or /dev "under NO read root" while
// the wall executes it.
var wallReadRoots = []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/run/systemd/resolve", "/opt", "/dev", "/proc"}

// NOVA_WALL_READ_ROOTS END

// wallExecRoots is every root a card can EXECUTE a tool from: the wall's read
// table, the directory /etc/resolv.conf resolves to on this machine (the wall
// grants it too), and the one home toolchain root carrying execute, $HOME/sdk.
// resolvDir is "" when the machine has none.
func wallExecRoots(home, resolvDir string) []string {
	roots := append([]string{}, wallReadRoots...)
	if resolvDir != "" {
		roots = append(roots, resolvDir)
	}
	return append(roots, filepath.Join(home, "sdk"))
}

// novaBins are the binaries a bench carries under $HOME/.local/bin, each of
// which must report the wanted version.
var novaBins = []string{
	"nova-bus", "nova-check", "nova-fuse", "nova-memory", "nova-sandbox",
	"nova-secrets", "nova-self-talk", "nova-worker", "nova-tokens", "nova-update",
	"nova-version",
}

const (
	defaultSBCL      = "2.5.8"
	defaultMinFreeG  = 25
	defaultProbeURL  = "https://models.opencode.ai/api.json"
	runnerDirPattern = "runner-nova-tools-*"
)

// witness is one run of the standard against one bench.
type witness struct {
	h      host
	out    io.Writer
	home   string
	apply  bool
	env    map[string]string // the card environment: the process's, with the sdk env file's on top
	drifts int
	strays []string
	freeG  string

	goWant  string // the wanted go, as `go version` names it
	want    string // the wanted nova version
	harness string // NOVA_HARNESS, an explicit harness binary

	systemDir string // the system-wide systemd unit directory; empty reads the user units only
}

// drift prints one finding.
func (w *witness) drift(format string, a ...any) {
	fmt.Fprintf(w.out, "DRIFT "+format+"\n", a...)
	w.drifts++
}

func (w *witness) get(key string) string { return w.env[key] }

// which is `command -v` on the card's PATH.
func (w *witness) which(name string) (string, bool) { return w.h.LookPath(name, w.env["PATH"]) }

// runTool runs a resolved program in the card environment, with overrides
// (KEY=value) replacing variables of the same name.
func (w *witness) runTool(path string, args []string, override ...string) runResult {
	env := envList(w.env)
	for _, o := range override {
		k, _, _ := strings.Cut(o, "=")
		kept := env[:0:0]
		for _, e := range env {
			if !strings.HasPrefix(e, k+"=") {
				kept = append(kept, e)
			}
		}
		env = append(kept, o)
	}
	return w.h.Run(runSpec{name: path, args: args, env: env})
}

// trimNL is what a shell's $(...) does to an output: drop the trailing newlines.
func trimNL(s string) string { return strings.TrimRight(s, "\n") }

// realPath is `readlink -f p || echo p`.
func (w *witness) realPath(p string) string {
	if r, err := w.h.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// under is the shell test case "$p" in "$root"/*: p is below root.
func under(p, root string) bool { return strings.HasPrefix(p, root+"/") }

func (w *witness) isDir(p string) bool {
	fi, err := w.h.Stat(p)
	return err == nil && fi.IsDir()
}

func (w *witness) exists(p string) bool {
	_, err := w.h.Stat(p)
	return err == nil
}

// isExec is the shell test [ -x p ]: present, with an execute bit.
func (w *witness) isExec(p string) bool {
	fi, err := w.h.Stat(p)
	return err == nil && fi.Mode().Perm()&0o111 != 0
}

// glob lists dir's entries matching pattern, the directory matched literally.
func (w *witness) glob(dir, pattern string) []string {
	m, _ := w.h.Glob(filepath.Join(escapeGlob(dir), pattern))
	return m
}

// escapeGlob makes every glob metacharacter of a literal directory literal.
func escapeGlob(dir string) string {
	if filepath.Separator == '\\' {
		return dir
	}
	var b strings.Builder
	for _, r := range dir {
		if strings.ContainsRune(`*?[\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// standard runs every check in order and prints the verdict. It returns the
// exit code: 0 for STANDARD OK, 1 for DRIFT.
func (w *witness) standard() int {
	w.checkRunners()
	w.killStrays()
	w.checkToolchainRoots()
	w.checkGoAndSbcl()
	w.checkWallExecutable()
	w.checkSbclPin()
	w.checkProRung()
	w.checkSqlite()
	w.checkSlotShare()
	harnessOK, hbin := w.checkHarness()
	if harnessOK && w.h.OS() == "linux" {
		w.checkHarnessCanary(hbin)
	}
	if w.h.OS() == "linux" {
		w.checkSandboxNetwork()
	}
	w.checkBins()
	w.checkSeat()
	w.checkPlaintextKeys()
	w.checkDisk()

	if w.drifts == 0 {
		want := w.want
		if want == "" {
			want = "unset"
		}
		fmt.Fprintf(w.out, "STANDARD OK go=%s bins=%s harness=ok seats=1 free=%sG\n", w.goWant, want, w.freeG)
		return 0
	}
	fmt.Fprintln(w.out, "STANDARD DRIFT (see lines above)")
	return 1
}

// checkRunners: each self-hosted runner directory has exactly one listener, the
// listener is under its systemd unit, and the unit file carries the standard
// stanzas. A runner is a linux thing; nothing here runs elsewhere.
func (w *witness) checkRunners() {
	if w.h.OS() != "linux" {
		return
	}
	for _, d := range w.glob(w.home, runnerDirPattern) {
		if !w.isDir(d) {
			continue
		}
		d += "/"
		base := filepath.Base(d)
		idx := base[strings.LastIndex(base, "-")+1:]
		unit := "nova-runner-" + idx + ".service"

		// (1) exactly one listener process mentioning the runner directory. The
		// listener binary is matched, not the directory: the runner's own shell
		// scripts carry the directory too. The table is one snapshot taken before
		// anything is matched against it.
		ps, found := w.which("ps")
		if !found {
			w.drift("%s ps not available to count listeners in %s", unit, d)
			continue
		}
		table := w.runTool(ps, []string{"-eo", "pid=,args="}).stdout
		var pids []string
		bin := d + "bin/Runner.Listener"
		for _, line := range strings.Split(table, "\n") {
			if f := strings.Fields(line); len(f) > 0 && strings.Contains(line, bin) {
				pids = append(pids, f[0])
			}
		}
		if len(pids) != 1 {
			w.drift("%s listeners=%d want=1 in %s", unit, len(pids), d)
		}
		// (1b) the listener descends from the systemd unit.
		for _, pid := range pids {
			if !w.underUnit(pid, unit) {
				w.drift("%s pid=%s not under %s", unit, pid, unit)
				w.strays = append(w.strays, pid)
			}
		}
		// (2) the unit file carries the standard stanzas.
		var unitFile string
		unitDirs := []string{filepath.Join(w.home, ".config", "systemd", "user")}
		if w.systemDir != "" {
			unitDirs = append(unitDirs, w.systemDir)
		}
		for _, d := range unitDirs {
			c := filepath.Join(d, unit)
			if w.isRegular(c) {
				unitFile = c
				break
			}
		}
		if unitFile == "" {
			w.drift("%s missing unit file %s", unit, unit)
			continue
		}
		body, _ := w.h.ReadFile(unitFile)
		text := string(body)
		if !strings.Contains(text, "Environment=PATH") || !strings.Contains(text, "go/bin") || !strings.Contains(text, ".local/bin") {
			w.drift("%s unit file PATH lacks go/bin and .local/bin in %s", unit, unitFile)
		}
		if !strings.Contains(text, "KillMode=control-group") {
			w.drift("%s unit file lacks KillMode=control-group in %s", unit, unitFile)
		}
		if !strings.Contains(text, "TimeoutStopSec=30s") {
			w.drift("%s unit file lacks TimeoutStopSec=30s in %s", unit, unitFile)
		}
	}
}

func (w *witness) isRegular(p string) bool {
	fi, err := w.h.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// underUnit reports whether the listener pid belongs to the systemd unit: its
// cgroup names the unit, or the unit's MainPID, asked of the user manager and
// then the system manager, is the pid.
func (w *witness) underUnit(pid, unit string) bool {
	if cg, ok := w.h.ProcCgroup(pid); ok && strings.Contains(cg, unit) {
		return true
	}
	systemctl, found := w.which("systemctl")
	if !found {
		return false
	}
	for _, args := range [][]string{
		{"--user", "show", unit, "-p", "MainPID"},
		{"show", unit, "-p", "MainPID"},
	} {
		out := trimNL(w.runTool(systemctl, args).stdout)
		_, main, _ := strings.Cut(out, "=")
		if main != "" && main != "0" && main == pid {
			return true
		}
	}
	return false
}

// killStrays is --apply: the stray listeners found above are sent SIGTERM, and
// that is the only thing the witness changes on a machine. Process 1 and a
// non-numeric id are never signalled.
func (w *witness) killStrays() {
	if !w.apply || len(w.strays) == 0 {
		return
	}
	for _, pid := range w.strays {
		n, err := strconv.Atoi(pid)
		if err != nil || n <= 1 {
			continue
		}
		if err := w.h.Kill(n); err != nil {
			fmt.Fprintf(w.out, "NOTE stray runner listener %d not killed: %v\n", n, err)
		}
	}
	fmt.Fprintf(w.out, "NOTE stray runner listeners killed: %s\n", strings.Join(w.strays, " "))
}

// checkToolchainRoots: every directory the wall grants a card for its toolchain
// exists, so a bench is missing one before a card finds out.
func (w *witness) checkToolchainRoots() {
	for _, root := range toolchainRoots {
		if !w.isDir(filepath.Join(w.home, root)) {
			w.drift("toolchain root %s missing; the sandbox wall grants this path and a card's go lives under it", filepath.Join(w.home, root))
		}
	}
}

// checkGoAndSbcl: go is the version the tree's go.mod names, and sbcl is on PATH.
func (w *witness) checkGoAndSbcl() {
	switch w.goWant {
	case "":
		w.drift("go.mod go directive unread; set NOVA_GO or run from a nova-tools checkout")
	default:
		if goPath, found := w.which("go"); found {
			out := trimNL(w.runTool(goPath, []string{"version"}).combined())
			if !strings.Contains(out, w.goWant) {
				w.drift("go version [%s] want %s", out, w.goWant)
			}
		} else {
			w.drift("go not on PATH want %s", w.goWant)
		}
	}
	if _, found := w.which("sbcl"); !found {
		w.drift("sbcl not on PATH")
	}
}

// checkWallExecutable: go and sbcl are runnable INSIDE the wall, not merely on
// PATH. A card runs behind the sandbox wall, whose execute roots are wallExecRoots;
// a tool on the bench user's PATH under $HOME/.local/bin is `Permission denied`
// there, which is how a bench passes a presence check and every card on it dies.
// An absent tool is checkGoAndSbcl's drift, not this one's.
func (w *witness) checkWallExecutable() {
	resolvDir := ""
	conf := orDefault(w.get("NOVA_RESOLV_CONF"), "/etc/resolv.conf")
	if r, err := w.h.EvalSymlinks(conf); err == nil && w.exists(r) {
		if d := filepath.Dir(r); d != "/" && d != "." && d != "" {
			resolvDir = d
		}
	}
	roots := wallExecRoots(w.home, resolvDir)
	for _, tool := range []string{"go", "sbcl"} {
		p, found := w.which(tool)
		if !found {
			continue
		}
		rp := w.realPath(p)
		granted := false
		for _, root := range roots {
			if rroot := w.realPath(root); rroot != "" && under(rp, rroot) {
				granted = true
			}
		}
		if !granted {
			w.drift("%s on PATH is %s -> %s, under NO read root the sandbox wall grants (the system roots of internal/sandbox/wrap_linux.go, the resolver directory, and $HOME/sdk from internal/swarm/toolchain.go): a card cannot EXECUTE it inside the wall. Install it under %s/sdk/%s-<ver>/ and point the PATH entry there",
				tool, p, rp, w.home, tool)
		}
	}
}

// sdkReal is $HOME/sdk with its links followed, so a tool reached through a
// link is judged by where it really lives.
func (w *witness) sdkReal() string { return w.realPath(filepath.Join(w.home, "sdk")) }

// checkSbclPin: the pinned sbcl, by whole version and by location under
// $HOME/sdk. A presence check passes a distribution sbcl from /usr/bin while the
// fleet pins its own. The version is `sbcl --version`'s second word, compared
// whole (2.5.80 is not 2.5.8).
func (w *witness) checkSbclPin() {
	p, found := w.which("sbcl")
	if !found {
		return
	}
	pin := orDefault(w.get("NOVA_SBCL"), defaultSBCL)
	first, _, _ := strings.Cut(trimNL(w.runTool(p, []string{"--version"}).combined()), "\n")
	var ver string
	if f := strings.Fields(first); len(f) > 1 {
		ver = f[1]
	}
	if ver != pin {
		w.drift("sbcl version [%s] want %s (NOVA_SBCL)", first, pin)
	}
	if rp := w.realPath(p); !under(rp, w.sdkReal()) {
		w.drift("sbcl at %s -> %s not under %s/sdk (want %s/sdk/sbcl-%s/)", p, rp, w.home, w.home, pin)
	}
}

// checkProRung: the bench carries the pro rung, so a pro wave has somewhere to
// run. NOVA_PRO_RUNG naming an executable, or either rung directory, is the rung.
func (w *witness) checkProRung() {
	if rung := w.get("NOVA_PRO_RUNG"); rung != "" && w.isExec(rung) {
		return
	}
	for _, rp := range []string{
		filepath.Join(w.home, "nova-bench", "rungs", "pro"),
		filepath.Join(w.home, "nova-bench", "pro"),
	} {
		if w.isDir(rp) {
			return
		}
	}
	w.drift("pro rung missing (no executable NOVA_PRO_RUNG, no %s/nova-bench/rungs/pro, no %s/nova-bench/pro)", w.home, w.home)
}

// checkSqlite: sqlite3 resolves under $HOME/sdk, so one card does not see a
// different sqlite3 on each bench.
func (w *witness) checkSqlite() {
	want := filepath.Join(w.home, "sdk", "sqlite3-<ver>", "bin", "sqlite3")
	p, found := w.which("sqlite3")
	if !found {
		w.drift("sqlite3 not on PATH (want %s)", want)
		return
	}
	if rp := w.realPath(p); !under(rp, w.sdkReal()) {
		w.drift("sqlite3 at %s -> %s not under %s/sdk (want %s)", p, rp, w.home, want)
	}
}

// checkSlotShare: the bench declares its share of the fleet's slots as a
// positive whole number, because the share is otherwise a fact recorded nowhere.
func (w *witness) checkSlotShare() {
	v := w.get("NOVA_SLOT_SHARE")
	switch {
	case v == "":
		w.drift("NOVA_SLOT_SHARE unset (declare the bench's slot share, a positive whole number of slots)")
	case strings.Trim(v, "0123456789") != "" || strings.HasPrefix(v, "0"):
		w.drift("NOVA_SLOT_SHARE=%s is not a positive whole number of slots", v)
	}
}

// checkHarness: the agent harness is on the bench. It returns whether it is, and
// the binary that proves it.
func (w *witness) checkHarness() (bool, string) {
	if w.harness != "" {
		if w.isExec(w.harness) {
			return true, w.harness
		}
		w.drift("harness missing at NOVA_HARNESS=%s", w.harness)
		return false, ""
	}
	if h := w.firstHarness(); h != "" {
		return true, h
	}
	w.drift("harness missing at %s/nova-bench/harness-<ver>/opencode", w.home)
	return false, ""
}

func (w *witness) firstHarness() string {
	for _, h := range w.glob(w.home, "nova-bench/harness-*/opencode") {
		if w.isExec(h) {
			return h
		}
	}
	return ""
}

// checkHarnessCanary starts the harness inside the sandbox wall. A harness the
// wall refuses means every card would fail at startup, so the bench is unfit.
func (w *witness) checkHarnessCanary(hbin string) {
	sbin := filepath.Join(w.home, ".local", "bin", "nova-sandbox")
	if !w.isExec(sbin) {
		return
	}
	cdir, err := w.h.MkdirTemp(filepath.Join(w.home, "nova-bench"), "nova-canary.*")
	if err != nil {
		w.drift("harness canary: cannot make a dir under %s/nova-bench: %v", w.home, err)
		return
	}
	defer func() { _ = w.h.RemoveUnder(filepath.Join(w.home, "nova-bench"), cdir) }() // ignored: the canary's own scratch dir, best-effort cleanup after the check
	if err := w.h.MkdirAll(filepath.Join(cdir, "home"), 0o755); err != nil {
		w.drift("harness canary: cannot make the wall's HOME under %s: %v", cdir, err)
		return
	}
	res := w.runTool(sbin, []string{"--read", filepath.Join(w.home, "nova-bench"), "--write", cdir, "--cwd", cdir, "--", hbin, "--help"}, "HOME="+filepath.Join(cdir, "home"))
	if !res.ok() {
		w.drift("harness cannot start inside the sandbox wall; %s --help failed under nova-sandbox", hbin)
	}
}

// checkSandboxNetwork runs the network probe inside the real sandbox and never on
// the host, so what it reports is what a card sees.
func (w *witness) checkSandboxNetwork() {
	url := orDefault(w.get("NOVA_PROBE_URL"), defaultProbeURL)
	dir, err := w.h.MkdirTemp(filepath.Join(w.home, "nova-bench"), "nova-probe.*")
	if err != nil {
		w.drift("sandbox-network: cannot make probe dir under %s/nova-bench: %v", w.home, err)
		return
	}
	defer func() { _ = w.h.RemoveUnder(filepath.Join(w.home, "nova-bench"), dir) }() // ignored: the probe's own scratch dir, best-effort cleanup after the check
	if err := w.h.MkdirAll(filepath.Join(dir, "home"), 0o755); err != nil {
		w.drift("sandbox-network: cannot make the wall's HOME under %s: %v", dir, err)
		return
	}
	sbin := filepath.Join(w.home, ".local", "bin", "nova-sandbox")
	if !w.isExec(sbin) {
		w.drift("sandbox-network: %s not executable", sbin)
		return
	}
	curl, _ := w.which("curl")
	if curl == "" {
		curl = "curl"
	}
	res := w.runTool(sbin, []string{"--read", filepath.Join(w.home, "nova-bench"), "--write", dir, "--cwd", dir, "--", curl, "-s", "-o", "/dev/null", "-w", "%{http_code}", url}, "HOME="+filepath.Join(dir, "home"))
	http := trimNL(res.stdout)
	// Only an all-digit reply is curl's http code. Anything else means the
	// sandbox did not run the command, which is the version check's to report; an
	// empty reply is a reachability failure and still drifts.
	switch {
	case http == "":
		w.drift("sandbox-network: curl inside nova-sandbox got http= (want 200)")
	case strings.Trim(http, "0123456789") != "":
	case http != "200":
		w.drift("sandbox-network: curl inside nova-sandbox got http=%s (want 200)", http)
	}
}

// checkBins: each nova binary on the bench reports the wanted version.
func (w *witness) checkBins() {
	if w.want == "" {
		w.drift("NOVA_WANT unset (set NOVA_WANT to the wanted version)")
		return
	}
	for _, name := range novaBins {
		bin := filepath.Join(w.home, ".local", "bin", name)
		if !w.isExec(bin) {
			w.drift("%s missing at %s", name, bin)
			continue
		}
		res := w.runTool(bin, []string{"version"})
		out := trimNL(res.combined())
		if !res.ok() {
			out = trimNL(w.runTool(bin, []string{"--version"}).combined())
		}
		if !strings.Contains(out, w.want) {
			w.drift("%s version [%s] want %s", name, out, w.want)
		}
	}
}

// checkSeat: exactly one seat key, and at most one per owner prefix, and the
// secrets tool accepts it. The owner is the key name before its first "-":
// ada-claude and ada-codex are both owner ada. Two keys for one owner is a
// lost key still trusted or a grant nobody declared, named by owner.
func (w *witness) checkSeat() {
	seatdir := filepath.Join(w.home, ".config", "nova-secrets")
	var keys []string
	if w.isDir(seatdir) {
		for _, k := range w.glob(seatdir, "*.key") {
			if w.exists(k) {
				keys = append(keys, k)
			}
		}
	}
	if len(keys) > 1 {
		perOwner := map[string]int{}
		for _, k := range keys {
			name := strings.TrimSuffix(filepath.Base(k), ".key")
			owner, _, _ := strings.Cut(name, "-")
			perOwner[owner]++
		}
		for _, o := range slices.Sorted(maps.Keys(perOwner)) {
			if o != "" && perOwner[o] > 1 {
				w.drift("seat owner=%s keys=%d want=1 in %s", o, perOwner[o], seatdir)
			}
		}
	}
	if len(keys) != 1 {
		w.drift("seat keys=%d want=1 in %s", len(keys), seatdir)
		return
	}
	seatkey := keys[0]
	secrets, found := w.which("nova-secrets")
	if !found {
		w.drift("nova-secrets not on PATH for seat check of %s", seatkey)
		return
	}
	seat := strings.TrimSuffix(filepath.Base(seatkey), ".key")
	store := w.get("NOVA_SECRETS_STORE")
	if store == "" && w.isDir(filepath.Join(w.home, "nova-bench", "secrets")) {
		store = filepath.Join(w.home, "nova-bench", "secrets")
	}
	if store == "" && w.isDir(filepath.Join(w.home, "secrets")) {
		store = filepath.Join(w.home, "secrets")
	}
	sops, haveSops := w.which("sops")
	if store != "" && w.isRegular(filepath.Join(store, seat+".yaml")) && haveSops {
		if !w.runTool(secrets, []string{"check", "--store", store, "--as", seat, "--key", seatkey, "--sops", sops}).ok() {
			w.drift("seat nova-secrets check failed for %s", seatkey)
		}
		return
	}
	// A store-backed check is the real gate where a store exists; the key-only
	// probe keeps a bench without a local store checkout honest without inventing
	// store paths.
	if !w.runTool(secrets, []string{"check", "--key", seatkey}).ok() {
		if !w.runTool(secrets, []string{"check", "--store", store, "--as", seat, "--key", seatkey}).ok() {
			w.drift("seat nova-secrets check failed for %s", seatkey)
		}
	}
}

// checkPlaintextKeys: no provider key sits in a plaintext file on the bench.
func (w *witness) checkPlaintextKeys() {
	for _, p := range []string{
		filepath.Join(w.home, ".local", "share", "opencode", "auth.json"),
		filepath.Join(w.home, ".config", "deepseek", "env"),
	} {
		if w.exists(p) {
			w.drift("plaintext key file %s present", p)
		}
	}
	dir := filepath.Join(w.home, ".config", "opencode")
	if !w.isDir(dir) {
		return
	}
	for _, f := range w.glob(dir, "*.json") {
		if body, err := w.h.ReadFile(f); err == nil && strings.Contains(string(body), `apiKey": "sk-`) {
			w.drift("plaintext apiKey in %s", f)
		}
	}
}

// checkDisk: the bench has headroom, and when it does not the finding names the
// three largest directories under $HOME so nobody has to go and look.
//
// A Go card costs 5-7 GB of module cache, build cache and scratch in its own
// slot, and a bench below the floor is one the launchers refuse to start cards
// on, so it is not a conforming bench whatever else it passes. The floor is the
// launchers' own 25 GB. `df -Pk` is asked because POSIX defines its columns and a
// BSD userland refuses the GNU-only `df -BG`; the walk of a home runs only on the
// failing path, since a green run does not pay for it.
func (w *witness) checkDisk() {
	minStr := orDefault(w.get("NOVA_MIN_FREE_G"), strconv.Itoa(defaultMinFreeG))
	minFree, merr := strconv.Atoi(minStr)
	if merr != nil {
		w.drift("NOVA_MIN_FREE_G=%s is not a whole number of gigabytes", minStr)
	}
	kb, ok := w.freeKB()
	if !ok {
		w.drift("disk free unknown: df answered nothing readable for %s; a bench whose free disk cannot be read is not known to be conforming", w.home)
		return
	}
	w.freeG = strconv.FormatInt(kb/1048576, 10)
	if merr == nil && kb/1048576 < int64(minFree) {
		largest := w.largest(3)
		if largest == "" {
			largest = "nothing readable under " + w.home
		}
		w.drift("disk free=%sG want>=%dG (both launchers refuse below it); largest under %s: %s", w.freeG, minFree, w.home, largest)
	}
}

// freeKB is the Available column of `df -Pk $HOME`'s one data row.
func (w *witness) freeKB() (int64, bool) {
	df, found := w.which("df")
	if !found {
		return 0, false
	}
	lines := strings.Split(w.runTool(df, []string{"-Pk", w.home}).stdout, "\n")
	if len(lines) < 2 {
		return 0, false
	}
	f := strings.Fields(lines[1])
	if len(f) < 4 {
		return 0, false
	}
	n, err := strconv.ParseInt(f[3], 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// largest names the n largest entries directly under $HOME as
// `path 12G; path 340M`, sized by `du -sk`.
func (w *witness) largest(n int) string {
	du, found := w.which("du")
	if !found {
		return ""
	}
	entries := w.glob(w.home, "*")
	type sized struct {
		kb   int64
		path string
	}
	var all []sized
	const chunk = 200
	for i := 0; i < len(entries); i += chunk {
		end := min(i+chunk, len(entries))
		res := w.runTool(du, append([]string{"-sk"}, entries[i:end]...))
		for _, line := range strings.Split(res.stdout, "\n") {
			kbs, path, ok := strings.Cut(line, "\t")
			if !ok {
				continue
			}
			kb, err := strconv.ParseInt(strings.TrimSpace(kbs), 10, 64)
			if err != nil {
				continue
			}
			all = append(all, sized{kb, strings.TrimLeft(path, " \t")})
		}
	}
	sort.Slice(all, func(a, b int) bool {
		if all[a].kb != all[b].kb {
			return all[a].kb > all[b].kb
		}
		return all[a].path > all[b].path
	})
	if len(all) > n {
		all = all[:n]
	}
	parts := make([]string, 0, len(all))
	for _, s := range all {
		v, unit := s.kb, "K"
		switch {
		case s.kb >= 1048576:
			v, unit = s.kb/1048576, "G"
		case s.kb >= 1024:
			v, unit = s.kb/1024, "M"
		}
		parts = append(parts, fmt.Sprintf("%s %d%s", s.path, v, unit))
	}
	return strings.Join(parts, "; ")
}

// orDefault is ${VAR:-def}.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
