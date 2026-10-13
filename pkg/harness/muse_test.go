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
 if len(os.Args) > 1 && os.Args[1] == "exec" {
  key, err := io.ReadAll(os.Stdin)
  if err != nil || string(key) != "fake-provider-key\n" || os.Getenv("META_API_KEY") != "" { os.Exit(8) }
  if len(os.Args) != 8 || os.Args[2] != "--prompt-file" || os.Args[4] != "--json" || os.Args[5] != "--model" || os.Args[7] != "--api-key-stdin" { os.Exit(9) }
  prompt, err := os.ReadFile(os.Args[3])
  if err != nil || string(prompt) != "card prompt" { os.Exit(10) }
  cwd, err := os.Getwd()
  if err != nil || cwd != filepath.Dir(os.Args[3]) { os.Exit(11) }
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
 for _, model := range []string{"frontier-model", "refused"} {
  t.Run(model, func(t *testing.T) {
   t.Parallel()
   dir := t.TempDir()
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
    assert.Equal(t, map[string]int64{"tokens_in":12,"tokens_out":7,"cache_write":3,"cache_read":5,"reasoning":2}, result.Usage)
   }
  })
 }
}
