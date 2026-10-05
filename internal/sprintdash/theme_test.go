package sprintdash

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPageHasNoThemeToggleAndStaysDark is the always-dark rule (docs/SPEC-SPRINT-DASHBOARD.md):
// the page has no theme toggle, no light tokens and no theme read; its inline script only
// clears a stored sprint-theme, and the html element stays data-theme="dark".
func TestPageHasNoThemeToggleAndStaysDark(t *testing.T) {
	t.Parallel()
	html := string(file("index.html"))
	app := string(file("app.js"))
	assert.NotContains(t, html, `id="theme"`, "index.html has a theme toggle button")
	assert.NotContains(t, html, `data-theme="light"`, "index.html carries light tokens")
	assert.Contains(t, html, `<html lang="en" data-theme="dark">`, "the html element stays dark")
	assert.NotContains(t, app, "sprint-theme", "app.js still stores a theme")
	assert.NotContains(t, app, `$("theme")`, "app.js still binds the toggle button")
	assert.Contains(t, html, `removeItem("sprint-theme")`, "the inline script clears a stored theme")
	assert.NotContains(t, html, "getItem", "the inline script still reads a stored theme")
}
