package ci

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGeneralityTextReadsHTMLWitness pins that the generality-text scan reads
// .html, .js and .css files, refusing forbidden names in them.
func TestGeneralityTextReadsHTMLWitness(t *testing.T) {
	t.Parallel()

	assert.True(t, isTextScanned("web/index.html"), "isTextScanned should recognize .html")
	assert.True(t, isTextScanned("web/app.js"), "isTextScanned should recognize .js")
	assert.True(t, isTextScanned("web/style.css"), "isTextScanned should recognize .css")

	empty, err := allowlist.Parse("debt", "# ceiling: 0\n", allowlist.Options{Ceiling: true, Counted: true})
	require.NoError(t, err)
	noFixtures, err := allowlist.Parse("fixtures", "# ceiling: 0\n", allowlist.Options{Ceiling: true})
	require.NoError(t, err)

	file := func(rel, src string) []textScanFile { return []textScanFile{{Rel: rel, Src: []byte(src)}} }

	// Broken .html fixture fails
	brokenHTML := file("web/page.html", "<html><body><!-- host: studio --></body></html>\n")
	v := checkTextGenerality(brokenHTML, noFixtures, empty)
	require.NotEmpty(t, v, "html fixture with forbidden host must fail")
	assert.Contains(t, strings.Join(v, "\n"), "web/page.html")
	assert.Contains(t, strings.Join(v, "\n"), "studio")

	// Fixed .html fixture passes
	fixedHTML := file("web/page.html", "<html><body><!-- host: web-1 --></body></html>\n")
	v = checkTextGenerality(fixedHTML, noFixtures, empty)
	assert.Empty(t, v, "fixed html fixture must pass: %v", v)

	// Broken .js fixture fails
	brokenJS := file("web/app.js", "const host = 'studio';\n")
	v = checkTextGenerality(brokenJS, noFixtures, empty)
	require.NotEmpty(t, v, "js fixture with forbidden host must fail")
	assert.Contains(t, strings.Join(v, "\n"), "web/app.js")
	assert.Contains(t, strings.Join(v, "\n"), "studio")

	// Fixed .js fixture passes
	fixedJS := file("web/app.js", "const host = 'web-1';\n")
	v = checkTextGenerality(fixedJS, noFixtures, empty)
	assert.Empty(t, v, "fixed js fixture must pass: %v", v)

	// Broken .css fixture fails
	brokenCSS := file("web/style.css", "/* host: studio */\n")
	v = checkTextGenerality(brokenCSS, noFixtures, empty)
	require.NotEmpty(t, v, "css fixture with forbidden host must fail")
	assert.Contains(t, strings.Join(v, "\n"), "web/style.css")
	assert.Contains(t, strings.Join(v, "\n"), "studio")

	// Fixed .css fixture passes
	fixedCSS := file("web/style.css", "/* host: web-1 */\n")
	v = checkTextGenerality(fixedCSS, noFixtures, empty)
	assert.Empty(t, v, "fixed css fixture must pass: %v", v)
}
