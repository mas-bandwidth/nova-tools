// Package fleet implements the nova-sprint fleet presence and state machine (#2046).
// It maintains UP/DOWN/PROBING/HELD state in Redis across passes and calls.
package fleet

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

const (
	StateUp      = "UP"
	StateDown    = "DOWN"
	StateProbing = "PROBING"
	StateHeld    = "HELD"

	FunctionConfig  = "ns_fleet_config"
	FunctionHold    = "ns_fleet_hold"
	FunctionRelease = "ns_fleet_release"
	FunctionRead    = "ns_fleet_read"
	FunctionStep    = "ns_fleet_step"
)

var (
	ErrUnregistered = errors.New("unregistered bench")
	ErrNotHeld      = errors.New("bench not held")
	ErrUsage        = errors.New("fleet usage")
)

// StateRow is one bench's row in fleet state.
type StateRow struct {
	Bench  string
	State  string
	Since  string
	Build  string
	Reason string
}

// Config returns the current fleet down_after and up_after thresholds.
func Config(ctx context.Context, client *redis.Client) (downAfter, upAfter int, err error) {
	reply, err := client.FCall(ctx, FunctionConfig, nil).StringSlice()
	if err != nil {
		return 0, 0, fmt.Errorf("fleet config: %w", err)
	}
	if len(reply) < 3 || reply[0] != "OK" {
		return 0, 0, fmt.Errorf("fleet config: unexpected reply %v", reply)
	}
	downAfter, err = strconv.Atoi(reply[1])
	if err != nil {
		return 0, 0, fmt.Errorf("fleet config down_after %q: %w", reply[1], err)
	}
	upAfter, err = strconv.Atoi(reply[2])
	if err != nil {
		return 0, 0, fmt.Errorf("fleet config up_after %q: %w", reply[2], err)
	}
	return downAfter, upAfter, nil
}

// SetConfig sets the fleet down_after and up_after thresholds. Values < 1 are refused.
func SetConfig(ctx context.Context, client *redis.Client, downAfter, upAfter int) error {
	if downAfter < 1 {
		return fmt.Errorf("down_after must be >= 1")
	}
	if upAfter < 1 {
		return fmt.Errorf("up_after must be >= 1")
	}
	reply, err := client.FCall(ctx, FunctionConfig, nil, strconv.Itoa(downAfter), strconv.Itoa(upAfter)).StringSlice()
	if err != nil {
		return fmt.Errorf("fleet config: %w", err)
	}
	if len(reply) < 1 || reply[0] != "OK" {
		return fmt.Errorf("fleet config: unexpected reply %v", reply)
	}
	return nil
}

// Hold marks bench as HELD with the given why reason and optional by actor.
func Hold(ctx context.Context, client *redis.Client, bench, why, by string) error {
	if bench == "" || why == "" {
		return fmt.Errorf("%w: --bench and --why are required", ErrUsage)
	}
	reply, err := client.FCall(ctx, FunctionHold, nil, bench, why, by).StringSlice()
	if err != nil {
		return fmt.Errorf("fleet hold: %w", err)
	}
	if len(reply) == 0 {
		return fmt.Errorf("fleet hold: empty reply")
	}
	switch reply[0] {
	case "OK":
		return nil
	case "UNREGISTERED":
		return ErrUnregistered
	case "USAGE":
		return ErrUsage
	default:
		return fmt.Errorf("fleet hold: %s", reply[0])
	}
}

// Release marks a HELD bench as PROBING. It refuses with ErrNotHeld if the bench is not HELD.
func Release(ctx context.Context, client *redis.Client, bench string) error {
	if bench == "" {
		return fmt.Errorf("%w: --bench is required", ErrUsage)
	}
	reply, err := client.FCall(ctx, FunctionRelease, nil, bench).StringSlice()
	if err != nil {
		return fmt.Errorf("fleet release: %w", err)
	}
	if len(reply) == 0 {
		return fmt.Errorf("fleet release: empty reply")
	}
	switch reply[0] {
	case "OK":
		return nil
	case "NOTHELD":
		return ErrNotHeld
	case "UNREGISTERED":
		return ErrUnregistered
	case "USAGE":
		return ErrUsage
	default:
		return fmt.Errorf("fleet release: %s", reply[0])
	}
}

// Read returns fleet state rows. If bench is non-empty, only that bench is read.
func Read(ctx context.Context, client *redis.Client, bench string) ([]StateRow, error) {
	var args []any
	if bench != "" {
		args = append(args, bench)
	}
	reply, err := client.FCallRO(ctx, FunctionRead, nil, args...).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("fleet read: %w", err)
	}
	if len(reply) == 0 {
		return nil, fmt.Errorf("fleet read: empty reply")
	}
	if reply[0] == "UNREGISTERED" {
		return nil, ErrUnregistered
	}
	if reply[0] != "OK" {
		return nil, fmt.Errorf("fleet read: %s", reply[0])
	}
	var rows []StateRow
	for i := 1; i+4 < len(reply); i += 5 {
		rows = append(rows, StateRow{
			Bench:  reply[i],
			State:  reply[i+1],
			Since:  reply[i+2],
			Build:  reply[i+3],
			Reason: reply[i+4],
		})
	}
	return rows, nil
}

// Step advances the fleet state machine by one pass for all benches (or the specified benches).
func Step(ctx context.Context, client *redis.Client, benches ...string) error {
	var args []any
	for _, b := range benches {
		if b != "" {
			args = append(args, b)
		}
	}
	reply, err := client.FCall(ctx, FunctionStep, nil, args...).StringSlice()
	if err != nil {
		return fmt.Errorf("fleet step: %w", err)
	}
	if len(reply) == 0 || reply[0] != "OK" {
		return fmt.Errorf("fleet step: unexpected reply %v", reply)
	}
	return nil
}
