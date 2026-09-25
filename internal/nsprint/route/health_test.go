package route_test

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/route"
)

func startTestRedis(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	l.Close()

	_, portStr, _ := net.SplitHostPort(addr)
	cmd := exec.Command("redis-server", "--port", portStr, "--save", "", "--appendonly", "no", "--protected-mode", "no")
	if err := cmd.Start(); err != nil {
		t.Skipf("redis-server not available or failed to start: %v", err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})

	for i := 0; i < 50; i++ {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			conn.Close()
			return addr
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("redis-server failed to start on %s", addr)
	return ""
}

func setupMockResults(t *testing.T, routeName string, gatewayDeath bool) string {
	dir := t.TempDir()
	resultMd := filepath.Join(dir, "RESULT.md")
	usageTsv := filepath.Join(dir, "usage.tsv")

	silentStr := ""
	if gatewayDeath {
		silentStr = "RESULT: SILENT\n"
	} else {
		silentStr = "RESULT: OK\n"
	}
	os.WriteFile(resultMd, []byte(silentStr), 0644)

	rc := "0"
	tin := "100"
	tout := "100"
	if gatewayDeath {
		rc = "1"
		tin = "0"
		tout = "0"
	}
	usageLine := fmt.Sprintf("%s\t%s\t%s\t%s\n", routeName, rc, tin, tout)
	os.WriteFile(usageTsv, []byte(usageLine), 0644)

	return dir
}

func TestRouteHealthBenchesOnGatewayDeathRate(t *testing.T) {
	redisAddr := startTestRedis(t)
	rName := "flash/model-x"

	t.Run("threshold", func(t *testing.T) {
		injectAttempts := func(total, deaths int) {
			for i := 0; i < total; i++ {
				isDeath := i < deaths
				dir := setupMockResults(t, rName, isDeath)
				_, err := card.CallEnd(dir, "S1", "label1", fmt.Sprintf("att-%d-%d", deaths, i), true, redisAddr)
				if err != nil {
					t.Fatalf("CallEnd failed: %v", err)
				}
			}
		}

		injectAttempts(50, 3)
		res, err := route.EvaluateHealth(rName, 50, 10.0, false, false, "", "", "picker", redisAddr)
		if err != nil {
			t.Fatalf("EvaluateHealth failed: %v", err)
		}
		if res.ExitCode != 0 {
			t.Errorf("3 deaths: exit code = %d, want 0 (OK)", res.ExitCode)
		}

		injectAttempts(50, 5)
		res, err = route.EvaluateHealth(rName, 50, 10.0, false, false, "", "", "picker", redisAddr)
		if err != nil {
			t.Fatalf("EvaluateHealth failed: %v", err)
		}
		if res.ExitCode != 0 {
			t.Errorf("5 deaths: exit code = %d, want 0 (OK)", res.ExitCode)
		}

		injectAttempts(50, 12)
		res, err = route.EvaluateHealth(rName, 50, 10.0, false, false, "", "", "picker", redisAddr)
		if err != nil {
			t.Fatalf("EvaluateHealth failed: %v", err)
		}
		if res.ExitCode != 1 {
			t.Errorf("12 deaths: exit code = %d, want 1 (BENCH)", res.ExitCode)
		}

		res, err = route.EvaluateHealth(rName, 50, 10.0, false, false, "", "", "picker", redisAddr)
		if res.ExitCode != 1 {
			t.Errorf("benched route should stay benched, got exit code %d", res.ExitCode)
		}

		res, err = route.EvaluateHealth(rName, 50, 10.0, false, false, "pass", "receipt-123", "reader1", redisAddr)
		if err != nil {
			t.Fatalf("probe pass failed: %v", err)
		}
		if res.ExitCode != 0 {
			t.Errorf("after probe pass, exit code = %d, want 0", res.ExitCode)
		}
	})

	t.Run("concurrent_once", func(t *testing.T) {
		rNameConcur := "flash/model-concur"
		dir := setupMockResults(t, rNameConcur, true)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = card.CallEnd(dir, "S2", "concur-label", "att-1", true, redisAddr)
		}()
		go func() {
			defer wg.Done()
			_, _ = card.CallEnd(dir, "S2", "concur-label", "att-1", true, redisAddr)
		}()
		wg.Wait()

		for i := 0; i < 15; i++ {
			dDir := setupMockResults(t, rNameConcur, true)
			_, _ = card.CallEnd(dir, "S2", fmt.Sprintf("label-%d", i), "att-1", true, redisAddr)
			_ = dDir
		}

		var wg2 sync.WaitGroup
		wg2.Add(2)
		var res1, res2 route.HealthResult
		go func() {
			defer wg2.Done()
			res1, _ = route.EvaluateHealth(rNameConcur, 50, 10.0, false, false, "", "", "picker1", redisAddr)
		}()
		go func() {
			defer wg2.Done()
			res2, _ = route.EvaluateHealth(rNameConcur, 50, 10.0, false, false, "", "", "picker2", redisAddr)
		}()
		wg2.Wait()

		if res1.ExitCode != 1 && res2.ExitCode != 1 {
			t.Errorf("expected benched exit code 1")
		}
	})

	t.Run("probe_dup", func(t *testing.T) {
		rNameProbe := "flash/model-probe"
		res1, err := route.EvaluateHealth(rNameProbe, 50, 10.0, false, false, "pass", "receipt-X", "reader1", redisAddr)
		if err != nil {
			t.Fatalf("first probe pass failed: %v", err)
		}
		if res1.ExitCode != 0 {
			t.Errorf("first probe pass exit = %d, want 0", res1.ExitCode)
		}

		res2, err := route.EvaluateHealth(rNameProbe, 50, 10.0, false, false, "pass", "receipt-X", "reader1", redisAddr)
		if err != nil {
			t.Fatalf("second probe pass failed: %v", err)
		}
		if res2.ExitCode != 0 {
			t.Errorf("second probe pass exit = %d, want 0", res2.ExitCode)
		}
	})
}
