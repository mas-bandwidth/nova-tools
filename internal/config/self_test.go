package config

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tailnetRunning = `{"BackendState":"Running","Self":{"HostName":"Box-1","DNSName":"m1.tail1234.ts.net.","Online":true}}`

func selfSrc(env map[string]string, host string, ts func(context.Context) ([]byte, error)) SelfSource {
	return SelfSource{
		Getenv:    func(k string) string { return env[k] },
		Hostname:  func() (string, error) { return host, nil },
		Tailscale: ts,
	}
}

func tsReturns(raw string) func(context.Context) ([]byte, error) {
	return func(context.Context) ([]byte, error) { return []byte(raw), nil }
}

// TestSelfNameIsTheTailnetNameWhenThereIsATailnet: the first label of the
// host's DNS name, lower-cased, beats the hostname; NOVA_MACHINE beats both;
// with no tailnet the hostname's first label stands.
func TestSelfNameIsTheTailnetNameWhenThereIsATailnet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, c := range []struct {
		name      string
		env       map[string]string
		host      string
		ts        func(context.Context) ([]byte, error)
		want, how string
	}{
		{"tailnet", nil, "box.local", tsReturns(tailnetRunning), "m1", SelfTailnet},
		{"env is lower-cased", map[string]string{EnvMachine: "M9"}, "box.local", tsReturns(tailnetRunning), "m9", SelfEnv},
		{"env wins", map[string]string{EnvMachine: "m9"}, "box.local", tsReturns(tailnetRunning), "m9", SelfEnv},
		{"empty env is unset", map[string]string{EnvMachine: ""}, "box.local", tsReturns(tailnetRunning), "m1", SelfTailnet},
		{"no program", nil, "Box.Local", func(context.Context) ([]byte, error) { return nil, ErrNoTailnet }, "box", SelfHostname},
		{"nil seam", nil, "m2.tail1234.ts.net", nil, "m2", SelfHostname},
		{"stopped tailnet", nil, "box.local", tsReturns(`{"BackendState":"Stopped","Self":{"DNSName":"m1.x.ts.net."}}`), "box", SelfHostname},
		{"not json", nil, "box.local", tsReturns(`nope`), "box", SelfHostname},
		{"the program fails", nil, "box.local", func(context.Context) ([]byte, error) { return nil, errors.New("exit 1") }, "box", SelfHostname},
		{"an invalid tailnet name falls to the hostname", nil, "box.local", tsReturns(`{"BackendState":"Running","Self":{"DNSName":"m_1.x.ts.net."}}`), "box", SelfHostname},
		{"hostname label when no dns name", nil, "box.local", tsReturns(`{"BackendState":"Running","Self":{"HostName":"M5"}}`), "m5", SelfTailnet},
	} {
		got, how, err := SelfName(ctx, selfSrc(c.env, c.host, c.ts))
		assert.False(t, err != nil || got != c.want || how != c.how, "%s: %q %q %v, want %q %q", c.name, got, how, err, c.want, c.how)
	}
}

// TestSelfNameRefusesAnInvalidNovaMachine: NOVA_MACHINE is validated as a
// machine name, never passed on as typed.
func TestSelfNameRefusesAnInvalidNovaMachine(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"m 1", "-m1", "m1/x", "m_1"} {
		env := map[string]string{EnvMachine: bad}
		n, _, err := SelfName(context.Background(), selfSrc(env, "box.local", nil))
		assert.False(t, err == nil || n != "", "%q: %q %v", bad, n, err)
	}
}

// TestSelfNameValidatesTheHostname: a hostname that is no machine name is
// refused, not passed on.
func TestSelfNameValidatesTheHostname(t *testing.T) {
	t.Parallel()
	for _, h := range []string{"bad_host.local", "-x.local"} {
		n, _, err := SelfName(context.Background(), selfSrc(nil, h, nil))
		assert.False(t, err == nil || n != "", "%q: %q %v", h, n, err)
	}
}

// TestSelfNameRefusesWhenNothingNamesTheMachine: no env, no tailnet, no
// hostname is an error, never an empty name.
func TestSelfNameRefusesWhenNothingNamesTheMachine(t *testing.T) {
	t.Parallel()
	src := SelfSource{Getenv: func(string) string { return "" }, Hostname: func() (string, error) { return "", errors.New("no hostname") }}
	n, _, err := SelfName(context.Background(), src)
	require.False(t, err == nil || n != "", "%q %v", n, err)
	src.Hostname = func() (string, error) { return ".local", nil }
	n, _, err = SelfName(context.Background(), src)
	require.False(t, err == nil || n != "", "an empty first label: %q %v", n, err)
}
