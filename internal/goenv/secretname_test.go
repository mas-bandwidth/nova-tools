package goenv

import "testing"

func TestIsSecretNameMatchesKeyTokenSecret(t *testing.T) {
	for _, name := range []string{"API_KEY", "GITHUB_TOKEN", "MY_SECRET", "aws_secret_access_key"} {
		if !IsSecretName(name) {
			t.Errorf("IsSecretName(%q) = false, want true", name)
		}
	}
}

func TestIsSecretNameLeavesOrdinaryNamesAlone(t *testing.T) {
	for _, name := range []string{"PATH", "HOME", "GOENV", "GOOS", "MY_FLAG"} {
		if IsSecretName(name) {
			t.Errorf("IsSecretName(%q) = true, want false", name)
		}
	}
}
