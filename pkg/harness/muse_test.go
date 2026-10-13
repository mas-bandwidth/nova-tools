package harness

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/mas-bandwidth/nova-tools/pkg/testbin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain makes this test binary the fake muse, with the real program's argv.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "check-child-env" {
		if os.Getenv("META_API_KEY") != "" {
			os.Exit(12)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "exec" {
		key, err := io.ReadAll(os.Stdin)
		if err != nil || string(key) != "fake-provider-key\n" || os.Getenv("META_API_KEY") != "" {
			os.Exit(8)
		}
		if len(os.Args) != 8 || os.Args[2] != "--prompt-file" || os.Args[4] != "--json" || os.Args[5] != "--model" || os.Args[7] != "--api-key-stdin" {
			os.Exit(9)
		}
		prompt, err := os.ReadFile(os.Args[3])
		if err != nil || string(prompt) != "card prompt" {
			os.Exit(10)
		}
		cwd, err := os.Getwd()
		if err != nil || cwd != filepath.Dir(os.Args[3]) {
			os.Exit(11)
		}
		child := subproc.Long(context.Background(), os.Args[0], "check-child-env")
		child.Env = os.Environ()
		if err := child.Run(); err != nil {
			os.Exit(12)
		}
		if os.Args[6] == "refused" {
			fmt.Println(`{"payload_type":"run.terminal.failed","payload":{"kind":"run_terminal","terminal":"failed","reason":"provider: HTTP 401 invalid API key"}}`)
			os.Exit(1)
		}
		fmt.Println(`{"payload_type":"run.model.completed","payload":{"kind":"model_completed","model":"frontier-model","usage":{"input_tokens":12,"output_tokens":7,"cache_write_tokens":3,"cache_read_tokens":5,"reasoning_tokens":2}}}`)
		fmt.Println(`{"payload_type":"run.terminal.completed","payload":{"kind":"run_terminal","terminal":"completed","text":"pong","reason":null}}`)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestMuseRunsACardHeadless(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, model string
		symlink     bool
	}{
		{name: "frontier-model", model: "frontier-model"},
		{name: "refused", model: "refused"},
		{name: "symlink working directory", model: "frontier-model", symlink: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			model := tc.model
			dir := t.TempDir()
			if tc.symlink {
				link := filepath.Join(t.TempDir(), "card")
				require.NoError(t, os.Symlink(dir, link))
				dir = link
			}
			dir, err := filepath.EvalSymlinks(dir)
			require.NoError(t, err)
			prompt := filepath.Join(dir, "prompt")
			require.NoError(t, os.WriteFile(prompt, []byte("card prompt"), 0o600))
			self, err := os.Executable()
			require.NoError(t, err)
			bin := filepath.Join(dir, "muse")
			require.NoError(t, testbin.Place(self, bin))
			argv := MuseArgv(bin, model, prompt)
			assert.Equal(t, []string{bin, "exec", "--prompt-file", prompt, "--json", "--model", model, "--api-key-stdin"}, argv)
			key, env := MuseInput([]string{"META_API_KEY=fake-provider-key", "PATH=/bin"})
			assert.NotContains(t, strings.Join(argv, " "), key)
			assert.NotContains(t, strings.Join(env, " "), key)
			cmd := subproc.Long(context.Background(), argv[0], argv[1:]...)
			cmd.Dir, cmd.Env, cmd.Stdin = dir, env, strings.NewReader(key+"\n")
			out, runErr := cmd.Output()
			result, err := ReadMuse(out)
			require.NoError(t, err)
			if model == "refused" {
				require.Error(t, runErr)
				assert.Contains(t, result.Failure, "401 invalid API key")
			} else {
				require.NoError(t, runErr)
				assert.Equal(t, "pong", result.Text)
				assert.Equal(t, map[string]int64{"input_tokens": 12, "output_tokens": 7, "cache_write_tokens": 3, "cache_read_tokens": 5, "reasoning_tokens": 2}, result.Usage)
			}
		})
	}
}

func TestMuseUsageKeepsMissingApartFromZeroAndRejectsBadCounts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, capture string
		want          map[string]int64
		bad           bool
	}{
		{"absent", `{"payload_type":"run.terminal.completed","payload":{"kind":"run_terminal","terminal":"completed","text":"pong"}}`, map[string]int64{}, false},
		{"zero", `{"payload_type":"run.model.completed","payload":{"kind":"model_completed","usage":{"input_tokens":0}}}`, map[string]int64{"input_tokens": 0}, false},
		{"negative", `{"payload_type":"run.model.completed","payload":{"kind":"model_completed","usage":{"input_tokens":-1}}}`, nil, true},
		{"malformed", `{"payload_type":"run.model.completed","payload":`, nil, true},
		{"two calls and attribution mirror", "{\"payload\":{\"kind\":\"model_completed\",\"usage\":{\"input_tokens\":2}}}\n{\"payload\":{\"kind\":\"goal_usage_attribution\",\"record\":{\"input_tokens\":99}}}\n{\"payload\":{\"kind\":\"model_completed\",\"usage\":{\"input_tokens\":3}}}", map[string]int64{"input_tokens": 5}, false},
		{"overflow", "{\"payload\":{\"kind\":\"model_completed\",\"usage\":{\"input_tokens\":9223372036854775807}}}\n{\"payload\":{\"kind\":\"model_completed\",\"usage\":{\"input_tokens\":1}}}", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ReadMuse([]byte(tc.capture))
			if tc.bad {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.Usage)
		})
	}
}

func TestMuseKeyIsRemovedFromEveryEnvironmentEntry(t *testing.T) {
	t.Parallel()
	key, child := MuseInput([]string{"META_API_KEY=old", "PATH=/bin", "META_API_KEY=new"})
	assert.Equal(t, "new", key)
	assert.Equal(t, []string{"PATH=/bin"}, child)
	key, child = MuseInput([]string{"PATH=/bin"})
	assert.Empty(t, key)
	assert.Equal(t, []string{"PATH=/bin"}, child)
}

func TestMuseTerminalAnswerReplacesAnEarlierFailure(t *testing.T) {
	t.Parallel()
	got, err := ReadMuse([]byte("{\"payload\":{\"kind\":\"run_terminal\",\"terminal\":\"failed\",\"reason\":\"invalid API key\"}}\n{\"payload\":{\"kind\":\"run_terminal\",\"terminal\":\"completed\",\"text\":\"pong\"}}"))
	require.NoError(t, err)
	assert.Equal(t, "pong", got.Text)
	assert.Empty(t, got.Failure)
}

func TestMuseFailureFallsBackToTerminalText(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, capture, want string }{
		{"nil reason", `{"payload":{"kind":"run_terminal","terminal":"failed","reason":null,"text":"HTTP 401 invalid API key"}}`, "HTTP 401 invalid API key"},
		{"empty reason", `{"payload":{"kind":"run_terminal","terminal":"failed","reason":"","text":"HTTP 429 rate limit"}}`, "HTTP 429 rate limit"},
		{"blank reason", `{"payload":{"kind":"run_terminal","terminal":"failed","reason":" ","text":"insufficient credit"}}`, "insufficient credit"},
		{"no text", `{"payload":{"kind":"run_terminal","terminal":"failed","reason":null}}`, "failed"},
		{"no terminal detail", `{"payload":{"kind":"run_terminal"}}`, "run failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ReadMuse([]byte(tc.capture))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.Failure)
		})
	}
}
