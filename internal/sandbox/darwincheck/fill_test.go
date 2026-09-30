package darwincheck

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/profiles"
)

// fakeFS answers the filler's questions from tables.
type fakeFS struct {
	dirs  map[string]bool
	links map[string]string
	real  map[string]string
}

func (f fakeFS) IsDir(p string) bool { return f.dirs[p] }
func (f fakeFS) Readlink(p string) (string, bool) {
	t, ok := f.links[p]
	return t, ok
}
func (f fakeFS) Real(p string) string {
	if r, ok := f.real[p]; ok {
		return r
	}
	return p
}

func TestFillTemplateReplacesEachMarkerLineAndNothingElse(t *testing.T) {
	t.Parallel()
	tmpl := strings.Join([]string{
		";; header @@OPTROOTS@@ inside a line is not a marker",
		"(version 1)",
		"@@OPTROOTS@@",
		"@@ANCESTORS@@",
		"@@READS@@",
		"@@READSNOEXEC@@",
		"(allow process-exec*)",
		"@@WRITES@@",
		"@@NET@@",
		"  @@NET@@",
		"",
	}, "\n")
	got := fillTemplate(tmpl, fillInput{
		write:    "/private/var/s/w",
		ref:      "/private/var/s/ref",
		optRoots: []string{"/opt/homebrew"},
	})
	want := strings.Join([]string{
		";; header @@OPTROOTS@@ inside a line is not a marker",
		"(version 1)",
		`(allow file-read* (subpath "/opt/homebrew"))`,
		`(allow file-read-metadata (literal "/opt"))`,
		`(allow file-read-metadata (literal "/private"))`,
		`(allow file-read-metadata (literal "/private/var"))`,
		`(allow file-read-metadata (literal "/private/var/s"))`,
		`(allow file-read* (subpath (param "READ0")))`,
		"(allow process-exec*)",
		`(allow file-read* file-write* (subpath (param "WRITE0")))`,
		`(allow network-outbound (subpath (param "WRITE0")))`,
		`(allow network-outbound (remote ip) (literal "/private/var/run/mDNSResponder"))`,
		"  @@NET@@",
		"",
	}, "\n")
	if got != want {
		t.Errorf("filled text\n%s\nwant\n%s", got, want)
	}
}

func TestAnAbsentOptionalRootLeavesTheMarkerEmpty(t *testing.T) {
	t.Parallel()
	got := fillTemplate("a\n@@OPTROOTS@@\nb\n", fillInput{write: "/x/w", ref: "/x/r"})
	if got != "a\nb\n" {
		t.Errorf("an empty optional-roots marker left %q", got)
	}
}

func TestATemplateLineWithNoNewlineIsNotALine(t *testing.T) {
	t.Parallel()
	// A shell's `while read -r` drops a final line with no newline; the embedded
	// template ends with one, and the filler reads it the same way.
	if got := fillTemplate("a\nb", fillInput{}); got != "a\n" {
		t.Errorf("fillTemplate = %q", got)
	}
	if !strings.HasSuffix(profiles.DarwinTemplate, "\n") {
		t.Errorf("profiles/darwin.sb.tmpl does not end with a newline")
	}
}

func TestAncestorsAreProperAndSlashIsExcluded(t *testing.T) {
	t.Parallel()
	if got := ancestorsOf("/a/b/c"); !reflect.DeepEqual(got, []string{"/a/b", "/a"}) {
		t.Errorf("ancestorsOf(/a/b/c) = %v", got)
	}
	if got := ancestorsOf("/a"); len(got) != 0 {
		t.Errorf("ancestorsOf(/a) = %v", got)
	}
}

// The embedded template, filled: every marker is gone and the grants the check's
// denials depend on are present.
func TestTheRealTemplateFillsCompletely(t *testing.T) {
	t.Parallel()
	got := fillTemplate(profiles.DarwinTemplate, fillInput{write: "/private/var/s/w", ref: "/private/var/s/ref", optRoots: []string{"/opt/homebrew"}})
	if strings.Contains(got, "@@") {
		for _, l := range strings.Split(got, "\n") {
			if strings.Contains(l, "@@") && !strings.HasPrefix(l, ";;") {
				t.Errorf("a marker survived: %s", l)
			}
		}
	}
	for _, want := range []string{
		`(allow file-read* (subpath (param "READ0")))`,
		`(allow file-read* file-write* (subpath (param "WRITE0")))`,
		`(allow network-outbound (subpath (param "WRITE0")))`,
		`(literal "/private/var/run/mDNSResponder")`,
		`(allow file-read* (subpath "/opt/homebrew"))`,
		`(allow file-read-metadata (literal "/private/var/s"))`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the filled template lacks %s", want)
		}
	}
	// The network grant is IP only: never (allow network*) on a line of policy.
	for _, l := range strings.Split(got, "\n") {
		if !strings.HasPrefix(l, ";;") && strings.Contains(l, "(allow network*)") {
			t.Errorf("the filled profile grants every unix-domain socket: %s", l)
		}
	}
}

