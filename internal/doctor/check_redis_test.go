package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctorRedisCheckFindsAMissingACLUser(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))

	env := map[string]string{
		"PATH":               bin,
		"NOVA_SECRETS_STORE": root + "/store",
		"NOVA_SECRETS_SEAT":  "coordinator",
		"NOVA_REDIS_ADDR":    "127.0.0.1:6390",
	}

	fake := fakeEnv{
		env:  env,
		root: root,
		exec: func(_ string, args ...string) (string, error) {
			switch args[0] {
			case "acl":
				checkSub := args[1]
				switch checkSub {
				case "check":
					user := args[5]
					if user == "coordinator" {
						return "ACL MISSING user bench role member\n", nil
					}
					return "ACL CHECK OK users 1 library abc123 store 127.0.0.1:6390\n", nil
				}
			}
			return "", nil
		},
	}

	reg := NewRegistry()
	reg.Register(Default.checks["redis-stores"])
	res, _, err := reg.Run(context.Background(), fake, Options{})
	require.NoError(t, err)
	require.Len(t, res, 1)

	assert.Equal(t, Fail, res[0].Status)
	assert.Contains(t, res[0].Evidence, "missing")
	assert.Contains(t, res[0].Fix, "nova-redis acl apply")
}
