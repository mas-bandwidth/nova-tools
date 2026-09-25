package consume

import (
	"context"
	"testing"
	"time"
    "os"
)

func TestRouteConsumeSweep(t *testing.T) {
	st, client := controlRedis(t)
	sprint := "control-sweep1"
	seedSprint(t, client, sprint)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

    host, _ := os.Hostname()
    
    for i := 0; i < 100; i++ {
        // Run router briefly
        instance, _ := NewInstance()
        ok := &OkFriend{Store: st, Sprint: sprint, Consumer: host, Actor: "test"}
        report := &Report{Store: st, Sprint: sprint, Consumer: host, Actor: "test"}
        
        router := &Router{
            Store: st, Sprint: sprint, Instance: instance, Host: host,
            Rules: Rules(ok, report, nil, nil, nil),
        }
        
        runCtx, cancelRun := context.WithTimeout(ctx, 10*time.Millisecond)
        _ = router.Run(runCtx)
        cancelRun()
    }
    
    // Check consumers
    consumers, err := client.XInfoConsumers(ctx, "s:"+sprint+":log", GroupOkFriend).Result()
    if err != nil {
        t.Fatal(err)
    }
    if len(consumers) > 2 {
        t.Fatalf("expected <= 2 consumers, got %d", len(consumers))
    }
}