func TestOptionalRoots(t *testing.T) {
	t.Parallel()
	all := map[string]bool{"/opt/homebrew": true, "/opt/local": true, "/usr/local/bin": true, "/opt/tools/bin": true}
	t.Run("the documented roots that exist", func(t *testing.T) {
		t.Parallel()
		got := optionalRoots(fakeFS{dirs: all}, "/opt/tools/bin")
		want := []string{"/opt/homebrew", "/opt/local", "/opt/tools/bin"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("roots = %v, want %v", got, want)
		}
	})
	t.Run("an absent root is skipped, never refused", func(t *testing.T) {
		t.Parallel()
		got := optionalRoots(fakeFS{dirs: map[string]bool{"/opt/local": true}}, "/nowhere")
		if !reflect.DeepEqual(got, []string{"/opt/local"}) {
			t.Errorf("roots = %v", got)
		}
	})
	t.Run("a root the template already grants is skipped", func(t *testing.T) {
		t.Parallel()
		dirs := map[string]bool{}
		for _, r := range []string{"/usr/local/bin", "/usr", "/bin", "/sbin", "/System/Library", "/Library/Developer/CommandLineTools", "/private/etc/x", "/private/var/select", "/dev/fd"} {
			dirs[r] = true
		}
		for r := range dirs {
			if got := optionalRoots(fakeFS{dirs: dirs}, r); len(got) != 0 {
				t.Errorf("%s was granted as an optional root: %v", r, got)
			}
		}
		// /bin/foo is not /bin: the template grants the tree /usr, and /bin exactly.
		if got := optionalRoots(fakeFS{dirs: map[string]bool{"/bin/foo": true}}, "/bin/foo"); len(got) != 1 {
			t.Errorf("/bin/foo skipped: %v", got)
		}
	})
	t.Run("the git directory is named once", func(t *testing.T) {
		t.Parallel()
		got := optionalRoots(fakeFS{dirs: all}, "/opt/homebrew")
		if !reflect.DeepEqual(got, []string{"/opt/homebrew", "/opt/local"}) {
			t.Errorf("roots = %v", got)
		}
	})
}

// xcode-select's link is followed the way the generator follows it: the
// developer directory it names becomes a root, Contents and not
// Contents/Developer, resolved, and a relative target is read from the link's
// own directory.
func TestOptionalRootsFollowXcodeSelectLink(t *testing.T) {
	t.Parallel()
	app := "/Applications/Xcode.app/Contents"
	cases := []struct {
		name string
		fs   fakeFS
		want []string
	}{
		{"Xcode.app: Contents, not Contents/Developer", fakeFS{
			dirs:  map[string]bool{app: true, app + "/Developer": true},
			links: map[string]string{"/var/db/xcode_select_link": app + "/Developer"},
		}, []string{app}},
		{"a relative target is read from the link's directory", fakeFS{
			dirs:  map[string]bool{"/var/db/Xcode.app/Contents": true, "/var/db/Xcode.app/Contents/Developer": true},
			links: map[string]string{"/var/db/xcode_select_link": "Xcode.app/Contents/Developer"},
		}, []string{"/var/db/Xcode.app/Contents"}},
		{"the target is resolved", fakeFS{
			dirs:  map[string]bool{"/Applications/Xcode-beta.app/Contents/Developer": true, app: true},
			links: map[string]string{"/private/var/db/xcode_select_link": "/Applications/Xcode-beta.app/Contents/Developer"},
			real:  map[string]string{"/Applications/Xcode-beta.app/Contents/Developer": "/Applications/Xcode.app/Contents/Developer"},
		}, []string{app}},
		{"CommandLineTools sits under /Library, which the template grants", fakeFS{
			dirs:  map[string]bool{"/Library/Developer/CommandLineTools": true},
			links: map[string]string{"/var/db/xcode_select_link": "/Library/Developer/CommandLineTools"},
		}, nil},
		{"a link to a directory that is not there adds nothing", fakeFS{
			links: map[string]string{"/var/db/xcode_select_link": "/gone"},
		}, []string{}},
		{"an empty link target is skipped", fakeFS{
			dirs:  map[string]bool{},
			links: map[string]string{"/var/db/xcode_select_link": ""},
		}, nil},
		{"both link spellings name one root once", fakeFS{
			dirs: map[string]bool{app: true, app + "/Developer": true},
			links: map[string]string{
				"/var/db/xcode_select_link":         app + "/Developer",
				"/private/var/db/xcode_select_link": app + "/Developer",
			},
		}, []string{app}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := optionalRoots(tc.fs, "/nowhere")
			if len(got) != len(tc.want) || (len(got) > 0 && !reflect.DeepEqual(got, tc.want)) {
				t.Errorf("roots = %v, want %v", got, tc.want)
			}
		})
	}
}

// Rule 9's scrub set is exact, by name: the wrapper drops what addresses an
// agent and keeps what says what is RUNNING the job.
func TestChildEnvDropsExactlyTheAgentVariables(t *testing.T) {
	t.Parallel()
	in := []string{
		"SSH_AUTH_SOCK=/s", "SSH_AGENT_PID=1", "SSH_AGENT_LAUNCHER=x", "GPG_AGENT_INFO=/g:1:1",
		"GNOME_KEYRING_AGENT_PID=2", "FOO_AGENT_INFO=y", "BAR_AGENT_SOCK=z",
		"NOVA_KEEP=kept", "AI_AGENT=claude", "CLAUDE_AGENT_SDK_VERSION=1", "AGENT=x", "SSH_AUTH_SOCKET=keep", "PATH=/bin",
	}
	want := []string{"NOVA_KEEP=kept", "AI_AGENT=claude", "CLAUDE_AGENT_SDK_VERSION=1", "AGENT=x", "SSH_AUTH_SOCKET=keep", "PATH=/bin"}
	if got := childEnv(in); !reflect.DeepEqual(got, want) {
		t.Errorf("childEnv = %v, want %v", got, want)
	}
}
