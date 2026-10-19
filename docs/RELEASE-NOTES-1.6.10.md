The stable tools point release after v1.6.9. A card on a native Windows member now gets a working environment. nova-sprint is unchanged.

Install: download the assets for your platform from this release, then run `nova-update release install --from <release root> --version v1.6.10 --bin <dir>` with the nova-sprint release staged beside them (see docs/SPEC-RELEASE.md). Only nova-swarm changed.

## What changed in v1.6.10

- On captain, every card was refused with "sqlite3 is on no PATH entry of this bench", even though sqlite3.exe was in the member's own bin. The cause: Windows spells PATH as `Path`, and the member's and native's environment allowlists matched names exactly. So native started with no PATH, and a card got Go's bin and nothing else. On Windows the names now match without regard to case.
- The names Windows needs to run a program are kept on Windows: SystemRoot, SystemDrive, windir, ComSpec, PATHEXT, the Program Files and ProgramData locations, and the processor and OS names.
- A card's USERPROFILE, APPDATA and LOCALAPPDATA now point into its data home, the same way HOME does.
- `toolchainRoots` has a `windows` list: `~/sdk`, `~/go/pkg/mod`, plus Git (`cmd`, `bin`), CMake, Go and the Visual Studio Installer (vswhere) under Program Files. The new `PathDirs` field names the directories of each tree that a card searches.
- On Windows, native runs vswhere and then that install's `vcvars64.bat` once per launch. The child gets what vcvars adds: `cl`, `link` and the Windows SDK on PATH, and INCLUDE, LIB and LIBPATH set.
- Linux and macOS behave as before. Unit tests cover the windows environment on every OS. On captain a card's environment found sqlite3, git, cmake, cl, link and Git's bash, and cl compiled and ran a C program.
