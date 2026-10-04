package fleet

// The semver-tag branch of IsValidBuildVersion is the identity a certificate binds to:
// BuildVersion reads it out of a machine's `nova-merge version` line and Certify writes it
// into every certificate row. A token that only LOOKS like a version -- v1banana, v1..2,
// v1.2.3- -- must never become that identity. The adopted validator is
// golang.org/x/mod/semver (docs/STANDARD.md section 7), which accepts the complete and
// shortened forms the tag grammar allows and rejects the rest.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/mod/semver"
)

// TestBuildVersionRejectsMalformedSemverTags pins the semver-tag branch of
// IsValidBuildVersion to golang.org/x/mod/semver. Malformed version-looking tokens are not
// build identities, while the complete, prerelease, build-metadata and shortened forms
// x/mod/semver accepts (v0.17.0, v1.2.3-pre+build, v1, v1.2) stay accepted. BuildVersion is
// the reader that turns a `nova-merge version` line into that identity, so the same tokens
// are checked through it as `<tool> <version>` lines.
func TestBuildVersionRejectsMalformedSemverTags(t *testing.T) {
	t.Parallel()

	malformed := []string{
		"v1banana",
		"v1..2",
		"v1.2.3-",
		"v1.2.3-.",
		"v1.2.3+",
		"v1.2.3.4",
		"v01.2.3",
	}
	for _, tok := range malformed {
		t.Run("malformed/"+tok, func(t *testing.T) {
			assert.False(t, semver.IsValid(tok), "x/mod/semver accepts %q; the case is not malformed", tok)
			assert.False(t, IsValidBuildVersion(tok), "IsValidBuildVersion(%q) = true, want false", tok)
			assert.Equal(t, "", BuildVersion("nova-merge "+tok),
				"BuildVersion(nova-merge %s) = a build identity, want empty", tok)
		})
	}

	valid := []string{
		"v0.17.0",
		"v1",
		"v1.2",
		"v1.2.3",
		"v1.2.3-pre",
		"v1.2.3+build",
		"v1.2.3-pre+build",
		"v2.0.0-rc.1",
	}
	for _, tok := range valid {
		t.Run("valid/"+tok, func(t *testing.T) {
			assert.True(t, semver.IsValid(tok), "x/mod/semver rejects %q; the case is not valid", tok)
			assert.True(t, IsValidBuildVersion(tok), "IsValidBuildVersion(%q) = false, want true", tok)
			assert.Equal(t, tok, BuildVersion("nova-merge "+tok), "BuildVersion(nova-merge %s)", tok)
		})
	}
}
