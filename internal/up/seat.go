package up

import (
	"fmt"
	"strings"
)

// The seat step: the coordinator's seat as one file of variables, the
// sprint's store and actor, the secrets store, seat and key, and the Redis
// address and the name of the coordinator's password secret, so a
// coordinator's shell starts with `set -a; . <root>/seat.env; set +a`
// (docs/SPEC-UP.md "Steps", 7; docs/SPRINT-COORDINATOR-SEAT.md).
func init() { Register(Step{Name: "seat", Order: 70, Plan: planSeat, Apply: applySeat}) }

func seatVars(e *Env) []byte {
	var b strings.Builder
	b.WriteString("# the coordinator's seat, written by nova-up --local; load it with: set -a; . " + e.Path(SeatFile) + "; set +a\n")
	for _, kv := range append(sprintEnv(e.Path(SprintTwin)),
		"NOVA_SECRETS_STORE="+e.Path(SecretsDir),
		"NOVA_SECRETS_SEAT="+Seat,
		"NOVA_SECRETS_KEY="+e.Path(KeyFile()),
		"NOVA_REDIS_ADDR="+RedisAddr(),
		"NOVA_REDIS_USER="+RedisUsers[0],
		"NOVA_REDIS_PASSWORD_ENV="+PasswordName(RedisUsers[0]),
		"JEV_API_KEY=",
	) {
		k, v, _ := strings.Cut(kv, "=")
		fmt.Fprintf(&b, "%s='%s'\n", k, strings.ReplaceAll(v, "'", `'\''`))
	}
	return []byte(b.String())
}

func planSeat(e *Env) Finding {
	return Finding{fileState(e.Path(SeatFile), seatVars(e)), e.Path(SeatFile) + " seat=" + Seat}
}

func applySeat(e *Env) error { return writeFile(e.Path(SeatFile), seatVars(e), 0o600) }
