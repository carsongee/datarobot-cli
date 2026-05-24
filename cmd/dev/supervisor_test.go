// Copyright 2026 DataRobot, Inc. and its affiliates.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build !windows

package dev

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drainUntilState reads updates from ch until the named service reaches want,
// or fails the test after timeout.
func drainUntilState(t *testing.T, ch <-chan ServiceUpdate, want ProcessState, timeout time.Duration) {
	t.Helper()

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		select {
		case u := <-ch:
			if u.State != nil && *u.State == want {
				return
			}
		case <-deadline.C:
			t.Fatalf("timeout waiting for state %v", want)
		}
	}
}

func TestSupervisor_StartStop_SendsStoppedState(t *testing.T) {
	ch := make(chan ServiceUpdate, 200)
	// ProbeNone means StateHealthy is sent immediately after the process starts,
	// which confirms the process is running before we call Stop().
	cfg := ServiceConfig{Name: "svc", Command: "sleep 60", Probe: ProbeNone}
	sup := NewSupervisor(cfg, ch)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	sup.Start(ctx)
	drainUntilState(t, ch, StateHealthy, 3*time.Second) // wait until running

	sup.Stop()
	drainUntilState(t, ch, StateStopped, 3*time.Second)
}

func TestSupervisor_ProcessCrash_SendsCrashedState(t *testing.T) {
	ch := make(chan ServiceUpdate, 200)
	cfg := ServiceConfig{Name: "svc", Command: "exit 1"}
	sup := NewSupervisor(cfg, ch)

	sup.Start(t.Context())
	drainUntilState(t, ch, StateCrashed, 5*time.Second)
}

func TestSupervisor_ProcessExitZero_SendsCrashedState(t *testing.T) {
	ch := make(chan ServiceUpdate, 200)
	cfg := ServiceConfig{Name: "svc", Command: "exit 0"}
	sup := NewSupervisor(cfg, ch)

	sup.Start(t.Context())
	drainUntilState(t, ch, StateCrashed, 5*time.Second)
}

func TestSupervisor_ProcessExitZero_LogsExitStatus(t *testing.T) {
	ch := make(chan ServiceUpdate, 200)
	cfg := ServiceConfig{Name: "svc", Command: "exit 0"}
	sup := NewSupervisor(cfg, ch)

	sup.Start(t.Context())

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()

	for {
		select {
		case u := <-ch:
			if u.LogLine != nil && u.LogLine.Line == "exited: status 0" {
				return
			}
		case <-deadline.C:
			t.Fatal("timeout waiting for 'exited: status 0' log line")
		}
	}
}

func TestSupervisor_ProcessCrash_LogsExitError(t *testing.T) {
	ch := make(chan ServiceUpdate, 200)
	cfg := ServiceConfig{Name: "svc", Command: "exit 1"}
	sup := NewSupervisor(cfg, ch)

	sup.Start(t.Context())

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()

	for {
		select {
		case u := <-ch:
			if u.LogLine != nil && strings.Contains(u.LogLine.Line, "exited:") {
				assert.Contains(t, u.LogLine.Line, "exit status")

				return
			}
		case <-deadline.C:
			t.Fatal("timeout waiting for 'exited:' log line")
		}
	}
}

func TestSupervisor_ProbeNone_SendsHealthyImmediately(t *testing.T) {
	ch := make(chan ServiceUpdate, 200)
	cfg := ServiceConfig{Name: "svc", Command: "sleep 60", Probe: ProbeNone}
	sup := NewSupervisor(cfg, ch)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	sup.Start(ctx)
	drainUntilState(t, ch, StateHealthy, 3*time.Second)

	sup.Stop()
}

func TestSupervisor_SendsPID(t *testing.T) {
	ch := make(chan ServiceUpdate, 200)
	cfg := ServiceConfig{Name: "svc", Command: "sleep 60"}
	sup := NewSupervisor(cfg, ch)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	sup.Start(ctx)

	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()

	var pid int

	for pid == 0 {
		select {
		case u := <-ch:
			if u.PID != 0 {
				pid = u.PID
			}
		case <-deadline.C:
			t.Fatal("timeout waiting for PID update")
		}
	}

	assert.Positive(t, pid)
	sup.Stop()
}

func TestSupervisor_Restart_SendsRestartingThenHealthy(t *testing.T) {
	ch := make(chan ServiceUpdate, 500)
	cfg := ServiceConfig{Name: "svc", Command: "sleep 60", Probe: ProbeNone}
	sup := NewSupervisor(cfg, ch)

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	sup.Start(ctx)
	drainUntilState(t, ch, StateHealthy, 3*time.Second)

	go sup.Restart(ctx)

	drainUntilState(t, ch, StateRestarting, 3*time.Second)
	drainUntilState(t, ch, StateHealthy, 5*time.Second)

	sup.Stop()
}

func TestSupervisor_StopBeforeStart_IsIdempotent(t *testing.T) {
	ch := make(chan ServiceUpdate, 10)
	cfg := ServiceConfig{Name: "svc", Command: "sleep 60"}
	sup := NewSupervisor(cfg, ch)

	sup.Stop() // must not panic or deadlock on an unstarted supervisor
}

func TestSupervisor_StopDuringProbe_SendsStoppedState(t *testing.T) {
	ch := make(chan ServiceUpdate, 200)
	// Port 1 is reserved and won't accept connections, so probe will keep failing.
	cfg := ServiceConfig{Name: "svc", Command: "sleep 60", Probe: ProbeTCP, Port: 1}
	sup := NewSupervisor(cfg, ch)

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	sup.Start(ctx)

	// Wait for PID update — proves the process is running and probe goroutine has started.
	pidDeadline := time.NewTimer(5 * time.Second)
	defer pidDeadline.Stop()

	for {
		select {
		case u := <-ch:
			if u.PID != 0 {
				goto probeStarted
			}
		case <-pidDeadline.C:
			t.Fatal("timeout waiting for PID")
		}
	}

probeStarted:
	sup.Stop()

	drainUntilState(t, ch, StateStopped, 5*time.Second)
}

