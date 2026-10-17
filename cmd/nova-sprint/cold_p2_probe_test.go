package main

import (
 "encoding/json"
 "strings"
 "testing"
 "github.com/stretchr/testify/assert"
 "github.com/stretchr/testify/require"
)

func TestColdP2PreviewContract(t *testing.T) {
 t.Parallel()
 for _, ids := range []string{"missing", "s1-1 missing", "s1-1 s1-1"} {
  t.Run(ids, func(t *testing.T) {
   ta, _ := friendCardApp(t, "only friend amy", "amy")
   cmd := "unpin " + ids + " --reason release --dry-run"
   code, out, errs := ta.do(cmd + " --json")
   var got map[string]any
   require.NoError(t, json.Unmarshal([]byte(out), &got), errs)
   wantCode, wantStatus := 0, "ok"
   if strings.Contains(ids, "missing") { wantCode, wantStatus = 1, "refused" }
   assert.Equal(t, wantCode, code)
   assert.Equal(t, float64(code), got["exit"])
   assert.Equal(t, wantStatus, got["status"])
   textCode, textOut, _ := ta.do(cmd)
   assert.Equal(t, code, textCode)
   assert.True(t, strings.HasPrefix(textOut, "UNPIN " + strings.ToUpper(wantStatus) + " DRY-RUN"), textOut)
   assert.Equal(t, "only.friend.amy", ta.primary("s1-1").F("who"))
   assert.NotContains(t, ta.ok("log --card s1-1"), "WHO unpinned")
   t.Logf("ids=%q code=%d status=%s changes=%v", ids, code, got["status"], got["changes"])
  })
 }
}

func TestColdP2ReturnedThroughCLI(t *testing.T) {
 t.Parallel()
 ta, root := friendCardApp(t, "only friend amy", "amy", "bob")
 ta.a.tip = tipIs(t, "")
 ta.ok("tick")
 ta.ok("friend sync --root " + root)
 ta.ok("friend take amy s1-1 --reason share")
 ta.ok("tick")
 before := ta.primary("s1-1").F("brief")
 var preview map[string]any
 ta.json("unpin s1-1 --reason share --dry-run", &preview)
 assert.Equal(t, float64(1), preview["changes"])
 assert.Equal(t, "only.friend.amy", ta.primary("s1-1").F("who"))
 ta.ok("unpin s1-1 --reason share")
 ta.ok("tick")
 var c cardView
 ta.json("card s1-1", &c)
 require.Len(t, c.Work, 1)
 assert.Equal(t, "friend.bob", c.Work[0].Row)
 assert.Equal(t, "3", c.Work[0].F("gen"))
 assert.Equal(t, before, ta.primary("s1-1").F("brief"))
 assert.Empty(t, ta.primary("s1-1").F("who"))
 assert.Contains(t, ta.ok("log --card s1-1"), "WHO unpinned")
 ta.clean()
}
