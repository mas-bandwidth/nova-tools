package lineup

import (
	"testing"
	"time"
)

func TestParseStageReadsWidthAndWall(t *testing.T) {
	cases := []struct {
		line    string
		ok      bool
		staged  int
		width   int
		elapsed time.Duration
		wall    bool
	}{
		{"STAGE OK 64/64 elapsed=42s", true, 64, 64, 42 * time.Second, true},
		{"STAGE OK 64/64 in 59.5s", true, 64, 64, 59500 * time.Millisecond, true},
		{"STAGE OK 8/8 in 1m3s", true, 8, 8, 63 * time.Second, true},
		{"STAGE OK 8/8 elapsed=12", true, 8, 8, 12 * time.Second, true},
		{"STAGE FAIL 60/64 elapsed=30s", false, 60, 64, 30 * time.Second, true},
		{"STAGE OK 8/8", true, 8, 8, 0, false},
	}
	for _, c := range cases {
		s, parsed := ParseStage(c.line)
		if !parsed || s.OK != c.ok || s.Staged != c.staged || s.Width != c.width || s.Elapsed != c.elapsed || s.HasWall != c.wall {
			t.Errorf("%q: got %+v parsed=%v", c.line, s, parsed)
		}
	}
	if _, parsed := ParseStage("LAUNCH OK 8/8"); parsed {
		t.Error("a non-STAGE line parsed")
	}
}

func TestLintLauncherFlagsSSHCommandWordsOnly(t *testing.T) {
	red := []string{
		"ssh host bash -s < card",
		"out=$(ssh -o BatchMode=yes $h true)",
		"/usr/bin/ssh $h true",
		"true && scp card $h:/tmp/",
		"  ssh $h 'nova-swarm native'  # per card",
	}
	green := []string{
		"# ssh $h is what we used to do",
		"ssh-keygen -l -f key",
		"echo sshd is fine",
		"nova-swarm native --card $1 # no ssh here",
		"GIT_SSH_COMMAND=true git fetch",
	}
	for _, l := range red {
		if r := LintLauncher("l.sh", "#!/bin/bash\n"+l+"\n"); r.Status != Red {
			t.Errorf("%q: %s, want RED", l, r)
		}
	}
	for _, l := range green {
		if r := LintLauncher("l.sh", "#!/bin/bash\n"+l+"\n"); r.Status != Green {
			t.Errorf("%q: %s, want GREEN", l, r)
		}
	}
}
