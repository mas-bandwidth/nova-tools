package route

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint"
	_ "embed"
)

//go:embed card_run.lua
var cardRunLua string

//go:embed route_health.lua
var routeHealthLua string

type HealthResult struct {
	Route          string
	Attempts       int
	Deaths         int
	Rate           float64
	Threshold      float64
	Window         string
	Decision       string
	State          string
	BenchedAt      string
	By             string
	Why            string
	AlreadyApplied bool
	OutputLine     string
	ExitCode       int
}

func EvaluateHealth(route string, n int, threshold float64, dryRun bool, checkOnly bool, probe string, receipt string, asFriend string, redisAddr string) (HealthResult, error) {
	if n <= 0 {
		n = 50
	}
	if threshold <= 0 {
		threshold = 10.0
	}

	client, err := nsprint.ConnectRedis(redisAddr)
	if err != nil {
		return HealthResult{ExitCode: 2}, fmt.Errorf("redis down: %w", err)
	}
	defer client.Close()

	if err := client.LoadFunctions(cardRunLua, routeHealthLua); err != nil {
		return HealthResult{ExitCode: 2}, err
	}

	if probe != "" {
		if receipt == "" {
			return HealthResult{ExitCode: 2}, fmt.Errorf("--probe requires --receipt")
		}
		if probe != "pass" && probe != "fail" {
			return HealthResult{ExitCode: 2}, fmt.Errorf("--probe must be pass or fail")
		}
		if asFriend == "" {
			return HealthResult{ExitCode: 2}, fmt.Errorf("--as <friend> is required")
		}

		_, err := client.Do("FCALL", "ns_route_probe", "0", route, receipt, probe, asFriend)
		if err != nil {
			return HealthResult{ExitCode: 2}, err
		}

		benchResp, _ := client.Do("HGETALL", "route:"+route+":bench")
		state := "ok"
		if benchResp != nil {
			if arr, ok := benchResp.([]any); ok {
				for i := 0; i < len(arr); i += 2 {
					if k, ok := arr[i].(string); ok && k == "state" {
						if v, ok := arr[i+1].(string); ok {
							state = v
						}
					}
				}
			}
		}

		exitCode := 0
		if state == "benched" {
			exitCode = 1
		}
		return HealthResult{Route: route, State: state, ExitCode: exitCode}, nil
	}

	attemptsResp, err := client.Do("XREVRANGE", "route:"+route+":attempts", "+", "-", "COUNT", strconv.Itoa(n))
	if err != nil {
		return HealthResult{ExitCode: 2}, err
	}

	benchResp, err := client.Do("HGETALL", "route:"+route+":bench")
	if err != nil {
		return HealthResult{ExitCode: 2}, err
	}

	currentState := "ok"
	benchedAt := ""
	by := ""
	why := ""
	if benchResp != nil {
		if arr, ok := benchResp.([]any); ok {
			for i := 0; i < len(arr); i += 2 {
				k, _ := arr[i].(string)
				v, _ := arr[i+1].(string)
				switch k {
				case "state":
					currentState = v
				case "benched_at":
					benchedAt = v
				case "by":
					by = v
				case "why":
					why = v
				case "window":
					// storedWindow = v
				}
			}
		}
	}

	var attempts []map[string]string
	newestWindow := ""

	if attemptsResp != nil {
		if arr, ok := attemptsResp.([]any); ok {
			for idx, item := range arr {
				entry, ok := item.([]any)
				if !ok || len(entry) < 2 {
					continue
				}
				streamID, _ := entry[0].(string)
				if idx == 0 {
					newestWindow = streamID
				}
				fields, ok := entry[1].([]any)
				if !ok {
					continue
				}
				m := make(map[string]string)
				for fi := 0; fi < len(fields); fi += 2 {
					fk, _ := fields[fi].(string)
					fv, _ := fields[fi+1].(string)
					m[fk] = fv
				}
				attempts = append(attempts, m)
			}
		}
	}

	a := len(attempts)
	d := 0
	for _, att := range attempts {
		if att["bench"] == "1" {
			d++
		}
	}

	var rate float64
	if a > 0 {
		rate = float64(d) / float64(a) * 100.0
	}

	decision := "OK"
	if a > 0 && int64(d)*1000 > int64(a)*int64(threshold)*10 {
		decision = "BENCH"
	}

	alreadyApplied := false
	if decision == "BENCH" && !dryRoute(dryRun) && newestWindow != "" {
		if asFriend == "" && !checkOnly {
			return HealthResult{ExitCode: 2}, fmt.Errorf("--as <friend> is required for applying bench")
		}
		applyBy := asFriend
		if applyBy == "" {
			applyBy = "picker"
		}

		fcallResp, err := client.Do("FCALL", "ns_route_health_apply", "0", route, newestWindow, "BENCH", strconv.Itoa(d), strconv.Itoa(a), fmt.Sprintf("%.1f", rate), fmt.Sprintf("%.1f", threshold), applyBy)
		if err == nil {
			if s, ok := fcallResp.(string); ok {
				if s == "ALREADY_BENCHED" || s == "SAME" {
					alreadyApplied = true
				}
			}
		}

		benchResp2, _ := client.Do("HGETALL", "route:"+route+":bench")
		if benchResp2 != nil {
			if arr, ok := benchResp2.([]any); ok {
				for i := 0; i < len(arr); i += 2 {
					k, _ := arr[i].(string)
					v, _ := arr[i+1].(string)
					if k == "state" {
						currentState = v
					}
					if k == "benched_at" {
						benchedAt = v
					}
					if k == "by" {
						by = v
					}
					if k == "why" {
						why = v
					}
				}
			}
		}
	}

	windowStr := newestWindow
	if windowStr == "" {
		windowStr = "none"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("ROUTE %s attempts=%d gateway_deaths=%d rate=%.1f%% threshold=%.1f%% window=%s -> %s",
		route, a, d, rate, threshold, windowStr, decision))

	if currentState == "benched" || benchedAt != "" {
		sb.WriteString(fmt.Sprintf(" [benched_at=%s by=%s why=%s]", benchedAt, by, why))
	}
	if a == 0 {
		sb.WriteString(" [no attempts recorded]")
	}
	if alreadyApplied {
		sb.WriteString(" (already applied)")
	}

	exitCode := 0
	if decision == "BENCH" || currentState == "benched" {
		exitCode = 1
	}

	return HealthResult{
		Route:          route,
		Attempts:       a,
		Deaths:         d,
		Rate:           rate,
		Threshold:      threshold,
		Window:         windowStr,
		Decision:       decision,
		State:          currentState,
		AlreadyApplied: alreadyApplied,
		OutputLine:     sb.String(),
		ExitCode:       exitCode,
	}, nil
}

func dryRoute(dry bool) bool {
	return dry
}

func ResolveRedisAddr(flagRedis string) string {
	return nsprint.ResolveRedisAddr(flagRedis)
}
