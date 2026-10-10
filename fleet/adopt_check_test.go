package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdoptCheckReachesItsReceipt exercises the real play with only fixture
// processes/files: no build, store, launchd or friend service exists here.
func TestAdoptCheckReachesItsReceipt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                       string
		staged, check, missingSops bool
	}{
		{"unstaged", false, true, false}, {"staged held bus", true, true, false},
		{"real held corrupt bus", true, false, false}, {"missing sops refusal", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			staged := tc.staged
			t.Parallel()
			play, err := exec.LookPath("ansible-playbook")
			if err != nil {
				t.Skip("ansible-playbook is not installed on this machine")
			}
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			bin := filepath.Join(home, ".local/bin")
			require.NoError(t, os.MkdirAll(bin, 0755))
			fake := `#!/usr/bin/env python3
import os,sys,json
name=os.path.basename(sys.argv[0]);args=sys.argv[1:]
if name=='nova-secrets':
 if not args[args.index('--sops')+1]: sys.exit('empty sops')
 command=args[args.index('--')+1:];os.execv(command[0],command)
elif name=='nova-sprint' and args[0]=='live':
 print(json.dumps({'agents':[],'processes':[],'dashboard':[],'server':{'revision':'fixture'},'library':{'loaded':'fixture','match':True}}))
elif name=='nova-sprint' and args[:2]==['server','switch'] and '--dry-run' in args:
 print('SWITCH DRY-RUN OK')
elif name=='nova-config' and args[:2]==['migrate','--dry-run'] and '--json' in args:
 print(json.dumps({'result':{'status':'ok'},'facts':{'role':'nova_config','owner':'nova_config','ready':'yes'}}))
elif name=='nova-config' and args[:2]==['migrate','--window']:
 print('MIGRATE APPLIED fixture')
elif name=='nova-redis' and args[:2]==['fn','check']:
 print('MATCH fixture')
elif name=='nova-redis' and args[:2]==['fn','load']:
 print('LOADED fixture')
elif args and args[0]=='version': print('fixture-old')
else: sys.exit('unexpected fixture command: '+name+' '+repr(args))
`
			for _, name := range []string{"nova-secrets", "nova-sprint", "nova-config", "nova-redis", "nova-update", "sops"} {
				require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(fake), 0755))
			}
			if tc.missingSops {
				require.NoError(t, os.Remove(filepath.Join(bin, "sops")))
			}
			held := filepath.Join(bin, "nova-bus")
			require.NoError(t, os.WriteFile(held, []byte("old held bus\n"), 0755))
			before, err := os.Stat(held)
			require.NoError(t, err)
			inventory := filepath.Join(dir, "inventory.ini")
			require.NoError(t, os.WriteFile(inventory, []byte("[all]\nlocalhost ansible_connection=local\n[coordinator]\nlocalhost\n[store_deployer]\nlocalhost\n[tla]\n"), 0600))
			vars := map[string]any{"nova_home": home, "nova_version": "v0.0.0-check", "nova_source": "fixture", "nova_dogfood_receipts": dir, "nova_release_out": filepath.Join(dir, "build"), "nova_sops_candidates": []string{filepath.Join(bin, "sops")}, "nova_seat": "fixture-seat", "nova_store": "localhost", "nova_redis_port": 6380, "nova_redis_addr": "localhost:6380", "nova_pg_dsn": "postgres://fixture@localhost/database", "nova_os": "linux", "nova_arch": "amd64", "nova_tool_hold": []string{"nova-bus"}}
			if staged {
				stage := filepath.Join(home, "nova-bench/release/v0.0.0-check/linux-amd64")
				require.NoError(t, os.MkdirAll(stage, 0755))
				var sums strings.Builder
				for _, name := range []string{"nova-update", "nova-sprint", "nova-config", "nova-redis", "nova-bus"} {
					body := []byte(fake + "\n")
					if name == "nova-bus" {
						body = []byte("new held bus\n")
					}
					require.NoError(t, os.WriteFile(filepath.Join(stage, name), body, 0755))
					sum := sha256.Sum256(body)
					sums.WriteString(hex.EncodeToString(sum[:]) + "  " + name + "\n")
				}
				require.NoError(t, os.WriteFile(filepath.Join(stage, "SHA256SUMS"), []byte(sums.String()), 0644))
				if !tc.check {
					require.NoError(t, os.Remove(filepath.Join(stage, "nova-update")))
					buildCtx, buildCancel := context.WithTimeout(context.Background(), 120*time.Second)
					defer buildCancel()
					build := exec.CommandContext(buildCtx, "go", "build", "-p", "2", "-o", filepath.Join(stage, "nova-update"), "./cmd/nova-update")
					build.Dir = ".."
					out, err := build.CombinedOutput()
					require.NoError(t, err, string(out))
					bytes, err := os.ReadFile(filepath.Join(stage, "nova-update"))
					require.NoError(t, err)
					sum := sha256.Sum256(bytes)
					manifest := hex.EncodeToString(sum[:]) + "  nova-update\n"
					bytes, err = os.ReadFile(filepath.Join(stage, "nova-sprint"))
					require.NoError(t, err)
					sum = sha256.Sum256(bytes)
					manifest += hex.EncodeToString(sum[:]) + "  nova-sprint\n"
					for _, name := range []string{"nova-config", "nova-redis"} {
						bytes, err = os.ReadFile(filepath.Join(stage, name))
						require.NoError(t, err)
						sum = sha256.Sum256(bytes)
						manifest += hex.EncodeToString(sum[:]) + "  " + name + "\n"
					}
					manifest += strings.Repeat("0", 64) + "  nova-bus\n"
					require.NoError(t, os.WriteFile(filepath.Join(stage, "SHA256SUMS"), []byte(manifest), 0644))
				}
			}
			body, err := json.Marshal(vars)
			require.NoError(t, err)
			extra := filepath.Join(dir, "vars.json")
			require.NoError(t, os.WriteFile(extra, body, 0600))
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			args := []string{"-i", inventory, "tools.yml", "--tags", "seat", "-e", "@" + extra}
			if tc.check {
				args = append(args, "--check")
			}
			command := exec.CommandContext(ctx, play, args...)
			command.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "ANSIBLE_LOCAL_TEMP="+filepath.Join(dir, "ansible/tmp"), "ANSIBLE_HOME="+filepath.Join(dir, "ansible"))
			out, err := command.CombinedOutput()
			if tc.missingSops {
				require.Error(t, err)
				assert.Contains(t, string(out), "ADOPT REFUSED step=sops host=localhost")
				assert.NotContains(t, string(out), "has no attribute")
			} else if !tc.staged {
				require.Error(t, err)
				assert.Contains(t, string(out), "ADOPT REFUSED step=candidate host=localhost")
				assert.NotContains(t, string(out), "ADOPT DRY-RUN OK")
			} else {
				require.NoError(t, err, string(out))
				if tc.check {
					assert.Regexp(t, `ADOPT DRY-RUN OK host=localhost would_replace=[0-9]+ held=1 stops=[^[:space:]]+ reinstalls=[^[:space:]]+`, string(out))
				} else {
					assert.Contains(t, string(out), "WINDOW host=localhost")
					assert.Contains(t, string(out), "held=nova-bus")
					sums, err := os.ReadFile(filepath.Join(home, "nova-bench/release/held-selection/v0.0.0-check/linux-amd64/SHA256SUMS"))
					require.NoError(t, err)
					assert.NotContains(t, string(sums), "nova-bus")
					assert.Contains(t, string(sums), "nova-update")
				}
			}
			for _, line := range strings.Split(string(out), "\n") {
				if strings.Contains(line, "ADOPT DRY-RUN OK") || strings.Contains(line, "WINDOW host=") || strings.Contains(line, "ADOPT REFUSED step=sops") {
					t.Log(line)
				}
			}
			after, err := os.Stat(held)
			require.NoError(t, err)
			assert.True(t, os.SameFile(before, after), "the held nova-bus inode changed")
			bytes, err := os.ReadFile(held)
			require.NoError(t, err)
			assert.Equal(t, "old held bus\n", string(bytes))
		})
	}
}
