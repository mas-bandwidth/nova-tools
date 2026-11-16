package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeHoldSession struct {
	mu     sync.Mutex
	line   string
	err    error
	closed bool
	block  chan struct{}
}

func (s *fakeHoldSession) StdoutLine() (string, error) {
	if s.block != nil {
		<-s.block
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.line, s.err
}

func (s *fakeHoldSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.block != nil {
		select {
		case <-s.block:
		default:
			close(s.block)
		}
	}
	return nil
}

func (s *fakeHoldSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *fakeHoldSession) Wait() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return 1, s.err
	}
	return 0, nil
}

func TestParseNeeds(t *testing.T) {
	t.Parallel()

	// Default cpu 1, memory 1
	p, err := parseNeeds("")
	require.NoError(t, err)
	assert.Equal(t, 1, p.Amounts["cpu"])
	assert.Equal(t, 1, p.Amounts["memory"])
	assert.Empty(t, p.Bare)

	// Custom amounts and bare tags
	p, err = parseNeeds("cpu 4, memory 8, mac, ps5")
	require.NoError(t, err)
	assert.Equal(t, 4, p.Amounts["cpu"])
	assert.Equal(t, 8, p.Amounts["memory"])
	assert.Equal(t, []string{"mac", "ps5"}, p.Bare)

	// Reversed order: 8 memory, 4 cpu
	p, err = parseNeeds("4 cpu, 8 memory, linux, amd64")
	require.NoError(t, err)
	assert.Equal(t, 4, p.Amounts["cpu"])
	assert.Equal(t, 8, p.Amounts["memory"])
	assert.Equal(t, []string{"linux", "amd64"}, p.Bare)

	// Invalid specs
	_, err = parseNeeds("4")
	assert.Error(t, err)
	_, err = parseNeeds("cpu -1")
	assert.Error(t, err)
	_, err = parseNeeds("cpu 0")
	assert.Error(t, err)
	_, err = parseNeeds("cpu 4 5")
	assert.Error(t, err)
}

func TestFitsMax(t *testing.T) {
	t.Parallel()

	cpu16 := 16
	mem64 := 64
	cores16 := 16
	m := fleetMachine{
		Name:      "bench-linux",
		Address:   "100.64.0.1",
		User:      "nova",
		OS:        "linux",
		Arch:      "amd64",
		Cores:     &cores16,
		CPUUnits:  &cpu16,
		MemoryGB:  &mem64,
		Tags:      "metal,gpu",
		Resources: map[string]int{"ps5": 1},
	}

	assert.Equal(t, "linux", m.EffectiveOS())
	assert.Equal(t, []string{"amd64", "gpu", "linux", "metal"}, m.EffectiveTags())
	assert.Equal(t, map[string]int{"cpu": 16, "memory": 64, "ps5": 1}, m.EffectiveResources())

	// Fits within capacity
	n, _ := parseNeeds("cpu 4, memory 8, linux, amd64, gpu")
	assert.True(t, m.FitsMax(n))

	// Fits with custom resource as bare token
	n, _ = parseNeeds("cpu 4, memory 8, ps5")
	assert.True(t, m.FitsMax(n))

	// Rejects missing tag
	n, _ = parseNeeds("cpu 4, memory 8, mac")
	assert.False(t, m.FitsMax(n))

	// Rejects CPU over capacity
	n, _ = parseNeeds("cpu 32, memory 8")
	assert.False(t, m.FitsMax(n))

	// Rejects memory over capacity
	n, _ = parseNeeds("cpu 4, memory 128")
	assert.False(t, m.FitsMax(n))

	// EffectiveOS darwin -> mac
	mMac := fleetMachine{
		Name:     "bench-mac",
		OS:       "darwin",
		Arch:     "arm64",
		CPUUnits: &cpu16,
		MemoryGB: &mem64,
	}
	assert.Equal(t, "mac", mMac.EffectiveOS())
	assert.Contains(t, mMac.EffectiveTags(), "mac")
	assert.Contains(t, mMac.EffectiveTags(), "arm64")
	nMac, _ := parseNeeds("mac, arm64")
	assert.True(t, mMac.FitsMax(nMac))
}

