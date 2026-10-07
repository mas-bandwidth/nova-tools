package sprintdash

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// priorityDriver draws a where --json snapshot with priorities and reads waiting with app.js on
// the scroll test's DOM shim and reads the priority marks back: each mark's classes and title.
const priorityDriver = `
context.render(input.data);
const box = doc.getElementById('priority-marks');
process.stdout.write(JSON.stringify({ hidden: !!box.hidden, marks: box.children.map(k => ({ cls: k._classes, title: k.title })) }));
`

// A card's priority shows by colour on its mark (docs/SPEC-SPRINT-DASHBOARD.md, "Priority";
// the owner, 2026-10-06): a blocker bright red, a critical dark red, a read the orange of the
// robot's shoes, and work blue whatever its level.
func TestPriorityMarksShowTheThreeColours(t *testing.T) {
	t.Parallel()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("NOVA_CI") == "1" {
			require.NoError(t, err, "node is required for the dashboard JS behavioural test")
		}
		t.Skip("node is not installed on this machine; the dashboard JS behavioural test needs it")
	}
	var d map[string]any
	require.NoError(t, json.Unmarshal(fixture(t), &d))
	d["priorities"] = map[string]any{"blocker": []string{"s1-9"}, "critical": []string{"s1-3"}, "high": []string{"s2-1"}, "low": []string{"s2-4"}}
	d["reads_waiting"] = 2

	shim, _, ok := strings.Cut(scrollShim, "// the viewer:")
	require.True(t, ok, "the scroll test's shim has its viewer")
	in, err := json.Marshal(map[string]any{"appJS": string(file("app.js")), "data": d})
	require.NoError(t, err)
	cmd := exec.Command(nodePath, "-e", shim+priorityDriver)
	cmd.Stdin = bytes.NewReader(in)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	require.NoError(t, cmd.Run(), "node runner failed: %s", errBuf.String())
	require.Empty(t, errBuf.String(), "app.js threw while drawing")
	var res struct {
		Hidden bool
		Marks  []struct {
			Cls   []string
			Title string
		}
	}
	require.NoError(t, json.Unmarshal(outBuf.Bytes(), &res), outBuf.String())
	assert.False(t, res.Hidden)
	var got [][2]string
	for _, m := range res.Marks {
		cls := ""
		for _, c := range m.Cls {
			if strings.HasPrefix(c, "p-") {
				cls = c
			}
		}
		got = append(got, [2]string{cls, m.Title})
	}
	assert.Equal(t, [][2]string{
		{"p-blocker", "blocker: s1-9"},
		{"p-critical", "critical: s1-3"},
		{"p-work", "high: s2-1"},
		{"p-reader", "reader: a read waiting"},
		{"p-reader", "reader: a read waiting"},
		{"p-work", "low: s2-4"},
	}, got, "the ladder's order, each in its colour")
}

// The three colours and the mark classes, read in Go from the page and app.js with no node:
// a machine without node still checks every hex value (the cold reader of PR 5377).
func TestPriorityColoursAreThePagesConstants(t *testing.T) {
	t.Parallel()
	page := string(file("index.html"))
	for name, want := range map[string]string{"--p-blocker": "#ff1a1a", "--p-critical": "#9b1c1c", "--p-reader": "#fb8321", "--p-work": "var(--s-working)"} {
		m := regexp.MustCompile(regexp.QuoteMeta(name) + `:\s*([^;]+);`).FindStringSubmatch(page)
		require.NotNil(t, m, "the page defines %s", name)
		assert.Equal(t, want, strings.TrimSpace(m[1]), name)
	}
	for _, rule := range []string{".p-blocker { background: var(--p-blocker); }", ".p-critical { background: var(--p-critical); }", ".p-reader { background: var(--p-reader); }", ".p-work { background: var(--p-work); }"} {
		assert.Contains(t, page, rule, "the mark class takes its colour")
	}
	js := string(file("app.js"))
	assert.Contains(t, js, `if (level.indexOf("critical") === 0) return "p-critical";`, "a critical mark, set or by weight, is dark red")
	assert.Contains(t, js, `return level === "blocker" || level === "reader" ? "p-" + level : "p-work";`, "a blocker red, a read orange, every work card blue")
	assert.Contains(t, js, "renderPriorityMarks(d)", "render() draws the marks")
}
