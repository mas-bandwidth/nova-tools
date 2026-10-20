package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The native Windows child's environment (winenv.go), built for goos "windows" from an
// environment the test writes, so it runs on every OS: no process, no file, no time.

// windowsEnviron is a member's environment as Windows hands it over: `Path`, not PATH.
var windowsEnviron = []string{
	`Path=C:\nova-swarm\bin;C:\Windows\system32;C:\Program Files\Git\cmd;C:\Program Files\CMake\bin`,
	`SystemRoot=C:\WINDOWS`, `ComSpec=C:\WINDOWS\system32\cmd.exe`, `PATHEXT=.COM;.EXE;.BAT;.CMD`,
	`ProgramFiles=C:\Program Files`, `ProgramFiles(x86)=C:\Program Files (x86)`, `ProgramData=C:\ProgramData`,
	`NUMBER_OF_PROCESSORS=64`, `PROCESSOR_ARCHITECTURE=AMD64`, `OS=Windows_NT`,
	`USERPROFILE=C:\Users\owner`, `APPDATA=C:\Users\owner\AppData\Roaming`, `LOCALAPPDATA=C:\Users\owner\AppData\Local`,
	`TEMP=C:\Users\owner\AppData\Local\Temp`, `Tmp=C:\Users\owner\AppData\Local\Temp`,
	`OneDrive=C:\Users\owner\OneDrive`, `DEEPSEEK_API_KEY=k`, `GH_PUSH_TOKEN=t`, `GOPATH=C:\Users\owner\go`,
}

func winEnvValue(t *testing.T, env []string, name string) (string, int) {
	t.Helper()
	var val string
	n := 0
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if strings.EqualFold(k, name) {
			val, n = v, n+1
		}
	}
	return val, n
}

// TestTheMemberHandsANativeWindowsChildItsPathAndWindowsOwnNames is captain's refusal of
// 2026-10-19: the member kept "PATH" by exact name, Windows spells it `Path`, and native
// started with no PATH, so sqlite3.exe in C:\nova-swarm\bin was "on no PATH entry".
func TestTheMemberHandsANativeWindowsChildItsPathAndWindowsOwnNames(t *testing.T) {
	t.Parallel()

	got := childEnvironFor("windows", windowsEnviron, []string{"DEEPSEEK_API_KEY"})
	p, n := winEnvValue(t, got, "PATH")
	assert.Equal(t, 1, n)
	assert.True(t, strings.HasPrefix(p, `C:\nova-swarm\bin;`), "the member's own bin, sqlite3's, reaches native: %q", p)
	for _, name := range []string{"SystemRoot", "ComSpec", "PATHEXT", "ProgramFiles", "ProgramFiles(x86)", "ProgramData",
		"NUMBER_OF_PROCESSORS", "OS", "USERPROFILE", "LOCALAPPDATA", "TEMP", "Tmp", "GOPATH", "DEEPSEEK_API_KEY"} {
		_, n := winEnvValue(t, got, name)
		assert.Equal(t, 1, n, "%s is handed to native", name)
	}
	for _, name := range []string{"OneDrive", "GH_PUSH_TOKEN"} {
		_, n := winEnvValue(t, got, name)
		assert.Zero(t, n, "%s is not handed to native", name)
	}
	// the pass list is read without case on Windows too
	_, n = winEnvValue(t, childEnvironFor("windows", windowsEnviron, []string{"deepseek_api_key"}), "DEEPSEEK_API_KEY")
	assert.Equal(t, 1, n)
	// and every other OS is exactly as it was: there `Path` is not PATH, and Windows' names are noise
	linux := childEnvironFor("linux", windowsEnviron, nil)
	for _, name := range []string{"Path", "SystemRoot", "USERPROFILE", "Tmp"} {
		_, n := winEnvValue(t, linux, name)
		assert.Zero(t, n, "linux keeps %s", name)
	}
}

// TestANativeWindowsCardFindsTheToolchainAndKeepsItsProfileInTheDataHome: the card's child
// gets the bench toolchain first and the member's PATH after it, in ONE PATH entry joined by
// `;`, Windows' own names, and a profile that is the data home, never the member user's.
func TestANativeWindowsCardFindsTheToolchainAndKeepsItsProfileInTheDataHome(t *testing.T) {
	t.Parallel()

	data := `C:\nova-swarm\run\m\slots\c\data`
	tool := []string{`C:\Users\owner\sdk\bin`, `C:\Program Files\Git\cmd`, `C:\Program Files\Go\bin`}
	env := nativeChildEnvOS("windows", windowsEnviron, data, `C:\job`, `C:\tmp\c`, "", "", "", "", tool)
	p, n := winEnvValue(t, env, "PATH")
	assert.Equal(t, 1, n, "one PATH entry, whatever spelling it came in: %v", env)
	assert.Equal(t, strings.Join(tool, ";")+`;C:\nova-swarm\bin;C:\Windows\system32;C:\Program Files\Git\cmd;C:\Program Files\CMake\bin`, p)
	for name, want := range map[string]string{
		"SystemRoot": `C:\WINDOWS`, "PATHEXT": `.COM;.EXE;.BAT;.CMD`, "ProgramFiles(x86)": `C:\Program Files (x86)`,
		"USERPROFILE": data, "APPDATA": data + `\AppData\Roaming`, "LOCALAPPDATA": data + `\AppData\Local`,
		"HOME": data, "TEMP": `C:\tmp\c`, "TMP": `C:\tmp\c`,
	} {
		v, n := winEnvValue(t, env, name)
		assert.Equal(t, 1, n, "%s once: %v", name, env)
		assert.Equal(t, want, v, name)
	}
	_, n = winEnvValue(t, env, "OneDrive")
	assert.Zero(t, n, "the member user's noise stays out")
	// linux: unchanged, no profile names, `Path` is not PATH
	lx := nativeChildEnvOS("linux", windowsEnviron, "/d", "/j", "/t", "", "", "", "", []string{"/sdk/bin"})
	p, n = winEnvValue(t, lx, "PATH")
	assert.Equal(t, 1, n)
	assert.Equal(t, "/sdk/bin", p, "linux: the toolchain alone, as before, since this environment has no PATH")
	_, n = winEnvValue(t, lx, "USERPROFILE")
	assert.Zero(t, n)
}

func TestPathWithDirsFirstForWindowsFoldsTheSpellings(t *testing.T) {
	t.Parallel()

	got := pathWithDirsFirstFor("windows", []string{`Path=C:\a`, `PATH=C:\dup`, `X=1`}, `C:\t`, "")
	assert.Equal(t, []string{`PATH=C:\t;C:\a`, `X=1`}, got)
	assert.Equal(t, []string{`Path=C:\a`, `PATH=/t`}, pathWithDirsFirstFor("linux", []string{`Path=C:\a`}, "/t"))
}

func TestWithEnvSetReplacesByName(t *testing.T) {
	t.Parallel()

	got := withEnvSet([]string{"A=1", "INCLUDE=old", "B=2"}, []string{"INCLUDE=new", "LIB=x"})
	assert.Equal(t, []string{"A=1", "B=2", "INCLUDE=new", "LIB=x"}, got)
}
