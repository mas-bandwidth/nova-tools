RESULT tools22-rule-sandbox-3-L1612 sha=5298f6be12ea — does the code at this base do what docs/SPEC-SANDBOX.md rule 3 says?
CONFORMS internal/sandbox/landlock_linux.go:133
SPEC docs/SPEC-SANDBOX.md:1612 rule 3
PKG internal/sandbox
ASK The linux Landlock backend must keep LANDLOCK_ACCESS_FS_IOCTL_DEV out of the ruleset's handled-access set below ABI 5 (so ioctl on a device file is unrestricted there, as the rule states) and include it once the kernel defines it at ABI 5, and the SANDBOX OK line must print the kernel's discovered ABI on abi= — with used= beside it only when the kernel is newer than the table — so a reader knows which machine and which wall.

Deciding lines:
internal/sandbox/landlock_linux.go:53:	fsIoctlDev   = 1 << 15 // ABI 5 (6.10)
internal/sandbox/landlock_linux.go:133:	if abi >= 5 {
internal/sandbox/landlock_linux.go:134:		fs |= fsIoctlDev
internal/sandbox/landlock_linux.go:125:	func handledFS(abi int) uint64 {  // "the whole set the discovered ABI defines" — masked DOWN to the ABI
internal/sandbox/landlock_linux.go:141:	func writeSubset(abi int) uint64 { return handledFS(abi) }  // --write gets IOCTL_DEV only at ABI >= 5
internal/sandbox/landlock_linux.go:145:	func fileWriteSubset(abi int) uint64 { return fsFileSubset & handledFS(abi) }  // /dev/null, /dev/tty
internal/sandbox/wrap_linux.go:120-126:	ABI() returns the DISCOVERED kernel landlock version, printed as abi= on SANDBOX OK
internal/sandbox/wrap_linux.go:131-137:	ClampedABI() returns wallABI(abi): (maxKnownABI, true) only when the kernel is above the table
cmd/nova-sandbox/main.go:372-373:	used = " used=" + ... only when ClampedABI() says clamped; SANDBOX NOTE says "clamped"
cmd/nova-sandbox/main.go:384-385:	SANDBOX OK backend=landlock abi=<kernel> [used=<wall>] ...

UNGUARDED — the core of the rule, the `abi >= 5` guard in handledFS (landlock_linux.go:133-135), has no test: no *_test.go anywhere references fsIoctlDev, handledFS, writeSubset or fileWriteSubset. A mutation that dropped the guard (ioctl bit handled below ABI 5, refused by the abi-4 bench kernel) or that deleted the bit would turn no test red on this platform. The abi=/used= DISCLOSURE side is guarded: internal/sandbox/wrap_linux_test.go:38 TestNewerLandlockABIIsClampedToTheTableOnLinux, internal/sandbox/wrap_linux_test.go:75 TestWallABIClampsOnlyAboveTheTable, and cmd/nova-sandbox/wall_linux_test.go:141 TestLandlockWallClampsAnABIAboveTheTable (the last runs the real tool on whatever kernel it finds). The rule is stated honestly: below ABI 5 the bit is not handled, so ioctl on an open device is unchecked — which is what rule 3 says, and the fleet bench at abi 4 gets exactly that.

Greps ran:
grep -n "ioctl\|IOCTL\|abi\|ABI" docs/SPEC-SANDBOX.md
grep -rn "used=\|abi=\|ClampedABI\|ABI()" --include='*.go' cmd/ internal/sandbox/
grep -rn "func Test" --include='*_test.go' internal/sandbox/ | grep -i "ioctl\|abi\|clamp\|landlock"
grep -rn "Ioctl\|ioctl\|IOCTL\|handledFS\|fsIoctlDev\|writeSubset" --include='*_test.go' .   # no output
grep -rn "handledFS\|fsIoctlDev\|fsFileSubset\|writeSubset\|fileWriteSubset" --include='*.go' .
grep -rn "func Test" cmd/nova-sandbox/*_test.go | grep -i "clamp\|landlock\|abi\|wall"

Files read: docs/SPEC-SANDBOX.md (1440-1620), internal/sandbox/landlock_linux.go (full), internal/sandbox/wrap_linux.go (full), internal/sandbox/wrap_linux_test.go (full), cmd/nova-sandbox/main.go:355-395, cmd/nova-sandbox/run.go:740-760, cmd/nova-sandbox/wall_linux_test.go:90-189.

Left owed: none. This card changes nothing; no branch, no commit, no push.

git status --short (must print nothing):