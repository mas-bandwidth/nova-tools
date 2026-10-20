package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The MSVC developer environment is read from vswhere's and cmd's output; these tests hand
// that output in as bytes, so no program runs and no Visual Studio is needed.

func TestMSVCInstallIsVswheresFirstLine(t *testing.T) {
	t.Parallel()

	got, ok := MSVCInstall([]byte("\r\nC:\\Program Files\\Microsoft Visual Studio\\18\\Community\r\nC:\\other\r\n"))
	assert.True(t, ok)
	assert.Equal(t, `C:\Program Files\Microsoft Visual Studio\18\Community`, got)
	_, ok = MSVCInstall([]byte(" \r\n"))
	assert.False(t, ok, "vswhere answering with nothing is no install")
}

func TestMSVCVcvarsCmdLineQuotesThePathInsideCmdsOwnQuotes(t *testing.T) {
	t.Parallel()

	bat := MSVCVcvars(`C:\Program Files\Microsoft Visual Studio\18\Community\`)
	assert.Equal(t, `C:\Program Files\Microsoft Visual Studio\18\Community\VC\Auxiliary\Build\vcvars64.bat`, bat)
	assert.Equal(t, `"C:\Windows\System32\cmd.exe" /d /s /c ""`+bat+`" >nul && set"`, MSVCVcvarsCmdLine(`C:\Windows\System32\cmd.exe`, bat))
}

func TestMSVCEnvFromKeepsWhatVcvarsAdded(t *testing.T) {
	t.Parallel()

	base := `C:\nova-swarm\bin;C:\Windows\system32;C:\Program Files\Git\cmd\`
	set := "ALLUSERSPROFILE=C:\\ProgramData\r\n" +
		"INCLUDE=C:\\VS\\VC\\Tools\\MSVC\\14.50\\include;C:\\Kits\\10\\include\\ucrt\r\n" +
		"LIB=C:\\VS\\VC\\Tools\\MSVC\\14.50\\lib\\x64\r\n" +
		"LIBPATH=C:\\VS\\VC\\Tools\\MSVC\\14.50\\lib\\x64\r\n" +
		"Path=C:\\VS\\VC\\Tools\\MSVC\\14.50\\bin\\HostX64\\x64;C:\\Kits\\10\\bin\\x64;C:\\VS\\VC\\Tools\\MSVC\\14.50\\bin\\HostX64\\x64\\;c:\\NOVA-SWARM\\bin;C:\\Windows\\system32;C:\\Program Files\\Git\\cmd\r\n" +
		"PROMPT=$P$G\r\n" +
		"VCToolsInstallDir=C:\\VS\\VC\\Tools\\MSVC\\14.50\\\r\n" +
		"VSCMD_ARG_TGT_ARCH=x64\r\n" +
		"WindowsSdkDir=C:\\Kits\\10\\\r\n" +
		"EMPTY=\r\n" +
		"no equals sign here\r\n"
	got := MSVCEnvFrom([]byte(set), base)
	assert.Equal(t, []string{`C:\VS\VC\Tools\MSVC\14.50\bin\HostX64\x64`, `C:\Kits\10\bin\x64`}, got.PathDirs,
		"the new directories in vcvars' order, once each, and none the base PATH held (no case, no trailing separator)")
	assert.Equal(t, []string{
		`INCLUDE=C:\VS\VC\Tools\MSVC\14.50\include;C:\Kits\10\include\ucrt`,
		`LIB=C:\VS\VC\Tools\MSVC\14.50\lib\x64`,
		`LIBPATH=C:\VS\VC\Tools\MSVC\14.50\lib\x64`,
		`VCToolsInstallDir=C:\VS\VC\Tools\MSVC\14.50\`,
		`VSCMD_ARG_TGT_ARCH=x64`,
		`WindowsSdkDir=C:\Kits\10\`,
	}, got.Vars, "only the compiler's and build's names; ALLUSERSPROFILE and PROMPT are not vcvars' to hand over")
	assert.Empty(t, MSVCEnvFrom([]byte("INCLUDE=x\r\n"), base).PathDirs, "a set with no PATH adds no directory")
}
