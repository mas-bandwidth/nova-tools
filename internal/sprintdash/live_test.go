package sprintdash

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The page pins (docs/SPEC-SPRINT.md section 2, the friends category). Changing a page
// file is a named change to these tables in the same commit. The friends cost category
// added the Cost breakdown's rows, the friends row token counts only: the live page
// takes the same change.

// liveRendered is the sha256 of each live file with its comments stripped (rendered):
// what the browser runs and shows.
var liveRendered = map[string]string{
	"app.js":     "b260148225fcb532f01ea269281ee90b8e7baf19f931642f614fb92016fdecaf",
	"index.html": "6b7a5e5cf59b4b78a0915a38c091eb63d8ed1321434d8be5244fa28b3fc78f68",
	"OFL.txt":    "580df76c95a1ec5ab878ceb25bb3d85c6a076804e9c970c8c6972aea775fdf65",
}

// pageSHA256 is the sha256 of each embedded file as it is in the repo, comments and all.
// Changing a page file is changing this table in the same commit: a named change.
var pageSHA256 = map[string]string{
	"app.js":           "33b4597f8edab12a70df32f678e4a68465504fdcebd9ecdcf26147a73346bc5c",
	"index.html":       "27816fdfb5459eb16b27e005d50f2727c718fb17ba5aeb7a802e50a92c41d34d",
	"OFL.txt":          "580df76c95a1ec5ab878ceb25bb3d85c6a076804e9c970c8c6972aea775fdf65",
	"nunito-800.woff2": "b42be94a8cf3d5fc7877216cdb8bbfb10d57291b06356b6f3b9e7fbf9742b8da",
}

func sum256(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// TestThePagePinsMoveWithThePage pins the page: each embedded file hashes to pageSHA256,
// and each file the browser runs, comments stripped, hashes to liveRendered. A change to
// app.js or index.html that does not move these tables fails here.
func TestThePagePinsMoveWithThePage(t *testing.T) {
	t.Parallel()
	embedded, err := page.ReadDir("page")
	require.NoError(t, err)
	names := map[string]bool{}
	for _, e := range embedded {
		names[e.Name()] = true
	}
	assert.Len(t, names, len(pageSHA256), "every page file is pinned, and only they")
	for name := range pageSHA256 {
		assert.True(t, names[name], "pinned %s is not embedded", name)
	}
	for name, want := range pageSHA256 {
		assert.Equal(t, want, sum256(file(name)), "%s changed: a change to the page is a named change to pageSHA256", name)
	}
	for name, want := range liveRendered {
		assert.Equal(t, want, sum256(rendered(name, file(name))), "%s does not render as the pinned page", name)
	}
}

// The stripper keeps what renders and drops only comments.
func TestRenderedDropsOnlyComments(t *testing.T) {
	t.Parallel()
	js := "var a = \"// no\", b = '/* no */'; // yes\nvar r = /\\/\\/[/]x/g; /* yes */ var c = `//${a}`;\nx = a / b / c; // yes"
	assert.Equal(t, "var a = \"// no\", b = '/* no */'; \nvar r = /\\/\\/[/]x/g;  var c = `//${a}`;\nx = a / b / c; ", string(rendered("app.js", []byte(js))))
	html := "<p>it's <!-- a note -->here</p>\n<style>a { b: url(\"/x\"); } /* note */</style><script>var s = \"</p>\"; // note\n</script><!--LOGO-->"
	assert.Equal(t, "<p>it's here</p>\n<style>a { b: url(\"/x\"); } </style><script>var s = \"</p>\"; \n</script>", string(rendered("index.html", []byte(html))))
	assert.Equal(t, []byte("as is /* */"), rendered("OFL.txt", []byte("as is /* */")))
}

// rendered is a page file with its comments taken out: what the browser runs and shows.
// app.js loses its // and /* */ comments; index.html its <!-- --> comments, the /* */
// comments of its <style> blocks and the comments of its <script> blocks; any other file
// is kept whole. Strings, template literals and regular expression literals are kept as
// they are, so a "//" inside one is no comment.
func rendered(name string, b []byte) []byte {
	switch filepath.Ext(name) {
	case ".js":
		return stripJS(b)
	case ".html":
		return stripHTML(b)
	}
	return b
}

// stripHTML drops the markup's comments and the comments inside its style and script blocks.
func stripHTML(b []byte) []byte {
	var out []byte
	for i := 0; i < len(b); {
		switch {
		case bytes.HasPrefix(b[i:], []byte("<!--")):
			end := bytes.Index(b[i+4:], []byte("-->"))
			if end < 0 {
				return out
			}
			i += 4 + end + 3
		case hasTag(b[i:], "<style"), hasTag(b[i:], "<script"):
			tag := "style"
			if hasTag(b[i:], "<script") {
				tag = "script"
			}
			open := bytes.IndexByte(b[i:], '>')
			if open < 0 {
				return append(out, b[i:]...)
			}
			out = append(out, b[i:i+open+1]...)
			i += open + 1
			end := bytes.Index(bytes.ToLower(b[i:]), []byte("</"+tag))
			if end < 0 {
				end = len(b) - i
			}
			if tag == "style" {
				out = append(out, stripCSS(b[i:i+end])...)
			} else {
				out = append(out, stripJS(b[i:i+end])...)
			}
			i += end
		default:
			out = append(out, b[i])
			i++
		}
	}
	return out
}

// hasTag is whether b opens with the tag (case aside) followed by '>' or a space.
func hasTag(b []byte, tag string) bool {
	if len(b) <= len(tag) || !bytes.EqualFold(b[:len(tag)], []byte(tag)) {
		return false
	}
	c := b[len(tag)]
	return c == '>' || c == ' ' || c == '\t' || c == '\n'
}

// stripCSS drops /* */ comments outside strings.
func stripCSS(b []byte) []byte {
	var out []byte
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == '"' || c == '\'':
			j := quoted(b, i)
			out = append(out, b[i:j]...)
			i = j
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i = blockEnd(b, i)
		default:
			out = append(out, c)
			i++
		}
	}
	return out
}