func TestMakeCommand_HasCorrectDirAndEnv(t *testing.T) {
	dir := t.TempDir()
	cfg := ServiceConfig{
		Name:    "test",
		Command: "echo hello",
		Dir:     dir,
		Env:     map[string]string{"MYVAR": "myval"},
	}

	cmd := makeCommand(t.Context(), cfg)

	require.NotNil(t, cmd)
	assert.Equal(t, dir, cmd.Dir)

	var found bool

	for _, e := range cmd.Env {
		if e == "MYVAR=myval" {
			found = true

			break
		}
	}

	assert.True(t, found, "MYVAR=myval should appear in cmd.Env")
}

func TestMakeCommand_CancelBeforeStartReturnsNil(t *testing.T) {
	cfg := ServiceConfig{Name: "test", Command: "echo hello"}

	cmd := makeCommand(t.Context(), cfg)

	// cmd.Process is nil before Start() — the Cancel guard should return nil.
	require.Nil(t, cmd.Process)
	require.NotNil(t, cmd.Cancel)

	err := cmd.Cancel()
	assert.NoError(t, err)
}

func TestSupervisor_ProbeTCP_SendsHealthy(t *testing.T) {
	// Start a real TCP listener so the probe can connect to it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port

	ch := make(chan ServiceUpdate, 200)
	cfg := ServiceConfig{
		Name:    "svc",
		Command: "sleep 60",
		Probe:   ProbeTCP,
		Port:    port,
	}
	sup := NewSupervisor(cfg, ch)

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	sup.Start(ctx)
	drainUntilState(t, ch, StateHealthy, 10*time.Second)

	sup.Stop()
}

func TestSupervisor_ProbeHTTP_SendsHealthy(t *testing.T) {
	// Start a real HTTP server so the probe can hit it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch := make(chan ServiceUpdate, 200)
	cfg := ServiceConfig{
		Name:    "svc",
		Command: "sleep 60",
		Probe:   ProbeHTTP,
		URL:     srv.URL,
	}
	sup := NewSupervisor(cfg, ch)

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	sup.Start(ctx)
	drainUntilState(t, ch, StateHealthy, 10*time.Second)

	sup.Stop()
}

func TestProbeTCP_ReturnsTrueWhenListening(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port

	assert.True(t, probeTCP(port))
}

func TestProbeTCP_ReturnsFalseWhenNotListening(t *testing.T) {
	// Port 1 is reserved and should not be listening in a test env.
	assert.False(t, probeTCP(1))
}

func TestProbeHTTP_ReturnsTrueForOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	assert.True(t, probeHTTP(srv.URL))
}

func TestProbeHTTP_ReturnsFalseForServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	assert.False(t, probeHTTP(srv.URL))
}

func TestProbeHTTP_ReturnsFalseWhenUnreachable(t *testing.T) {
	assert.False(t, probeHTTP("http://127.0.0.1:1/health"))
}

func TestSupervisor_StartErrorCancelledContext_SendsStoppedState(t *testing.T) {
	ch := make(chan ServiceUpdate, 50)
	// Dir that does not exist causes cmd.Start() to fail immediately.
	cfg := ServiceConfig{Name: "svc", Command: "echo hello", Dir: "/nonexistent-test-dir-abc123"}
	sup := NewSupervisor(cfg, ch)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Start so ctx.Err() != nil inside run()

	sup.Start(ctx)
	drainUntilState(t, ch, StateStopped, 3*time.Second)
}

func TestSupervisor_StartErrorActiveContext_SendsCrashedState(t *testing.T) {
	ch := make(chan ServiceUpdate, 50)
	// Dir that does not exist causes cmd.Start() to fail immediately.
	cfg := ServiceConfig{Name: "svc", Command: "echo hello", Dir: "/nonexistent-test-dir-abc123"}
	sup := NewSupervisor(cfg, ch)

	sup.Start(t.Context())
	drainUntilState(t, ch, StateCrashed, 3*time.Second)
}

func TestMakeCommand_CancelWithDeadProcess_FallsBackToSignal(t *testing.T) {
	// Start a process that exits immediately so the PID is freed, which makes
	// Getpgid fail and exercises the cmd.Process.Signal fallback in Cancel.
	cmd := makeCommand(t.Context(), ServiceConfig{Name: "test", Command: "true"})
	require.NoError(t, cmd.Start())
	_ = cmd.Wait() // reap the zombie; PID is now freed

	// Cancel on a dead process: Getpgid returns ESRCH → falls back to Signal.
	// Signal also returns ESRCH, but Cancel must not panic.
	_ = cmd.Cancel()
}

func TestSupervisor_Start_GoroutinePanicIsCaught(t *testing.T) {
	// Closing the channel before Start() causes the first sendUpdate in run()
	// (StateStarting) to panic. The goroutine's defer/recover must catch it,
	// attempt sendUpdate(StateCrashed) — which also panics on the closed channel
	// but is caught by the nested inner recover — and exit cleanly without
	// crashing the program.
	ch := make(chan ServiceUpdate, 50)
	cfg := ServiceConfig{Name: "svc", Command: "sleep 60", Probe: ProbeNone}
	sup := NewSupervisor(cfg, ch)

	close(ch)

	// Start() must not panic; the goroutine must recover internally.
	assert.NotPanics(t, func() { sup.Start(t.Context()) })

	// Stop() calls wg.Wait(), ensuring the goroutine has fully exited.
	assert.NotPanics(t, sup.Stop)
}
