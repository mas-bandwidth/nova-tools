package doctor

import (
	"context"
	"fmt"
	"strings"
)

const redisStoreDoc = "docs/SETUP.md, dep-redis-stores-b.w7"

func init() {
	Default.Register(Check{
		Name:       "redis-stores",
		Dependency: "the Redis stores and their ACL users",
		Fleet:      true,
		Run:        checkRedisStores,
	})
}

func checkRedisStores(ctx context.Context, env Env) Result {
	store := env.Getenv("NOVA_SECRETS_STORE")
	seat := env.Getenv("NOVA_SECRETS_SEAT")
	if store == "" || seat == "" {
		return Result{Status: Fail,
			Evidence: "NOVA_SECRETS_STORE and NOVA_SECRETS_SEAT are not set, so the secrets store and seat are unknown",
			Fix:      "run nova-up --local to set up the secrets store and seat (" + redisStoreDoc + ")"}
	}

	addr := env.Getenv("NOVA_REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6390"
	}

	user := "coordinator"
	pwVar := "NOVA_REDIS_PASSWORD"

	out, err := env.Exec(ctx, "nova-redis", "acl", "check",
		"--redis", addr, "--user", user, "--password-env", pwVar)
	if err != nil {
		return Result{Status: Fail,
			Evidence: "could not run nova-redis acl check for store " + addr + ": " + err.Error(),
			Fix:      "ensure the Redis store at " + addr + " is running and accessible (" + redisStoreDoc + ")"}
	}

	var missing, drifted []string
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ACL MISSING") {
			parts := strings.Fields(trimmed)
			userName := extractUser(parts)
			if userName != "" {
				missing = append(missing, userName)
			}
		} else if strings.HasPrefix(trimmed, "ACL DRIFT") {
			parts := strings.Fields(trimmed)
			userName := extractUser(parts)
			if userName != "" {
				drifted = append(drifted, userName)
			}
		}
	}

	if len(drifted) > 0 {
		userList := strings.Join(drifted, ", ")
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("ACL DRIFT for users %s on store %s: rules differ from expected", userList, addr),
			Fix:      "nova-redis acl apply --redis " + addr + " --user " + user + " --password-env " + pwVar + " (sets the users that differ)"}
	}

	if len(missing) > 0 {
		userList := strings.Join(missing, ", ")
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("%d user(s) missing from ACL on store %s: %s", len(missing), addr, userList),
			Fix:      "nova-redis acl apply --redis " + addr + " --user " + user + " --password-env " + pwVar + " (creates the missing user(s) " + userList + ")"}
	}

	return Result{Status: OK,
		Evidence: "all ACL users present for store " + addr,
		Fix:      ""}
}

func extractUser(fields []string) string {
	for i, f := range fields {
		if f == "user" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}
