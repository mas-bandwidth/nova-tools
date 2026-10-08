package main

import (
	"context"
	"encoding/json"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"sync"
	"sync/atomic"
	"time"
)

type sprintMachine struct {
	state string
	at    time.Time
}

// machineWords keeps one immutable control snapshot. Responses from superseded
// completed requests cannot erase a later STOP/PAUSE. Pending queries do not
// supersede a completed admission; otherwise overlapping beats could starve it.
type machineWords struct {
	mu        sync.Mutex
	requested uint64
	published uint64
	snapshot  atomic.Pointer[sprintMachine]
}

func (m *machineWords) Load() *sprintMachine { return m.snapshot.Load() }
func (m *machineWords) begin() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requested++
	return m.requested
}
func (m *machineWords) publish(request uint64, word *sprintMachine) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if request <= m.published {
		return false
	}
	m.published = request
	if word == nil {
		if old := m.snapshot.Load(); old != nil {
			m.snapshot.Store(&sprintMachine{state: old.state})
		}
		return false
	}
	m.snapshot.Store(word)
	return true
}
func admitMachineLaunch(ctx context.Context, words *machineWords, name string, ask func(context.Context, []string) (string, error)) bool {
	request := words.begin()
	if ask == nil {
		words.publish(request, nil)
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, friend.FinishWait)
	defer cancel()
	body, err := ask(ctx, append(friend.ReadQueueArgv(name), "--packets", "0"))
	var q struct {
		Machine string `json:"machine"`
	}
	if err != nil || json.Unmarshal([]byte(body), &q) != nil || (q.Machine != "RUNNING" && q.Machine != "PAUSED" && q.Machine != friend.MachineStoppedWord) {
		words.publish(request, nil)
		return false
	}
	return words.publish(request, &sprintMachine{state: q.Machine, at: time.Now()}) && q.Machine == "RUNNING"
}
