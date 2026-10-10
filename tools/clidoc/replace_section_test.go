package main

import (
	"strings"
	"testing"
)

func TestClidocReplaceSectionKeepsDollarSigns(t *testing.T) {
	t.Parallel()

	// The reference text contains various dollar sign patterns that must
	// survive the replacement literally: $HOME, ${HOME}, $1, $2, and $$
	ref := `
$HOME expands to the home directory
${HOME} is the same
$1 and $2 are regex groups
$$ is a literal dollar`

	doc := `<!-- clidoc:begin nova-test -->
OLD CONTENT
<!-- clidoc:end nova-test -->

<!-- clidoc:begin nova-other -->
OTHER CONTENT
<!-- clidoc:end nova-other -->

Prose between and after blocks.
`

	// Run replaceSection on nova-test block
	result := replaceSection(doc, "nova-test", ref)

	// Verify the dollar signs appear literally
	if !strings.Contains(result, "$HOME") {
		t.Errorf("result missing literal $HOME; got: %s", result)
	}
	if !strings.Contains(result, "${HOME}") {
		t.Errorf("result missing literal ${HOME}; got: %s", result)
	}
	if !strings.Contains(result, "$1") {
		t.Errorf("result missing literal $1; got: %s", result)
	}
	if !strings.Contains(result, "$2") {
		t.Errorf("result missing literal $2; got: %s", result)
	}
	if !strings.Contains(result, "$$") {
		t.Errorf("result missing literal $$; got: %s", result)
	}

	// Verify other block is untouched
	if !strings.Contains(result, "OTHER CONTENT") {
		t.Errorf("result missing untouched nova-other block; got: %s", result)
	}

	// Verify prose is untouched
	if !strings.Contains(result, "Prose between and after blocks.") {
		t.Errorf("result missing untouched prose; got: %s", result)
	}

	// Verify the old content marker is gone (it's been replaced)
	if strings.Contains(result, "OLD CONTENT") {
		t.Errorf("result should have replaced OLD CONTENT with ref; got: %s", result)
	}
}

func TestClidocReplaceSectionKeepsDollarSignsNoMarkers(t *testing.T) {
	t.Parallel()

	ref := `ref with $HOME and $1`

	doc := `No clidoc markers here.
Prose stays as is.`

	result := replaceSection(doc, "nova-test", ref)

	// Document without markers should be returned unchanged
	if result != doc {
		t.Errorf("doc without markers should be unchanged; got:\n%s", result)
	}
}