func TestReserveValidation(t *testing.T) {
	t.Parallel()

	var out, err bytes.Buffer
	// Missing --needs
	code := runReserve([]string{}, &out, &err)
	assert.Equal(t, 2, code)
	assert.Contains(t, err.String(), "--needs wants a resource spec")

	// Invalid --needs
	err.Reset()
	code = runReserve([]string{"--needs", "cpu 0"}, &out, &err)
	assert.Equal(t, 2, code)
	assert.Contains(t, err.String(), "invalid --needs spec")

	// Unknown flag
	err.Reset()
	code = runReserve([]string{"--needs", "cpu 1", "--bogus"}, &out, &err)
	assert.Equal(t, 2, code)
	assert.Contains(t, err.String(), "unknown flag --bogus")
}

func setTestReserveSeam(s reserveSeam) func() {
	reserveSeamMu.Lock()
	old := activeReserveSeam
	activeReserveSeam = s
	reserveSeamMu.Unlock()
	return func() {
		reserveSeamMu.Lock()
		activeReserveSeam = old
		reserveSeamMu.Unlock()
	}
}

func TestReserveR12RefusalWhenNoMachineCanFit(t *testing.T) {
	cpu4 := 4
	mem8 := 8
	cores4 := 4
	fakeSeam := reserveSeam{
		configMachines: func() ([]fleetMachine, error) {
			return []fleetMachine{
				{
					Name:      "small-bench",
					Roles:     "bench",
					OS:        "linux",
					Arch:      "amd64",
					Cores:     &cores4,
					CPUUnits:  &cpu4,
					MemoryGB:  &mem8,
					Tags:      "linux,amd64",
					Resources: map[string]int{},
				},
			}, nil
		},
	}

	defer setTestReserveSeam(fakeSeam)()

	var out, err bytes.Buffer
	code := runReserve([]string{"--needs", "cpu 16, memory 32"}, &out, &err)
	assert.Equal(t, 1, code)
	assert.Contains(t, err.String(), "no machine in the fleet can fit needs")
	assert.Contains(t, err.String(), "(R12)")
}

func TestReserveAcquiresAndPrints(t *testing.T) {
	cpu8 := 8
	mem16 := 16
	cores8 := 8
	mach := fleetMachine{
		Name:      "worker-1",
		Address:   "100.64.0.10",
		User:      "nova",
		Roles:     "bench",
		OS:        "linux",
		Arch:      "amd64",
		Cores:     &cores8,
		CPUUnits:  &cpu8,
		MemoryGB:  &mem16,
		Tags:      "metal",
		Resources: map[string]int{},
	}

	sess := &fakeHoldSession{line: "HELD cpu=4 cores=4 slot0"}
	fakeSeam := reserveSeam{
		configMachines: func() ([]fleetMachine, error) {
			return []fleetMachine{mach}, nil
		},
		probeHold: func(ctx context.Context, m fleetMachine, holdArgs []string) (holdSession, error) {
			return sess, nil
		},
		runRemote: func(m fleetMachine, cmd []string, stdin io.Reader, stdout, stderr io.Writer) int {
			assert.Equal(t, "worker-1", m.Name)
			assert.Equal(t, []string{"echo", "ok"}, cmd)
			return 0
		},
	}

	defer setTestReserveSeam(fakeSeam)()

	// Text output with command
	var out, err bytes.Buffer
	code := runReserve([]string{"--needs", "cpu 4, memory 8", "--", "echo", "ok"}, &out, &err)
	assert.Equal(t, 0, code)
	assert.Contains(t, out.String(), "RESERVED host=worker-1 address=100.64.0.10 user=nova cores=4")
	assert.True(t, sess.closed, "hold session closed after command")

	// JSON output with command
	out.Reset()
	err.Reset()
	sess2 := &fakeHoldSession{line: "HELD cpu=4 cores=4 slot0"}
	fakeSeam.probeHold = func(ctx context.Context, m fleetMachine, holdArgs []string) (holdSession, error) {
		return sess2, nil
	}
	defer setTestReserveSeam(fakeSeam)()

	code = runReserve([]string{"--needs", "cpu 4, memory 8", "--json", "--", "echo", "ok"}, &out, &err)
	assert.Equal(t, 0, code)
	var j map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &j))
	assert.Equal(t, "worker-1", j["host"])
	assert.Equal(t, "100.64.0.10", j["address"])
	assert.Equal(t, "nova", j["user"])
	assert.Equal(t, float64(4), j["cores"])
}

