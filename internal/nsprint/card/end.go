package card

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint"
	_ "embed"
)

//go:embed card_run.lua
var cardRunLua string

//go:embed route_health.lua
var routeHealthLua string

type AttemptResult struct {
	Route     string
	RC        int
	TokensIn  int
	TokensOut int
	Silent    bool
	Class     string
}

func ReadResult(resultsDir string) (AttemptResult, error) {
	res := AttemptResult{
		Class: "ok",
	}

	resultMdPath := filepath.Join(resultsDir, "RESULT.md")
	f, err := os.Open(resultMdPath)
	if err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		if scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "RESULT: SILENT") || strings.Contains(line, "SILENT") {
				res.Silent = true
			}
		}
	}

	usagePath := filepath.Join(resultsDir, "usage.tsv")
	uf, err := os.Open(usagePath)
	if err == nil {
		defer uf.Close()
		var lastLine string
		scanner := bufio.NewScanner(uf)
		for scanner.Scan() {
			text := scanner.Text()
			if strings.TrimSpace(text) != "" {
				lastLine = text
			}
		}
		if lastLine != "" {
			parts := strings.Split(lastLine, "\t")
			if len(parts) >= 4 {
				res.Route = parts[0]
				if rc, err := strconv.Atoi(parts[1]); err == nil {
					res.RC = rc
				}
				if tin, err := strconv.Atoi(parts[2]); err == nil {
					res.TokensIn = tin
				}
				if tout, err := strconv.Atoi(parts[3]); err == nil {
					res.TokensOut = tout
				}
			} else if len(parts) >= 1 {
				res.Route = parts[0]
			}
		}
	}

	return res, nil
}

func IsGatewayDeath(res AttemptResult) bool {
	if res.Silent {
		return true
	}
	if res.RC != 0 && res.TokensIn == 0 && res.TokensOut == 0 {
		return true
	}
	return false
}

func CallEnd(resultsDir, sprint, label, attempt string, resolved bool, redisAddr string) (string, error) {
	res, err := ReadResult(resultsDir)
	if err != nil {
		return "", err
	}

	bench := "0"
	if IsGatewayDeath(res) {
		bench = "1"
	}

	resolvedStr := "0"
	if resolved {
		resolvedStr = "1"
	}

	client, err := nsprint.ConnectRedis(redisAddr)
	if err != nil {
		return "", err
	}
	defer client.Close()

	client.LoadFunctions(cardRunLua, routeHealthLua)

	rcStr := strconv.Itoa(res.RC)
	tinStr := strconv.Itoa(res.TokensIn)
	toutStr := strconv.Itoa(res.TokensOut)
	silentStr := "0"
	if res.Silent {
		silentStr = "1"
	}

	resp, err := client.Do("FCALL", "ns_card_end", "0",
		sprint, label, attempt, res.Route, rcStr, tinStr, toutStr, silentStr, res.Class, resolvedStr, bench)
	if err != nil {
		return "", err
	}

	if s, ok := resp.(string); ok {
		return s, nil
	}
	return fmt.Sprintf("%v", resp), nil
}
