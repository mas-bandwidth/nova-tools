package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVisibleRootsIncludeDataGrantsAndExcludeMetadataOnlyAncestors(t *testing.T) {
	t.Parallel()
	p := &Policy{Reads: []string{"/read"}, ReadsNoExec: []string{"/noexec"}, Writes: []string{"/work"}, OptRoots: []string{"/program"}, PathDirs: []string{"/metadata-only"}, Tmp: "/work/tmp", Home: "/work/home"}
	roots := p.VisibleRoots()
	for _, path := range []string{"/read", "/noexec", "/work", "/program", "/work/tmp", "/work/home"} { assert.Contains(t, roots, path) }
	for _, path := range systemVisibleRoots() { assert.Contains(t, roots, path) }
	assert.NotContains(t, roots, "/metadata-only")
}
