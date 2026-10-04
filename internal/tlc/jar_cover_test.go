package tlc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJarCoverReadJavaVersionRefusesAJavaItCannotRun(t *testing.T) {
	t.Parallel()
	plain := filepath.Join(t.TempDir(), "not-a-program")
	require.NoError(t, os.WriteFile(plain, []byte("# not a program\n"), 0o644))
	rows := []struct {
		what string
		java string
		want string
	}{
		{"absent from PATH", "nova-tlc-jar-cover-absent-java", "executable file not found"},
		{"present but not executable", plain, "permission denied"},
	}
	for _, row := range rows {
		t.Run(row.what, func(t *testing.T) {
			t.Parallel()
			got, err := ReadJavaVersion(context.Background(), row.java)
			assert.ErrorContains(t, err, row.java+" -version failed")
			assert.ErrorContains(t, err, row.want)
			assert.Empty(t, got, "a refusal returns no version")
		})
	}
}
