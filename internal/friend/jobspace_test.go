package friend

import (
 "context"
 "fmt"
 "os"
 "path/filepath"
 "testing"

 "github.com/mas-bandwidth/nova-tools/internal/testkit"
 "github.com/stretchr/testify/assert"
 "github.com/stretchr/testify/require"
)

// TestTheDaemonKeepsJobsUnderTheCapAndReportsSpace pins Delivery's cleanup guard: a report and
// origin proof permit removal, while active or unpublished work stays recoverable.
func TestTheDaemonKeepsJobsUnderTheCapAndReportsSpace(t *testing.T) {
 t.Parallel()
 for _, row := range []struct{name string; report, push, dirty, live bool; removed bool}{
  {name:"published LAND", report:true, push:true, removed:true},
  {name:"unreported", push:true},
  {name:"unpushed", report:true},
  {name:"uncommitted", report:true, push:true, dirty:true},
  {name:"active", report:true, push:true, live:true},
 } {
  t.Run(row.name, func(t *testing.T) {
   t.Parallel()
   g := testkit.Git(t,1)
   g.Commit(g.Clones[0],map[string]string{"README.md":"base\n"})
   env := stageEnv(t)
   gitIn(t,env,g.Clones[0],"push","-q",g.Remote,"HEAD:refs/heads/sprint/mechanical")
   dir := t.TempDir()
   stager := &Stager{Dir:dir,Env:env,URL:func(string)string{return g.Remote}}
   p,ok := PacketOf(stagedCard("a.w1","working","mas-bandwidth/nova-tools","sprint/mechanical"))
   require.True(t,ok)
   _,err := stager.Stage(context.Background(),p)
   require.NoError(t,err)
   checkout := filepath.Join(JobDir(dir,p.Job),"repo")
   g.Commit(checkout,map[string]string{"work.md":"finished\n"})
   head := gitIn(t,env,checkout,"rev-parse","HEAD")
   if row.push {gitIn(t,env,checkout,"push","-q","origin",p.Branch)}
   if row.report {
    out := filepath.Join(dir,"outbox",p.Job)
    require.NoError(t,os.MkdirAll(out,0o755))
    require.NoError(t,os.WriteFile(filepath.Join(out,"REPORT.md"),[]byte(fmt.Sprintf("Verdict: LAND\nHead: %s\n",head)),0o644))
   }
   if row.dirty {require.NoError(t,os.WriteFile(filepath.Join(checkout,"work.md"),[]byte("not committed\n"),0o644))}
   pruned,err := stager.Prune(context.Background(),map[string]bool{p.Job:row.live},0)
   require.NoError(t,err)
   if row.removed {
    assert.Equal(t,[]string{p.Job},pruned)
    assert.NoDirExists(t,JobDir(dir,p.Job))
    assert.FileExists(t,filepath.Join(dir,"outbox",p.Job,"REPORT.md"))
   } else {
    assert.Empty(t,pruned,"absent lane alone never authorizes deleting unpublished work")
    assert.DirExists(t,checkout)
   }
  })
 }
}