func TestReserveClosesLosingCandidates(t *testing.T) {
	cpu8 := 8
	mem16 := 16
	cores8 := 8
	mach1 := fleetMachine{Name: "b1", Address: "100.64.0.1", Roles: "bench", OS: "linux", Arch: "amd64", Cores: &cores8, CPUUnits: &cpu8, MemoryGB: &mem16}
	mach2 := fleetMachine{Name: "b2", Address: "100.64.0.2", Roles: "bench", OS: "linux", Arch: "amd64", Cores: &cores8, CPUUnits: &cpu8, MemoryGB: &mem16}

	sessWin := &fakeHoldSession{line: "HELD cpu=4 cores=4"}
	sessLose := &fakeHoldSession{block: make(chan struct{})}

	fakeSeam := reserveSeam{
		configMachines: func() ([]fleetMachine, error) {
			return []fleetMachine{mach1, mach2}, nil
		},
		probeHold: func(ctx context.Context, m fleetMachine, holdArgs []string) (holdSession, error) {
			if m.Name == "b1" {
				return sessWin, nil
			}
			return sessLose, nil
		},
		runRemote: func(m fleetMachine, cmd []string, stdin io.Reader, stdout, stderr io.Writer) int {
			return 0
		},
	}

	defer setTestReserveSeam(fakeSeam)()

	var out, err bytes.Buffer
	code := runReserve([]string{"--needs", "cpu 2, memory 4", "--", "true"}, &out, &err)
	assert.Equal(t, 0, code)
	assert.Contains(t, out.String(), "RESERVED host=b1")
	require.Eventually(t, sessLose.isClosed, time.Second, 5*time.Millisecond, "losing session was cancelled and closed immediately")
}

func TestResourcesQuery(t *testing.T) {
	cpu16 := 16
	mem64 := 64
	cores16 := 16
	mach := fleetMachine{
		Name:     "b1",
		Address:  "100.64.0.1",
		Roles:    "bench",
		OS:       "linux",
		Arch:     "amd64",
		Cores:    &cores16,
		CPUUnits: &cpu16,
		MemoryGB: &mem64,
	}

	statusJSON := `{"tags":["linux","amd64"],"resources":{"cpu":16,"memory":64},"held_by_label":{"work":{"cpu":4,"memory":8}}}`

	fakeSeam := reserveSeam{
		configMachines: func() ([]fleetMachine, error) {
			return []fleetMachine{mach}, nil
		},
		queryStatus: func(ctx context.Context, m fleetMachine) (string, error) {
			if m.Name == "b1" {
				return statusJSON, nil
			}
			return "", errors.New("connection timed out")
		},
	}

	defer setTestReserveSeam(fakeSeam)()

	var out, err bytes.Buffer
	code := runResources([]string{}, &out, &err)
	assert.Equal(t, 0, code)
	assert.Contains(t, out.String(), "RESOURCE b1 status=ok cpu=4/16 memory=8/64 tags=linux,amd64")

	out.Reset()
	err.Reset()
	code = runResources([]string{"--json"}, &out, &err)
	assert.Equal(t, 0, code)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &rows))
	require.Len(t, rows, 1)
	assert.Equal(t, "b1", rows[0]["machine"])
	assert.Equal(t, "ok", rows[0]["status"])
}
