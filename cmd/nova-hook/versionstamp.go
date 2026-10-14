package main

import (
	"context"
	"errors"
	"strings"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
	"github.com/redis/go-redis/v9"
)

// hookVersion is the version this nova-hook binary reports. It is the binary's
// own stamp (pkg/buildinfo), never a version this process invents.
func hookVersion() string { return buildinfo.Version(version) }

// versionHash is the one write used to store that version. The field is a
// label the client overwrites. It is not a state of the hook machine
// (tla/Hook.tla), so this write is one HSET and not a new machine.
type versionHash interface {
	HSet(ctx context.Context, key string, values ...interface{}) *redis.IntCmd
}

// stampVersion writes ver onto key's version field. The hook client stamps its
// own record; nova-hook serve stamps the server row. A blank is refused: an
// empty field is not a version this binary reported.
func stampVersion(ctx context.Context, c versionHash, key, ver string) error {
	ver = strings.TrimSpace(ver)
	if ver == "" {
		return errors.New("this binary has no version string to report")
	}
	if c == nil {
		return errors.New("the hook record has no store")
	}
	return c.HSet(ctx, key, "version", ver).Err()
}

// stampHookVersion writes this binary's version onto the session's hook record.
func (w world) stampHookVersion(s *session) error {
	ctx, cancel := call()
	defer cancel()
	return stampVersion(ctx, s.hooks.C, hook.RecordKey(s.as), hookVersion())
}
