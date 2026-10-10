package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctorJevCheckWarnsWithoutTheKeyAndNeverPrintsIt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))

	env := map[string]string{
		"PATH":               bin,
		"NOVA_SECRETS_STORE": root + "/store",
		"NOVA_SECRETS_SEAT":  "coordinator",
	}

	fake := fakeEnv{
		env:  env,
		root: root,
		exec: func(_ string, args ...string) (string, error) {
			switch args[0] {
			case "names":
				// JEV_API_KEY is not in the listing
				return "SECRETS NAMES OK\n", nil
			case "curl":
				// Endpoint returns 200
				return "200", nil
			}
			return "", nil
		},
	}

	reg := NewRegistry()
	reg.Register(Default.checks["jev"])
	res, _, err := reg.Run(context.Background(), fake, Options{})
	require.NoError(t, err)
	require.Len(t, res, 1)

	assert.Equal(t, Warn, res[0].Status)
	assert.Contains(t, res[0].Evidence, "JEV_API_KEY")
	assert.Contains(t, res[0].Evidence, "not in the secrets store")
	assert.Contains(t, res[0].Fix, "nova-secrets seal")
	// Verify the key itself is not printed
	assert.NotContains(t, res[0].Evidence, "JEV_API_KEY=")
	assert.NotContains(t, res[0].Fix, "JEV_API_KEY=")
}

func TestDoctorJevCheckOKWithKeyAndHealthyEndpoint(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))

	env := map[string]string{
		"PATH":               bin,
		"NOVA_SECRETS_STORE": root + "/store",
		"NOVA_SECRETS_SEAT":  "coordinator",
	}

	fake := fakeEnv{
		env:  env,
		root: root,
		exec: func(_ string, args ...string) (string, error) {
			switch args[0] {
			case "names":
				// JEV_API_KEY is in the listing
				return "SECRETS NAME key=JEV_API_KEY clear=false\nSECRETS NAMES OK\n", nil
			case "curl":
				// Endpoint returns 200
				return "200", nil
			}
			return "", nil
		},
	}

	reg := NewRegistry()
	reg.Register(Default.checks["jev"])
	res, _, err := reg.Run(context.Background(), fake, Options{})
	require.NoError(t, err)
	require.Len(t, res, 1)

	assert.Equal(t, OK, res[0].Status)
	assert.Contains(t, res[0].Evidence, "JEV_API_KEY")
}

func TestDoctorJevCheckFailsWithoutStore(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))

	env := map[string]string{
		"PATH": bin,
	}

	fake := fakeEnv{
		env:  env,
		root: root,
		exec: func(_ string, args ...string) (string, error) {
			return "", nil
		},
	}

	reg := NewRegistry()
	reg.Register(Default.checks["jev"])
	res, _, err := reg.Run(context.Background(), fake, Options{})
	require.NoError(t, err)
	require.Len(t, res, 1)

	assert.Equal(t, Fail, res[0].Status)
	assert.Contains(t, res[0].Evidence, "NOVA_SECRETS_STORE")
}

func TestDoctorJevCheckWarnsWithUnhealthyEndpoint(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))

	env := map[string]string{
		"PATH":               bin,
		"NOVA_SECRETS_STORE": root + "/store",
		"NOVA_SECRETS_SEAT":  "coordinator",
	}

	fake := fakeEnv{
		env:  env,
		root: root,
		exec: func(_ string, args ...string) (string, error) {
			switch args[0] {
			case "names":
				return "SECRETS NAME key=JEV_API_KEY clear=false\nSECRETS NAMES OK\n", nil
			case "curl":
				// Endpoint returns 503
				return "503", nil
			}
			return "", nil
		},
	}

	reg := NewRegistry()
	reg.Register(Default.checks["jev"])
	res, _, err := reg.Run(context.Background(), fake, Options{})
	require.NoError(t, err)
	require.Len(t, res, 1)

	assert.Equal(t, Warn, res[0].Status)
	assert.Contains(t, res[0].Evidence, "503")
}
