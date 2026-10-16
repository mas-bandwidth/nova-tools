package main

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// hookHarnessGrok is this client's one harness row. Other names stay with
// their own adapters. tla/GrokHookNotice.tla is the notice.
const hookHarnessGrok = "grok"

// grokRoute is the installed harness: the home the hooks live in, the wake
// file one monitor tails, and the notice those tails share.
type grokRoute struct {
	home, wake, notices, as string
	role                    hook.Role
}

// grokConfig resolves --harness grok before any hook record is written.
// An empty harness, and every harness this row does not own, returns nil.
func (w world) grokConfig(c *tool.Call) (*grokRoute, error) {
	if !strings.EqualFold(c.Str("harness"), hookHarnessGrok) {
		return nil, nil
	}
	if c.Str("codex-socket") != "" || c.Str("codex-thread") != "" || c.Str("codex-events-dir") != "" || c.Str("codex-since") != "" {
		return nil, errors.New("one harness at a time: --harness grok or the Codex desktop route, not both")
	}
	if c.Str("muse-bundle") != "" || c.Str("muse-bin") != "" {
		return nil, errors.New("one harness at a time: --harness grok or --harness muse, not both")
	}
	home, err := notice.GrokHome(c.Str("grok-home"), w.getenv)
	if err != nil {
		return nil, err
	}
	return &grokRoute{home: home, as: c.Str("as")}, nil
}

func (w world) installGrok(c *tool.Call, g *grokRoute) *tool.Out {
	wake, err := notice.InstallGrok(g.home)
	if err == nil {
		g.notices, err = notice.MakeDir(filepath.Join(g.home, "nova-hook", "notices"))
	}
	if err != nil {
		o := tool.Refuse("grok harness could not be installed: " + err.Error())
		o.Remedy = grokRemedy(g)
		return o
	}
	g.wake = wake
	line(c, tool.Done().As("STATE").Fact("state", "grok-installed").Fact("wake", wake).Note("monitor `tail -n 0 -F "+wake+"`"))
	return nil
}

func grokRemedy(g *grokRoute) string {
	role := string(g.role)
	if role == "" {
		role = "<role>"
	}
	as := g.as
	if as == "" {
		as = "<me>"
	}
	return "nova-hook hook --as " + as + " --role " + role + " --harness grok --grok-home " + g.home
}
