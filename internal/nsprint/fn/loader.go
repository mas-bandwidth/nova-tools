// Package fn assembles and installs the nova-sprint Redis Function library.
package fn

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/redis/go-redis/v9"
)

const Library = "nova_sprint"

//go:embed lua/*.lua
var sources embed.FS

// Source builds one library from all verb files. A verb is added by placing a
// Lua file in lua/; no central Lua registry needs to change.
func Source() (string, error) {
	names, err := fs.Glob(sources, "lua/*.lua")
	if err != nil {
		return "", fmt.Errorf("list sprint functions: %w", err)
	}
	if len(names) == 0 {
		return "", fmt.Errorf("nova_sprint has no function files")
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("#!lua name=" + Library + "\n")
	for _, name := range names {
		fragment, err := sources.ReadFile(name)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", name, err)
		}
		if strings.TrimSpace(string(fragment)) == "" {
			return "", fmt.Errorf("empty function file %s", name)
		}
		b.WriteString("\n-- " + name + "\n")
		b.Write(fragment)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// Load installs the complete library atomically. REPLACE permits an updated
// binary to deploy its exact embedded version without a delete/load gap.
func Load(ctx context.Context, client *redis.Client) error {
	source, err := Source()
	if err != nil {
		return err
	}
	if err := client.FunctionLoadReplace(ctx, source).Err(); err != nil {
		return fmt.Errorf("load %s function library: %w", Library, err)
	}
	return nil
}
