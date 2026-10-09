package friend

import "sync/atomic"

// sessionTarget keeps the adapter's observed target and the proof's pinned target;
// ordinary standalone adapter callers retain discovery (SPEC-FRIEND, Session binding).
type sessionTarget struct{ pin, observed atomic.Pointer[string] }

func (t *sessionTarget) get(fallback string) string {
	if p := t.pin.Load(); p != nil {
		return *p
	}
	return fallback
}
func (t *sessionTarget) saw(id string) { t.observed.Store(&id) }
func (t *sessionTarget) reported(fallback string) string {
	if p := t.pin.Load(); p != nil {
		return *p
	}
	if p := t.observed.Load(); p != nil {
		return *p
	}
	return fallback
}
func (t *sessionTarget) set(id string) error                 { t.pin.Store(&id); return nil }
func (o *OpenCode) DeliverySession() string                  { return o.target.reported(o.Session) }
func (o *OpenCode) SwitchDeliverySession(id string) error    { return o.target.set(id) }
func (c *Codex) DeliverySession() string                     { return c.target.reported(c.Session) }
func (c *Codex) SwitchDeliverySession(id string) error       { return c.target.set(id) }
func (d *DSH) DeliverySession() string                       { return d.target.reported(d.Session) }
func (d *DSH) SwitchDeliverySession(id string) error         { return d.target.set(id) }
func (g *Gemini) DeliverySession() string                    { return g.target.reported(g.Session) }
func (g *Gemini) SwitchDeliverySession(id string) error      { return g.target.set(id) }
func (a *Antigravity) DeliverySession() string               { return a.target.reported(a.Session) }
func (a *Antigravity) SwitchDeliverySession(id string) error { return a.target.set(id) }

func (g *Grok) DeliverySession() string               { return g.target.reported(g.Wake) }
func (g *Grok) SwitchDeliverySession(id string) error { return g.target.set(id) }
func (t *Tmux) DeliverySession() string               { return t.target.reported(t.Session) }
func (t *Tmux) SwitchDeliverySession(id string) error { return t.target.set(id) }