// stripJS drops // and /* */ comments outside strings, template literals and regular
// expression literals; a line comment's newline is kept.
func stripJS(b []byte) []byte {
	var out []byte
	var prev byte // the last byte kept that is not space: whether a '/' opens a regex
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == '"' || c == '\'' || c == '`':
			j := quoted(b, i)
			out = append(out, b[i:j]...)
			i, prev = j, c
			continue
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			continue
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i = blockEnd(b, i)
			continue
		case c == '/' && opensRegex(prev):
			j := regexEnd(b, i)
			out = append(out, b[i:j]...)
			i, prev = j, '/'
			continue
		}
		out = append(out, c)
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			prev = c
		}
		i++
	}
	return out
}

// opensRegex is whether a '/' after prev begins a regular expression literal rather than a division.
func opensRegex(prev byte) bool {
	return prev == 0 || bytes.IndexByte([]byte("(,=:[!&|?{};+-*%<>~^"), prev) >= 0
}

// quoted is the index just past the string or template literal opening at i.
func quoted(b []byte, i int) int {
	q := b[i]
	for j := i + 1; j < len(b); j++ {
		switch b[j] {
		case '\\':
			j++
		case q:
			return j + 1
		}
	}
	return len(b)
}

// blockEnd is the index just past the /* */ comment opening at i.
func blockEnd(b []byte, i int) int {
	end := bytes.Index(b[i+2:], []byte("*/"))
	if end < 0 {
		return len(b)
	}
	return i + 2 + end + 2
}

// regexEnd is the index just past the regular expression literal opening at i, its flags with it.
func regexEnd(b []byte, i int) int {
	class := false
	for j := i + 1; j < len(b); j++ {
		switch c := b[j]; {
		case c == '\\':
			j++
		case c == '[':
			class = true
		case c == ']':
			class = false
		case c == '\n':
			return j
		case c == '/' && !class:
			j++
			for j < len(b) && (b[j] >= 'a' && b[j] <= 'z') {
				j++
			}
			return j
		}
	}
	return len(b)
}
