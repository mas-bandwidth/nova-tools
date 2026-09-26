package gh

import (
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// Token is the bearer token: GH_TOKEN, then GITHUB_TOKEN, then `gh auth
// token` (which honours GH_CONFIG_DIR, the fleet's per-seat gh config). The
// gh run here is the one gh shell-out in nova-sprint (TestOneGitHubClient)
// and it makes no GitHub call: it reads the login gh stored.
func Token() (string, error) {
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v, nil
		}
	}
	testguard.RefuseHosts("gh", "auth", "token")
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return "", errors.New("no GH_TOKEN or GITHUB_TOKEN, and gh auth token failed: " + err.Error())
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" {
		return "", errors.New("gh auth token printed nothing; set GH_TOKEN")
	}
	return tok, nil
}
