package main

import "testing"

// benchlog_test.go pins the ONE decision TestBenchProbeNeverReadsAuth rests on:
// given a line of the fake ssh's recording, did the probe READ the auth file?
//
// It is pinned here because the decision was wrong on exactly one platform, and
// was wrong for a reason no amount of re-running it on Linux would ever show.
// The check grepped the whole recorded LINE for "cat", "head" and "cp". On
// darwin `t.TempDir()` sits under a per-boot random `$TMPDIR` -- the Air's is
//
//	/var/folders/vk/dgdj_cpn55177y7hyx0qyx_r0000gn/T
//
// and `_cpn55177` contains the letters `cp`. So a line that ran nothing but
// `stat` read as a line that had copied the key out, and the probe's own
// security test failed on every Mac in the fleet and on no Linux bench. The
// path is random per boot, so it is also a defect that comes and goes.
//
// The fix is the rule these cases enforce: the recording is a list of ARGV
// WORDS, and the question is which WORD was the command -- never which letters
// appear somewhere in the line. The `stat` line with the macOS temporary
// directory in it is the first case below, and it is the whole bug.

func TestProbeLogReadCheckIsOnArgvWords(t *testing.T) {
	const darwinAuth = "/var/folders/vk/dgdj_cpn55177y7hyx0qyx_r0000gn/T/TestBenchProbeNeverReadsAuth2499271931/001/auth"
	const linuxAuth = "/tmp/TestBenchProbeNeverReadsAuth123/001/auth"

	cases := []struct {
		name string
		line string
		path string
		want bool
	}{{
		// THE DEFECT. Only `stat` ran; `cp` is three letters of a random
		// directory name that macOS chose at boot.
		name: "darwin tempdir containing cp, stat only",
		line: "b2 stat -c %a " + darwinAuth,
		path: darwinAuth,
		want: false,
	}, {
		name: "linux tempdir, stat only",
		line: "b2 stat -c %a " + linuxAuth,
		path: linuxAuth,
		want: false,
	}, {
		// The same class one directory up: a path whose own name holds a
		// reader's letters is still only a path.
		name: "a directory named concatenated, stat only",
		line: "b2 stat -c %a /home/nova/concatenated/auth",
		path: "/home/nova/concatenated/auth",
		want: false,
	}, {
		// And the sharpest form of it: the file itself is named for a
		// reader. It is still the argument, not the command.
		name: "the auth file itself is named cp",
		line: "b2 stat -c %a /home/nova/keys/cp",
		path: "/home/nova/keys/cp",
		want: false,
	},
		// The reads the check exists to catch, each of which must stay caught.
		{name: "cat", line: "b2 cat " + linuxAuth, path: linuxAuth, want: true},
		{name: "head", line: "b2 head -c 1 " + linuxAuth, path: linuxAuth, want: true},
		{name: "cp", line: "b2 cp " + linuxAuth + " /tmp/stolen", path: linuxAuth, want: true},
		{
			// By its full path, because `cat` is `/bin/cat` too.
			name: "an absolute reader",
			line: "b2 /bin/cat " + linuxAuth,
			path: linuxAuth,
			want: true,
		}, {
			// A read of some OTHER file is not a read of this one.
			name: "a reader on a different file",
			line: "b2 cat /home/nova/notes",
			path: linuxAuth,
			want: false,
		}, {
			name: "a line naming no path at all",
			line: "b2 nproc",
			path: linuxAuth,
			want: false,
		}, {
			name: "the empty line the recording ends on",
			line: "",
			path: linuxAuth,
			want: false,
		}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := readsPath(c.line, c.path); got != c.want {
				t.Errorf("readsPath(%q, %q) = %v, want %v", c.line, c.path, got, c.want)
			}
		})
	}
}

// The positive half of the same check: the probe HAS to stat the auth path, and
// that assertion must not be satisfiable by the letters of a path either.
func TestProbeLogRanOnIsOnArgvWords(t *testing.T) {
	const auth = "/home/nova/.config/statistics/auth"
	// `statistics` contains `stat`, so a substring check calls this line a
	// stat of the auth file when the probe never stat'd anything.
	if ranOn("b2 nproc\nb2 true "+auth+"\n", "stat", auth) {
		t.Errorf("a path containing the letters of a command read as running it")
	}
	if !ranOn("b2 nproc\nb2 stat -c %a "+auth+"\n", "stat", auth) {
		t.Errorf("a real stat of the auth path was not seen")
	}
	// Two different lines do not add up to one: a stat of some other file plus
	// a mention of the auth path is not a stat of the auth path.
	if ranOn("b2 stat -c %a /home/nova/other\nb2 touch "+auth+"\n", "stat", auth) {
		t.Errorf("a stat on one line and the path on another read as one command")
	}
}
