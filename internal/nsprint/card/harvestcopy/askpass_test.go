package harvestcopy

import (
	"strings"
	"testing"
)

// TestAskpassAnswersGitFromTheEnvironment: in askpass mode the binary prints
// the user name for a username prompt and the token from TokenEnv for a
// password prompt, and outside askpass mode it does nothing.
func TestAskpassAnswersGitFromTheEnvironment(t *testing.T) {
	t.Parallel()
	env := map[string]string{AskpassEnv: "1", TokenEnv: "ghp-secret"}
	getenv := func(k string) string { return env[k] }
	var out strings.Builder
	if !Askpass([]string{"Username for 'https://example.com': "}, getenv, &out) || out.String() != "x-access-token\n" {
		t.Fatalf("username prompt: %q", out.String())
	}
	out.Reset()
	if !Askpass([]string{"Password for 'https://x-access-token@example.com': "}, getenv, &out) || out.String() != "ghp-secret\n" {
		t.Fatalf("password prompt: %q", out.String())
	}
	out.Reset()
	if Askpass([]string{"Password"}, func(string) string { return "" }, &out) || out.String() != "" {
		t.Fatalf("outside askpass mode: handled, wrote %q", out.String())
	}
}

// TestGitEnvKeepsTheTokenOutOfArgvAndOffThePathRemote: a network remote's
// git child gets this binary as GIT_ASKPASS with the token in TokenEnv for
// it and nothing else; a path remote's child gets no credential at all; any
// token variable in the wrapper's own environment is dropped either way.
func TestGitEnvKeepsTheTokenOutOfArgvAndOffThePathRemote(t *testing.T) {
	t.Parallel()
	base := []string{"PATH=/usr/bin", TokenEnv + "=stale", "GITHUB_TOKEN=other", "GIT_ASKPASS=/bin/echo", "HOME=/h"}
	req := Request{Token: "ghp-new", Askpass: "/opt/nova-card"}

	network, err := gitEnv(base, req, true)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"PATH": "/usr/bin", "HOME": "/h", "GIT_TERMINAL_PROMPT": "0",
		"GIT_ASKPASS": "/opt/nova-card", AskpassEnv: "1", TokenEnv: "ghp-new"}
	if got := envMap(network); len(got) != len(want) {
		t.Fatalf("network env %v, want %v", got, want)
	} else {
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("network env %s=%q, want %q (%v)", k, got[k], v, got)
			}
		}
	}

	local, err := gitEnv(base, req, false)
	if err != nil {
		t.Fatal(err)
	}
	got := envMap(local)
	for _, k := range []string{TokenEnv, AskpassEnv, "GIT_ASKPASS", "GITHUB_TOKEN"} {
		if _, ok := got[k]; ok {
			t.Fatalf("path remote env carries %s: %v", k, local)
		}
	}
	if got["GIT_TERMINAL_PROMPT"] != "0" || got["PATH"] != "/usr/bin" {
		t.Fatalf("path remote env %v", local)
	}
	for _, kv := range append(network, local...) {
		if strings.Contains(kv, "stale") || strings.Contains(kv, "other") {
			t.Fatalf("a stale token survived: %q", kv)
		}
	}
}

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

// TestNetworkRemote: a URL or user@host is a host; a path or file:// is not.
func TestNetworkRemote(t *testing.T) {
	t.Parallel()
	for remote, want := range map[string]bool{
		"https://example.com/o/r.git": true, "git@example.com:o/r.git": true, "ssh://git@example.com/o/r": true,
		"/nowhere/remote.git": false, "file:///nowhere/remote.git": false, "remote.git": false,
	} {
		if got := networkRemote(remote); got != want {
			t.Fatalf("networkRemote(%q)=%v, want %v", remote, got, want)
		}
	}
}
