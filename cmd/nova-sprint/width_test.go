package main

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/width"
	"github.com/redis/go-redis/v9"
)

func TestWidthVerbs(t *testing.T) {
	t.Run("V1", func(t *testing.T) {
		addr := testutil.Start(t)
		client := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = client.Close() })
		ctx := context.Background()
		if err := fn.Load(ctx, client); err != nil {
			t.Fatal(err)
		}

		st, err := store.Open(ctx, addr)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()

		client.SAdd(ctx, "friends", "f1", "f2", "f3")
		client.HSet(ctx, "friend:f1:desired", "slots", "4")
		client.HSet(ctx, "friend:f2:desired", "slots", "8")
		client.HSet(ctx, "friend:f3:desired", "slots", "2")

		timeVal, err := client.Time(ctx).Result()
		if err != nil {
			t.Fatal(err)
		}
		nowMs := timeVal.UnixMilli()

		for i := 1; i <= 4; i++ {
			client.ZAdd(ctx, "friend:f1:living", redis.Z{Score: float64(nowMs), Member: fmt.Sprintf("m%d", i)})
		}

		lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test", Instance: "c8"})
		if err != nil {
			t.Fatal(err)
		}
		duty := &width.Duty{Store: st}
		if _, err := duty.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}

		// Set f3 fillstate 4 seconds old
		client.HSet(ctx, "friend:f3:fillstate", "at", strconv.FormatInt(nowMs-4000, 10))

		// width on C8 fixture exits 0, includes ? row
		var stdout, stderr bytes.Buffer
		code := run([]string{"width", "--redis", addr}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("width code=%d stderr=%q", code, stderr.String())
		}
		outStr := stdout.String()
		wantF3 := "WIDTH f3 slots=? starting=? living=? leased=? working=? deficit=? eligible=? idle=? peak=?@? at=?"
		if !strings.Contains(outStr, wantF3) {
			t.Fatalf("width output does not contain stale ? line for f3: %q", outStr)
		}
		if !strings.Contains(outStr, "WIDTH fleet ") {
			t.Fatalf("width output does not contain fleet line: %q", outStr)
		}

		// width --as ghost exits 2, one nova-sprint width: stderr line, empty stdout
		stdout.Reset()
		stderr.Reset()
		code = run([]string{"width", "--as", "ghost", "--redis", addr}, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("width --as ghost exit code=%d; want 2", code)
		}
		if stdout.Len() != 0 {
			t.Fatalf("width --as ghost stdout=%q; want empty", stdout.String())
		}
		errLines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
		if len(errLines) != 1 || !strings.HasPrefix(errLines[0], "nova-sprint width: ") {
			t.Fatalf("width --as ghost stderr=%q; want one nova-sprint width: line", stderr.String())
		}

		// width with --redis at closed port exits 2
		stdout.Reset()
		stderr.Reset()
		code = run([]string{"width", "--redis", "127.0.0.1:1"}, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("width closed redis exit code=%d; want 2", code)
		}

		// positional argument exits 2
		stdout.Reset()
		stderr.Reset()
		code = run([]string{"width", "extra", "--redis", addr}, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("width positional argument exit code=%d; want 2", code)
		}
	})

	t.Run("V2", func(t *testing.T) {
		addr := testutil.Start(t)
		client := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = client.Close() })
		ctx := context.Background()
		if err := fn.Load(ctx, client); err != nil {
			t.Fatal(err)
		}

		sprint := "s1"
		client.SAdd(ctx, "sprints", sprint)
		client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
		client.HSet(ctx, "s:"+sprint, "status", "open")

		client.SAdd(ctx, "friends", "f1")
		client.HSet(ctx, "friend:f1:desired", "slots", "1")
		client.HSet(ctx, "friend:f1:beat", "at", "1")

		for i := 1; i <= 5; i++ {
			id := fmt.Sprintf("t%d", i)
			client.ZAdd(ctx, "s:"+sprint+":open:f1", redis.Z{Score: float64(i), Member: id})
			client.HSet(ctx, "s:"+sprint+":task:"+id, "state", "open", "kind", "work", "ref", id)
		}

		var out1, err1 bytes.Buffer
		var out2, err2 bytes.Buffer
		var code1, code2 int
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			code1 = run([]string{"task", "fill", "--as", "f1", "--sprint", sprint, "--redis", addr}, &out1, &err1)
		}()
		go func() {
			defer wg.Done()
			code2 = run([]string{"task", "fill", "--as", "f1", "--sprint", sprint, "--redis", addr}, &out2, &err2)
		}()
		wg.Wait()

		if code1 != 0 || code2 != 0 {
			t.Fatalf("concurrent fill exit codes: code1=%d err1=%q code2=%d err2=%q", code1, err1.String(), code2, err2.String())
		}

		var winnerOut, loserOut string
		if strings.Contains(out1.String(), "n=1") {
			winnerOut = out1.String()
			loserOut = out2.String()
		} else {
			winnerOut = out2.String()
			loserOut = out1.String()
		}

		if !strings.Contains(winnerOut, "FILL ") || !strings.Contains(winnerOut, "FILLED f1 n=1") {
			t.Fatalf("winner output unexpected: %q", winnerOut)
		}
		if strings.Contains(loserOut, "FILL ") || !strings.Contains(loserOut, "FILLED f1 n=0") {
			t.Fatalf("the C2 loser did not print FILLED f1 n=0: %q", loserOut)
		}

		// task fill with no --as exits 2 and prints no FILL line
		var outNoAs, errNoAs bytes.Buffer
		codeNoAs := run([]string{"task", "fill", "--sprint", sprint, "--redis", addr}, &outNoAs, &errNoAs)
		if codeNoAs != 2 {
			t.Fatalf("task fill no --as exit code=%d; want 2", codeNoAs)
		}
		if strings.Contains(outNoAs.String(), "FILL ") {
			t.Fatalf("task fill no --as printed FILL line: %q", outNoAs.String())
		}

		// --max 0 exits 2
		var outMax0, errMax0 bytes.Buffer
		codeMax0 := run([]string{"task", "fill", "--as", "f1", "--max", "0", "--sprint", sprint, "--redis", addr}, &outMax0, &errMax0)
		if codeMax0 != 2 {
			t.Fatalf("task fill --max 0 exit code=%d; want 2", codeMax0)
		}
	})

	t.Run("V3", func(t *testing.T) {
		addr := testutil.Start(t)
		client := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = client.Close() })
		ctx := context.Background()
		if err := fn.Load(ctx, client); err != nil {
			t.Fatal(err)
		}

		// task width --as a exits 2 (unknown subverb)
		var stdout, stderr bytes.Buffer
		code := run([]string{"task", "width", "--as", "a", "--redis", addr}, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("task width exit code=%d; want 2", code)
		}
		if !strings.Contains(stderr.String(), "unknown subverb width") {
			t.Fatalf("task width stderr=%q; want unknown subverb width", stderr.String())
		}

		// capacity friend --as op --machine m1 a 8
		client.SAdd(ctx, "friends", "a")
		stdout.Reset()
		stderr.Reset()
		if code := run([]string{"capacity", "machine", "--redis", addr, "--as", "op", "m1", "64"}, &stdout, &stderr); code != 0 {
			t.Fatalf("capacity machine code=%d: %s", code, stderr.String())
		}
		if code := run([]string{"capacity", "friend", "--redis", addr, "--as", "op", "--machine", "m1", "a", "8"}, &stdout, &stderr); code != 0 {
			t.Fatalf("capacity friend code=%d: %s", code, stderr.String())
		}

		st, err := store.Open(ctx, addr)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()

		lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test", Instance: "v3"})
		if err != nil {
			t.Fatal(err)
		}
		duty := &width.Duty{Store: st}
		if _, err := duty.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}

		stdout.Reset()
		stderr.Reset()
		code = run([]string{"width", "--as", "a", "--redis", addr}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("width --as a code=%d stderr=%q", code, stderr.String())
		}

		lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
		if len(lines) != 1 {
			t.Fatalf("width --as a printed %d lines; want exactly 1: %q", len(lines), stdout.String())
		}
		re := regexp.MustCompile(`^WIDTH a slots=8 starting=\d+ living=\d+ leased=\d+ working=\d+ deficit=\d+ `)
		if !re.MatchString(lines[0]) {
			t.Fatalf("line %q does not match %s", lines[0], re.String())
		}

		gw, err := task.GetWidth(ctx, st, "a")
		if err != nil {
			t.Fatal(err)
		}
		fsData, ok, err := width.ReadFillstate(ctx, st, "a")
		if err != nil || !ok {
			t.Fatalf("read fillstate: ok=%v err=%v", ok, err)
		}
		if fsData.Slots != gw.Desired || fsData.Starting != gw.Starting || fsData.Living != gw.Living || fsData.Leased != gw.Leased || fsData.Deficit != gw.Free {
			t.Fatalf("mismatch with task.GetWidth: fillstate=%+v getWidth=%+v", fsData, gw)
		}
	})

	t.Run("V4", func(t *testing.T) {
		addr := testutil.Start(t)
		client := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = client.Close() })
		ctx := context.Background()
		if err := fn.Load(ctx, client); err != nil {
			t.Fatal(err)
		}
		seed(t, addr, table.DefectFixture())

		st, err := store.Open(ctx, addr)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()

		lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test", Instance: "v4"})
		if err != nil {
			t.Fatal(err)
		}
		duty := &width.Duty{Store: st}
		if _, err := duty.Run(ctx, lease); err != nil {
			t.Fatal(err)
		}

		var stdout, stderr bytes.Buffer
		code := run([]string{"table", "--check", "--redis", addr}, &stdout, &stderr)
		if code != 0 || strings.Contains(stdout.String(), "two writers:") || strings.Contains(stderr.String(), "two writers:") {
			t.Fatalf("table --check failed: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}

		if client.Exists(ctx, "friend:eight:width").Val() != 0 {
			t.Fatalf("friend:eight:width exists; want 0")
		}
		slots := client.HGet(ctx, "friend:eight:fillstate", "slots").Val()
		if slots != "8" {
			t.Fatalf("friend:eight:fillstate slots=%q; want 8", slots)
		}

		// Negative control: SET friend:a:width 9
		client.SAdd(ctx, "friends", "a")
		client.Set(ctx, "friend:a:width", "9", 0)
		stdout.Reset()
		stderr.Reset()
		code = run([]string{"table", "--check", "--redis", addr}, &stdout, &stderr)
		if code == 0 || (!strings.Contains(stdout.String(), "two writers: friend:a:width") && !strings.Contains(stderr.String(), "two writers: friend:a:width")) {
			t.Fatalf("negative control failed: expected non-zero exit and 'two writers: friend:a:width', got code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
}
