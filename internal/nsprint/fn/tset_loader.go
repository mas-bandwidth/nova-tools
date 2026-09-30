package fn

import (
	"context"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

// TSetProfile chooses the installed tset writer. The profile is baked into the
// Redis Function source; a request cannot select a less restricted writer.
type TSetProfile string

const (
	TSetStandalone TSetProfile = "l1_only"
	TSetComposed   TSetProfile = "composed"
)

// The tset library deliberately has no glob. A newly embedded Lua file is not
// part of this writer surface until it is reviewed and added here.
var tsetFragments = []string{
	"lua/table_set.lua",
	"lua/table_set_log.lua",
	"lua/table_set_read.lua",
	"lua/table_set_receipt.lua",
	"lua/table_set_rows.lua",
	"lua/table_set_validate.lua",
}

// These are the read-only registrations from the unchanged table.lua that
// watch/check callers still need. Its other callbacks are built as closures,
// but the lexical Redis shim never registers them with the server.
var tsetLegacyReaders = []string{
	"ns_table_check",
	"ns_table_list",
	"ns_table_member_find",
	"ns_table_members",
	"ns_table_read",
	"ns_table_read_set",
	"ns_view_get",
	"ns_view_list",
}

// TSetSource assembles an isolated nova_sprint library with the tset writer
// and only the required legacy read callbacks. table.lua's bytes are included
// unchanged. The lexical shim filters every registration at library load
// time; no legacy mutator can be reached by FCALL on this server.
func TSetSource(profile TSetProfile) (string, error) {
	if profile != TSetStandalone && profile != TSetComposed {
		return "", fmt.Errorf("unknown tset profile %q", profile)
	}
	var b strings.Builder
	b.WriteString("#!lua name=" + Library + "\n")
	b.WriteString("local NS = {tset_profile = '")
	b.WriteString(string(profile))
	b.WriteString("'}\n")
	// The lexical redis shim is visible to every file block. table.lua adds
	// another local shim internally; its closures capture this one. Redis gives
	// FUNCTION LOAD and FCALL different global redis facades: retain the former
	// only for registration, and resolve the latter when a callback runs.
	// Defining this helper before the local redis declaration binds its redis
	// reference to the runtime global, not to our registration filter.
	b.WriteString("local function runtime_redis() return redis end\n")
	b.WriteString("local native_redis = redis\n")
	b.WriteString("local redis = {\n")
	b.WriteString("  call = function(...) return runtime_redis().call(...) end,\n")
	b.WriteString("  pcall = function(...) return runtime_redis().pcall(...) end,\n")
	b.WriteString("  sha1hex = function(...) return runtime_redis().sha1hex(...) end,\n")
	b.WriteString("  acl_check_cmd = function(...) return runtime_redis().acl_check_cmd(...) end,\n")
	b.WriteString("  register_function = function(spec, callback)\n")
	b.WriteString("    local name = callback and spec or spec.function_name\n")
	b.WriteString("    if name == 'ns_tset_step' or name == 'ns_tset_read' or\n       ")
	for i, name := range tsetLegacyReaders {
		if i != 0 {
			b.WriteString(" or\n       ")
		}
		b.WriteString("name == '")
		b.WriteString(name)
		b.WriteString("'")
	}
	b.WriteString(" then\n")
	b.WriteString("      if callback == nil then return native_redis.register_function(spec) end\n")
	b.WriteString("      return native_redis.register_function(spec, callback)\n")
	b.WriteString("    end\n")
	b.WriteString("  end,\n}\n")

	legacy, err := sources.ReadFile("lua/table.lua")
	if err != nil {
		return "", fmt.Errorf("read tset legacy readers: %w", err)
	}
	if strings.TrimSpace(string(legacy)) == "" {
		return "", fmt.Errorf("empty tset legacy readers")
	}
	b.WriteString("\n" + fileHeader + "lua/table.lua\n" + blockOpen + "\n")
	b.Write(legacy)
	b.WriteString("\n" + blockClose + "lua/table.lua\n")

	for _, name := range tsetFragments {
		if name == "lua/table_set_log.lua" && profile != TSetComposed {
			continue
		}
		if err := appendTSetFragment(&b, name); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

func appendTSetFragment(b *strings.Builder, name string) error {
	fragment, err := sources.ReadFile(name)
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	if strings.TrimSpace(string(fragment)) == "" {
		return fmt.Errorf("empty function file %s", name)
	}
	b.WriteString("\n" + fileHeader + name + "\n" + blockOpen + "\n")
	b.Write(fragment)
	b.WriteString("\n" + blockClose + name + "\n")
	return nil
}

// LoadTSet is for an isolated standalone tset server. It never replaces an
// existing different nova_sprint library: replacing a legacy server here would
// remove its writers, while installing the legacy library on a tset server
// would expose unsupported mutation callbacks.
func LoadTSet(ctx context.Context, client *redis.Client, profile TSetProfile) error {
	source, err := TSetSource(profile)
	if err != nil {
		return err
	}
	info, err := client.Info(ctx, "server").Result()
	if err != nil {
		return fmt.Errorf("inspect tset load target: %w", err)
	}
	if !strings.Contains("\n"+info, "\nredis_mode:standalone\r\n") &&
		!strings.Contains("\n"+info, "\nredis_mode:standalone\n") {
		return fmt.Errorf("tset function library requires a standalone Redis server")
	}
	libs, err := client.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: Library, WithCode: true}).Result()
	if err != nil {
		return fmt.Errorf("inspect existing %s library: %w", Library, err)
	}
	for _, lib := range libs {
		if lib.Name == Library {
			if lib.Code == source {
				return nil
			}
			return fmt.Errorf("refuse to replace existing %s library with tset %s profile", Library, profile)
		}
	}
	if err := client.FunctionLoad(ctx, source).Err(); err != nil {
		return fmt.Errorf("load %s tset %s function library: %w", Library, profile, err)
	}
	return nil
}
