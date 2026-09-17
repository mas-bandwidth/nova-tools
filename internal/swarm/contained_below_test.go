package swarm

import (
	"strings"
	"testing"
)

// ISSUE #69: containment comes from below the model. A swarm worker runs a cheaper model
// than the seat, and a cheaper model follows a planted instruction more readily, so the
// machinery -- not the model's good behaviour -- must keep the worker inside its job. Two
// halves of this issue are already built and pinned elsewhere; this test pins them together
// as the issue names them:
//
//	item 1, read-only credentials: the child environment carries the provider's inference key
//	and the job's own paths, and NO git or gh credential, so a worker has nothing to push with;
//	clone-over-https is enforced by admission (admitrepo.go, "private-repo ... unreachable
//	without auth"), and the wall denies the key file even when its value is in the environment
//	(SPEC-SWARM rule 6, SPEC-SANDBOX the dispatcher caller).
//
//	item 2, the OS sandbox at the launch seam: the wrap argv names the job directory and its
//	per-job data home as the WHOLE write set, the worker home as a read, the job directory as
//	the cwd, and never --net-deny -- the provider's API is the work.
func TestWorkersAreContainedBelowTheModel(t *testing.T) {
	job := SandboxJob{
		Sandbox:  "/usr/local/bin/nova-sandbox",
		SlotDir:  "/pool/worker-home-1",
		JobDir:   "/pool/worker-home-1/jobs/abc",
		DataHome: "/pool/worker-home-1/jobs/abc/data",
		Command:  "/usr/local/bin/opencode",
		Args:     []string{"run", "--model", "deepseek/deepseek-flash", "--", "/pool/worker-home-1/jobs/abc/PROMPT.md"},
	}
	argv := strings.Join(job.SandboxArgv(), " ")
	for _, want := range []string{
		"--read /pool/worker-home-1",
		"--write /pool/worker-home-1/jobs/abc",
		"--write /pool/worker-home-1/jobs/abc/data",
		"--cwd /pool/worker-home-1/jobs/abc",
		"-- /usr/local/bin/opencode",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("the wall argv does not carry %q: %s", want, argv)
		}
	}
	if strings.Contains(argv, "--net-deny") {
		t.Errorf("--net-deny is in the argv, and the provider's API is the work: %s", argv)
	}

	w := Worker{EnvVar: "DEEPSEEK_API_KEY"}
	env := strings.Join(childEnv(w, 1, "abc", "sk-test"), "\n")
	if !strings.Contains(env, "DEEPSEEK_API_KEY=sk-test") {
		t.Errorf("the inference key did not reach the child: %s", env)
	}
	for _, banned := range []string{"SSH_AUTH_SOCK", "GIT_SSH", "GIT_ASKPASS", "GH_TOKEN", "GITHUB_TOKEN", "AWS_SECRET", "GPG_AGENT_INFO"} {
		if strings.Contains(env, banned+"=") {
			t.Errorf("a write-capable credential (%s) reaches the worker: %s", banned, env)
		}
	}
}
