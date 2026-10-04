//go:build darwin || linux

package friendwatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

const ownedHelperArg = "--nova-friend-owned-command-helper"
const groupGrace = 100 * time.Millisecond

type groupStatus struct {
	Kind  string
	PID   int
	Error string
}

type ownedCommand struct {
	leader          *exec.Cmd
	control, status *os.File
	group           int
}

// startOwnedCommand implements OwnedCommandGroup Spawn/Verify/Launch: verify the private
// session leader before authorizing argv, and reserve its PID until cleanup.
func startOwnedCommand(ctx context.Context, o Options) (*ownedCommand, <-chan error, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, nil, err
	}
	configR, configW, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	statusR, statusW, err := os.Pipe()
	if err != nil {
		configR.Close()
		configW.Close()
		return nil, nil, err
	}
	leader := subproc.Long(context.Background(), executable, ownedHelperArg)
	leader.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	leader.ExtraFiles = []*os.File{configR, statusW}
	leader.Stdout, leader.Stderr = o.Stdout, o.Stderr
	if !o.StdinLifetime {
		leader.Stdin = o.Stdin
	}
	if err = leader.Start(); err != nil {
		configR.Close()
		configW.Close()
		statusR.Close()
		statusW.Close()
		return nil, nil, err
	}
	configR.Close()
	statusW.Close()
	owned := &ownedCommand{leader: leader, control: configW, status: statusR}
	// No Wait goroutine: even a crashed leader remains unreaped until cleanup.
	events := make(chan groupStatus, 3)
	go func() {
		defer close(events)
		decoder := json.NewDecoder(statusR)
		for {
			var event groupStatus
			if err := decoder.Decode(&event); err != nil {
				events <- groupStatus{Kind: "error", Error: err.Error()}
				return
			}
			events <- event
			if event.Kind == "done" || event.Kind == "error" {
				return
			}
		}
	}()
	setupFailure := func(cause error) (*ownedCommand, <-chan error, error) {
		// Contract 2: before verified startup only the directly owned handle is killed.
		_ = leader.Process.Kill()
		_ = leader.Wait()
		configW.Close()
		statusR.Close()
		return nil, nil, cause
	}
	get := func() (groupStatus, error) {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case e, ok := <-events:
			if !ok || e.Kind == "error" {
				return e, fmt.Errorf("owned helper setup: %s", e.Error)
			}
			return e, nil
		case <-ctx.Done():
			return groupStatus{}, ctx.Err()
		case <-timer.C:
			return groupStatus{}, errors.New("owned helper setup timed out")
		}
	}
	hello, err := get()
	if err != nil {
		return setupFailure(err)
	}
	pid := leader.Process.Pid
	group, err := syscall.Getpgid(pid)
	if err != nil || hello.Kind != "leader" || hello.PID != pid || pid <= 1 || group != pid || group == syscall.Getpgrp() {
		return setupFailure(errors.New("owned helper session identity refused"))
	}
	owned.group = group
	if err := json.NewEncoder(configW).Encode(o.Argv); err != nil {
		return nil, nil, errors.Join(err, owned.cleanup())
	}
	started, err := get()
	if err != nil || started.Kind != "started" {
		if err == nil {
			err = errors.New("owned helper did not start the command")
		}
		return nil, nil, errors.Join(err, owned.cleanup())
	}
	done := make(chan error, 1)
	go func() {
		e, ok := <-events
		if !ok || e.Kind != "done" {
			done <- fmt.Errorf("owned helper status failed: %s", e.Error)
			return
		}
		if e.Error != "" {
			done <- errors.New(e.Error)
		} else {
			done <- nil
		}
	}()
	return owned, done, nil
}

// cleanup implements OwnedCommandGroup Term/Kill/Reap. The leader is not reaped
// until both signals finish, including on normal command completion.
func (o *ownedCommand) cleanup() error {
	if o.group <= 1 || o.group == syscall.Getpgrp() || o.group != o.leader.Process.Pid {
		return errors.New("owned group cleanup identity refused")
	}
	term := syscall.Kill(-o.group, syscall.SIGTERM)
	if errors.Is(term, syscall.ESRCH) {
		term = nil
	}
	timer := time.NewTimer(groupGrace)
	<-timer.C
	kill := syscall.Kill(-o.group, syscall.SIGKILL)
	if errors.Is(kill, syscall.ESRCH) {
		kill = nil
	}
	// Killing the keeper is expected. Wait is only a reap, not the workload result.
	_ = o.leader.Wait()
	o.control.Close()
	o.status.Close()
	return errors.Join(term, kill)
}

// OwnedHelper is private reexec entry, not a CLI PID or registry authority.
// Its inherited coordination pipes never enter the actual workload's exec.
func OwnedHelper(args []string) (bool, int) {
	if len(args) != 1 || args[0] != ownedHelperArg {
		return false, 0
	}
	control := os.NewFile(3, "owned-control")
	status := os.NewFile(4, "owned-status")
	for _, f := range []*os.File{control, status} {
		info, err := f.Stat()
		if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
			return true, 2
		}
	}
	defer control.Close()
	defer status.Close()
	syscall.CloseOnExec(3)
	syscall.CloseOnExec(4)
	encoder := json.NewEncoder(status)
	if err := encoder.Encode(groupStatus{Kind: "leader", PID: os.Getpid()}); err != nil {
		return true, 2
	}
	var argv []string
	if err := json.NewDecoder(control).Decode(&argv); err != nil || len(argv) == 0 || argv[0] == "" {
		return true, 2
	}
	command := subproc.Long(context.Background(), argv[0], argv[1:]...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := command.Start(); err != nil {
		_ = encoder.Encode(groupStatus{Kind: "error", Error: err.Error()})
		_, _ = io.Copy(io.Discard, control)
		return true, 1
	}
	// Only the keeper ignores TERM, after the command inherited normal dispositions.
	signal.Ignore(syscall.SIGTERM)
	if err := encoder.Encode(groupStatus{Kind: "started"}); err != nil {
		return true, 2
	}
	err := command.Wait()
	event := groupStatus{Kind: "done"}
	if err != nil {
		event.Error = err.Error()
	}
	_ = encoder.Encode(event)
	// Stay alive as the PGID anchor even after the actual command was reaped.
	_, _ = io.Copy(io.Discard, control)
	return true, 0
}
