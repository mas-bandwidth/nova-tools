package main

import "strings"

// THE ENVIRONMENT OF A NATIVE WINDOWS CHILD (captain, 2026-10-19). Windows reads environment
// names without case and its process environment spells PATH `Path`, so the member's and
// native's allowlists, which matched "PATH" exactly, dropped it: every `nova-swarm native` on
// captain started with no PATH, refused each card with "sqlite3 is on no PATH entry of this
// bench" beside the sqlite3.exe in the member's own bin, and would have handed a card a PATH of
// the Go bin alone. The same lists dropped the names Windows itself needs to run a program --
// SystemRoot (Winsock and the crypto providers load from it), ComSpec, PATHEXT, the Program
// Files locations a build tool finds Visual Studio and the SDK through. On Windows a name is
// now matched without case and those names are kept. Every other OS is exactly as it was.

// envName is a name as the allowlists compare it: upper case on Windows, as it is otherwise.
func envName(goos, name string) string {
	if goos == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

// envNameIs says whether name is want on goos: without case on Windows, exactly elsewhere.
func envNameIs(goos, name, want string) bool {
	if goos == "windows" {
		return strings.EqualFold(name, want)
	}
	return name == want
}

// windowsSystemEnv are the names (upper case) a Windows program needs to start and to find
// the machine's own installs. Kept for the member's native child and for the card's child.
// None names a credential.
var windowsSystemEnv = map[string]bool{
	"SYSTEMROOT": true, "SYSTEMDRIVE": true, "WINDIR": true, "COMSPEC": true, "PATHEXT": true,
	"PROGRAMFILES": true, "PROGRAMFILES(X86)": true, "PROGRAMW6432": true,
	"COMMONPROGRAMFILES": true, "COMMONPROGRAMFILES(X86)": true, "COMMONPROGRAMW6432": true,
	"PROGRAMDATA": true, "ALLUSERSPROFILE": true, "PUBLIC": true, "DRIVERDATA": true,
	"NUMBER_OF_PROCESSORS": true, "PROCESSOR_ARCHITECTURE": true, "PROCESSOR_IDENTIFIER": true,
	"PROCESSOR_LEVEL": true, "PROCESSOR_REVISION": true, "OS": true,
	"COMPUTERNAME": true, "USERNAME": true, "USERDOMAIN": true,
}

// windowsMemberEnv are the per-user names (upper case) the member hands its native child,
// which is the member's own tool and runs as the member's user: the profile and the
// temporary directory. Native does NOT hand these to the card's child; it points the profile
// names into the data home (windowsChildProfile) and sets TMP and TEMP itself.
var windowsMemberEnv = map[string]bool{
	"USERPROFILE": true, "APPDATA": true, "LOCALAPPDATA": true, "HOMEDRIVE": true, "HOMEPATH": true,
	"TEMP": true, "TMP": true,
}

// windowsChildProfile is the Windows face of HOME=<data home>: a Windows program finds its
// home through USERPROFILE (Go's os.UserHomeDir, Node's os.homedir) and its per-user files
// through APPDATA and LOCALAPPDATA (MSBuild, NuGet, CMake's user registry), so all three name
// the data home and the job's state stays in it, never in the member user's own profile.
func windowsChildProfile(dataHome string) []string {
	d := windowsChildProfileDirs(dataHome)
	return []string{"USERPROFILE=" + dataHome, "APPDATA=" + d[0], "LOCALAPPDATA=" + d[1]}
}

// windowsChildProfileDirs are the roaming and local folders windowsChildProfile names.
func windowsChildProfileDirs(dataHome string) []string {
	base := strings.TrimRight(dataHome, `\/`) + `\AppData\`
	return []string{base + "Roaming", base + "Local"}
}

// withEnvSet is env with each NAME=VALUE of set in place of any entry by that name, so each
// is there exactly once and set's value wins.
func withEnvSet(env, set []string) []string {
	for _, kv := range set {
		name, _, _ := strings.Cut(kv, "=")
		env = append(environWithoutName(env, name), kv)
	}
	return env
}
