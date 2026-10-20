package swarm

import (
	"bufio"
	"bytes"
	"strings"
)

// THE MSVC DEVELOPER ENVIRONMENT OF A NATIVE WINDOWS CARD (captain, 2026-10-19). A card on a
// native Windows member builds with Visual Studio's compiler, and Visual Studio puts nothing
// on any PATH: `cl`, `link` and the Windows SDK's tools are findable, and the compiler finds
// its headers and libraries, only inside the environment `vcvars64.bat` makes (PATH, INCLUDE,
// LIB, LIBPATH and the VS/SDK location names). The rocketnet Windows CI reaches MSVC through
// CMake's Visual Studio generator (`cmake -B build -A x64`), which needs none of it; a card
// that runs `cl` by name, or a Ninja or NMake generator, needs all of it. So native, on
// Windows, asks vswhere.exe (at its documented fixed location, the toolchain root
// `C:/Program Files (x86)/Microsoft Visual Studio/Installer`) for the newest install with the
// x64 C++ tools, runs that install's vcvars64.bat once per launch and reads back `set`, and
// hands the child what vcvars added: the new PATH directories go after the bench toolchain,
// the named variables are set. The programs run in cmd/nova-swarm (msvc_windows.go); what is
// here reads their output and runs nothing, so it is tested on every OS.

// MSVCEnv is what vcvars64.bat adds to an environment: the PATH directories it put in front of
// the PATH it was run with, in its order, and the variables a compiler and a build reads, as
// NAME=VALUE.
type MSVCEnv struct {
	PathDirs []string
	Vars     []string
}

// MSVCVswhereArgs is the vswhere query: the newest install, of any product (Build Tools
// included), that has the x64 C++ toolset, answering with its installation path alone.
var MSVCVswhereArgs = []string{"-latest", "-products", "*", "-requires", "Microsoft.VisualStudio.Component.VC.Tools.x86.x64", "-property", "installationPath", "-nologo"}

// msvcVarNames are the variables vcvars64.bat sets that a compile, a link or a build tool
// reads. Matched without case: cmd prints the spelling vcvars chose.
var msvcVarNames = []string{
	"INCLUDE", "LIB", "LIBPATH", "EXTERNAL_INCLUDE",
	"VSINSTALLDIR", "VCINSTALLDIR", "VCIDEInstallDir", "VCToolsInstallDir", "VCToolsRedistDir", "VCToolsVersion",
	"VisualStudioVersion", "DevEnvDir", "Platform",
	"VSCMD_ARG_HOST_ARCH", "VSCMD_ARG_TGT_ARCH", "VSCMD_ARG_app_plat", "VSCMD_VER",
	"WindowsSdkDir", "WindowsSDKVersion", "WindowsSDKLibVersion", "WindowsSdkBinPath", "WindowsSdkVerBinPath", "WindowsLibPath",
	"UniversalCRTSdkDir", "UCRTVersion", "ExtensionSdkDir",
	"FrameworkDir", "FrameworkDir64", "FrameworkVersion", "FrameworkVersion64",
}

// MSVCInstall is the installation path vswhere answered with: its first non-empty line, or
// false when it answered with none (no Visual Studio with the C++ tools on this machine).
func MSVCInstall(vswhereOut []byte) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(vswhereOut))
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			return line, true
		}
	}
	return "", false
}

// MSVCVcvars is the x64 developer-environment script of one install, a Windows path.
func MSVCVcvars(install string) string {
	return strings.TrimRight(install, `\/`) + `\VC\Auxiliary\Build\vcvars64.bat`
}

// MSVCVcvarsCmdLine is the cmd.exe command line that runs vcvars and prints the environment
// it leaves: `/s /c` with the whole command in one pair of quotes, so a script path with
// spaces is quoted inside it (cmd's own rule; the program's argv quoting cannot express it).
// /d skips the machine's AutoRun, so nothing but vcvars changes what `set` prints.
func MSVCVcvarsCmdLine(cmdExe, vcvars string) string {
	return `"` + cmdExe + `" /d /s /c ""` + vcvars + `" >nul && set"`
}

// MSVCEnvFrom reads `set` as it is printed after vcvars ran, against the PATH that vcvars
// was run with: every directory of the new PATH that the old one does not hold (Windows paths
// compare without case and without a trailing separator), in order and once, and every
// variable msvcVarNames names that has a value. A `set` that printed no PATH adds none.
func MSVCEnvFrom(setOut []byte, basePath string) MSVCEnv {
	old := map[string]bool{}
	for _, d := range strings.Split(basePath, ";") {
		old[winPathKey(d)] = true
	}
	want := map[string]bool{}
	for _, n := range msvcVarNames {
		want[strings.ToUpper(n)] = true
	}
	var env MSVCEnv
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(setOut))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		name, val, ok := strings.Cut(strings.TrimRight(sc.Text(), "\r"), "=")
		if !ok || name == "" || val == "" {
			continue
		}
		up := strings.ToUpper(name)
		switch {
		case up == "PATH":
			for _, d := range strings.Split(val, ";") {
				k := winPathKey(d)
				if k == "" || old[k] || seen[k] {
					continue
				}
				seen[k] = true
				env.PathDirs = append(env.PathDirs, strings.TrimSpace(d))
			}
		case want[up]:
			env.Vars = append(env.Vars, name+"="+val)
		}
	}
	return env
}

// winPathKey is one PATH entry as Windows compares it: no surrounding blanks, no trailing
// separator, no case.
func winPathKey(d string) string {
	d = strings.TrimSpace(d)
	if len(d) > 3 {
		d = strings.TrimRight(d, `\/`)
	}
	return strings.ToLower(d)
}
