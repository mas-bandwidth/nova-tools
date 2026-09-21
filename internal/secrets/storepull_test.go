package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStorePullUsesBenchDeployKey pins SPEC-SECRETS.md rule 7:
//
//	"The store is pulled on a bench over a bench-owned read-only deploy key,
//	 never a person's credential, because a person's key in a bench's clone
//	 is that person on that bench."
//
// A store whose git remote origin URL embeds a person's credential token
// means the bench authenticates as a person, not as a bench-owned deploy key.
// No existing function in this package enforces that rule — the test is RED
// as a finding, not as a bug in the test.
func TestStorePullUsesBenchDeployKey(t *testing.T) {
	tmp := t.TempDir()
	gitDir := filepath.Join(tmp, ".git")
	mustMkdir(t, gitDir, 0755)

	// Set up a minimal git repo with a HEAD that matches the remote tracking
	// ref so CheckGitWorkingCopy would succeed if it only checked refs. The
	// remote origin URL embeds a person's credential token.
	const fakeSHA = "abcdefabcdefabcdefabcdefabcdefabcdefabcd"
	heads := filepath.Join(gitDir, "refs", "heads")
	remotes := filepath.Join(gitDir, "refs", "remotes", "origin")
	mustMkdir(t, heads, 0755)
	mustMkdir(t, remotes, 0755)
	mustWrite(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/main\n", 0644)
	mustWrite(t, filepath.Join(heads, "main"), fakeSHA+"\n", 0644)
	mustWrite(t, filepath.Join(remotes, "main"), fakeSHA+"\n", 0644)
	mustWrite(t, filepath.Join(gitDir, "config"), ""+
		"[core]\n"+
		"\trepositoryformatversion = 0\n"+
		"\tfilemode = true\n"+
		"\tbare = false\n"+
		"[branch \"main\"]\n"+
		"\tremote = origin\n"+
		"\tmerge = refs/heads/main\n"+
		"[remote \"origin\"]\n"+
		"\turl = https://x-access-token:ghp_fakecredentialtoken@github.test/org/store.git\n"+
		"\tfetch = +refs/heads/*:refs/remotes/origin/*\n", 0644)

	// CheckGitWorkingCopy sees a store whose remote URL carries a person's
	// token. It should refuse — but it does not check the remote URL, so it
	// succeeds. That is the finding.
	st, err := CheckGitWorkingCopy(tmp)
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if st.Clean {
		// The store is clean only because no function checks the remote
		// URL for embedded credentials. This test is RED by design.
		t.Error("CheckGitWorkingCopy reports a clean store whose remote origin URL carries a person's credential token; deploy key enforcement is absent")
	}

	// Also verify the remote URL is detectable from git config.
	configBytes, err := os.ReadFile(filepath.Join(gitDir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	config := string(configBytes)
	if !strings.Contains(config, "https://x-access-token:ghp_") {
		t.Error("credential URL not found in git config; fixture is wrong")
	}
}
