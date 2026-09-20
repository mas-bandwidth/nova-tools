RESULT tools22-rule-sandbox-3-L2256 sha=5298f6be12eaa0f7e6622334d2b6a1eb427649e3 — does the code at this base do what docs/SPEC-SANDBOX.md rule 3 says?
CONFORMS internal/sandbox/landlock_linux.go:105
SPEC docs/SPEC-SANDBOX.md:2256 rule 3
PKG internal/sandbox

ASK: The implementation must build read-only access for system roots and --read paths, full read-write access for --write paths, deny sensitive files in the caller's original HOME (blocked because HOME is redirected to a data directory inside --write that has no traversal back to the real home), grant darwin symlinks on /etc /tmp /var as literals, and on linux allow child processes to read /proc/self/status.

Deciding lines:

internal/sandbox/landlock_linux.go:105
  const fsReadSubset = fsExecute | fsReadFile | fsReadDir
  // roots and --read get EXECUTE|READ_FILE|READ_DIR

internal/sandbox/wrap_linux.go:290-334
  func addRules(rulesetFd int, p *Policy, abi int) error {
      read := uint64(fsReadSubset)
      write := writeSubset(abi)
      // Roots, read-only
      for _, root := range linuxRoots() { ... }
      // --read paths
      for _, dir := range p.Reads { ... }
      // --write paths get full handled set
      for _, dir := range writePaths(p) { ... }

internal/sandbox/landlock_linux.go:51
  var linuxReadRoots = []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/run/systemd/resolve", "/opt", "/dev", "/proc"}
  // Linux does NOT list /home or any user-home path, so ~/.ssh/id_test etc. are never readable inside the wall.
  // /proc (not /proc/self) allows children to read /proc/self/status

profiles/darwin.sb.tmpl:101
  (allow file-read* (literal "/") (literal "/etc") (literal "/tmp") (literal "/var"))
  // Symlink targets reachable via /etc, /tmp, /var grants cat /etc/hosts and sh -c true
profiles/darwin.sb.tmpl:108-109
  (allow file-read* (subpath "/private/etc"))
  (allow file-read* (subpath "/private/var/select"))
profiles/darwin.sb.tmpl:123
  (allow file-read* file-write* (subpath (param "HOME")))
  // HOME param = data home inside --write; original ~ isn't reachable

internal/sandbox/policy.go:707-725 (ChildEnv)
  // Redirects $HOME to p.Home (data home), removes temp vars, scrubs agent socks
  // Inside the wall, HOME points to the write-set path — the original user home at ~/.../.ssh is unreachable

GUARDED-BY cmd/nova-sandbox/main.go:574 step{"read_root", p.Command, "allow"} TestProbeReExecsTheToolAndNeverAShell reads os.Executable() under a root
cmd/nova-sandbox/main.go:869-880 probeStepVerb case "read_root": opens and reads one byte of path, returns exit 0 on success

Also tested (indirectly):
- profiles/darwin-check.sh: expect_ok cat_etc_hosts (darwin /etc literal grant)
- profiles/darwin-check.sh: expect_ok sh_c_true (darwin sh -c true exits 0)
- profiles/darwin-check.sh: expect_deny read_secret (secret outside wall is denied)
- cmd/nova-sandbox/main_test.go: TestProbeReExecsTheToolAndNeverAShell (line 669) verifies read_root works on resolved command path

UNGUARDED findings (rule 3 assertions with no dedicated test):
- Reading a file directly from a --read path (probeVerb tests root, not --read explicitly)
- Writing under a --read path fails (no test asserts this denial)
- Sensitive files ($HOME/.ssh/id_test, .config/gh/hosts.yml, Library/Keychains/probe.db, .zsh_history) unreadable inside the wall — currently relies on the implicit fact that these paths are outside every granted root
- Darwin /etc /tmp /var symlink assertions asserted only by the shell script which is invoked by a Go test but no Go-level unit test pins them
- Linux /proc/self/status child read assertion — linuxReadRoots lists /proc (not /proc/self), which correctly enables child /proc/self/status reads, but no test executes a child that reads /proc/self/status

Grep commands run:
  grep -rn ".ssh|id_test|hosts\.yml|Keychains|zsh_history|/etc/hosts|/proc/self/status|private/var/select" --include='*.go' internal/sandbox/
  grep -rn "rule 3\|TestRule\|TestReadWrite\|TestSensitive\|read.*root\|sensitive.*file" --include='*.go' cmd/nova-sandbox/ internal/sandbox/
  grep -rn "\.ssh|\.config/gh|Keychains|\.zsh_history" --include='*.go' internal/sandbox/
  ls internal/sandbox/

Left owed

git status --short
