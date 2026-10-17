package main

import (
 "encoding/json"
 "testing"
 "github.com/stretchr/testify/assert"
 "github.com/stretchr/testify/require"
)

func TestColdReadDryRunRefusalJSON(t *testing.T) {
 t.Parallel()
 ta, _ := friendCardApp(t, "only friend amy", "amy")
 code, out, errs := ta.do("unpin missing --reason share --dry-run --json")
 t.Logf("exit=%d stdout=%s stderr=%s", code, out, errs)
 require.Equal(t, 1, code)
 var got map[string]any
 require.NoError(t, json.Unmarshal([]byte(out), &got))
 assert.Equal(t, float64(code), got["exit"], "JSON exit must match process exit")
 assert.NotEqual(t, "ok", got["status"], "a refused operation cannot report success")
}
