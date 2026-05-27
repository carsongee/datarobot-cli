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

package up

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/datarobot/cli/internal/log"
	gopsutil "github.com/shirou/gopsutil/v3/process"
)

// ProcessState is the lifecycle state of a managed process.
type ProcessState int

const (
	StateStarting   ProcessState = iota // Waiting for health probe
	StateHealthy                        // Health probe confirmed readiness
	StateCrashed                        // Process exited unexpectedly
	StateStopped                        // Intentionally stopped
	StateRestarting                     // In the middle of a restart
)

// String returns a human-readable label for the state.
func (s ProcessState) String() string {
	switch s {
	case StateStarting:
		return "Starting"
	case StateHealthy:
		return "Healthy"
	case StateCrashed:
		return "Crashed"
	case StateStopped:
		return "Stopped"
	case StateRestarting:
		return "Restarting"
	default:
		return "Unknown"
	}
}

// ServiceUpdate is sent from a supervisor goroutine to the TUI model.
// Fields are pointers so callers can distinguish "no change" from zero values.
type ServiceUpdate struct {
	Name    string
	State   *ProcessState
	LogLine *LogEntry
	PID     int
}

// LogEntry is a single timestamped line of process output.
type LogEntry struct {
	Line      string
	Timestamp time.Time
	IsSep     bool // true for synthetic separator lines (e.g. "── restarted ──")
}

const maxLogLines = 500

// logRing is a fixed-capacity circular buffer of log entries.
type logRing struct {
	buf  [maxLogLines]LogEntry
	head int // next write index
	size int // number of valid entries
}

func (r *logRing) add(e LogEntry) {
	r.buf[r.head] = e
	r.head = (r.head + 1) % maxLogLines

	if r.size < maxLogLines {
		r.size++
	}
}

// all returns entries in chronological order.
func (r *logRing) all() []LogEntry {
	out := make([]LogEntry, r.size)

	if r.size < maxLogLines {
		copy(out, r.buf[:r.size])
	} else {
		tail := r.head
		n := copy(out, r.buf[tail:])
		copy(out[n:], r.buf[:tail])
	}

	return out
}

// Supervisor manages the lifecycle of a single service process.
type Supervisor struct {
	cfg       ServiceConfig
	ch        chan ServiceUpdate
	mu        sync.Mutex // protects cancel
	restartMu sync.Mutex // serializes concurrent Restart calls
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// NewSupervisor creates a supervisor that sends updates on ch.
func NewSupervisor(cfg ServiceConfig, ch chan ServiceUpdate) *Supervisor {
	return &Supervisor{cfg: cfg, ch: ch}
}

// Start launches the service process in a goroutine.
func (s *Supervisor) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	childCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.wg.Add(1)

	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Debug("up: supervisor panic recovered", "service", s.cfg.Name, "panic", r)

				crashed := StateCrashed

				// Guard sendUpdate with its own recover: if the update channel was
				// closed (e.g. during a test that closes it to trigger this path),
				// the send itself would panic and crash the goroutine. This inner
				// recover ensures the outer recovery body always exits cleanly.
				func() {
					defer func() { _ = recover() }()

					s.sendUpdate(ServiceUpdate{Name: s.cfg.Name, State: &crashed})
				}()
			}
		}()

		s.run(childCtx)
	}()
}

// Stop cancels the service context and waits for the goroutine to finish.
func (s *Supervisor) Stop() {
	cancel := s.readCancel()
	if cancel != nil {
		cancel()
	}

	s.wg.Wait()
}

func (s *Supervisor) readCancel() context.CancelFunc {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.cancel
}

// Restart transitions the service to StateRestarting, stops it, then starts it again.
// Concurrent Restart calls are serialized so only one process is ever launched per service.
func (s *Supervisor) Restart(ctx context.Context) {
	s.restartMu.Lock()
	defer s.restartMu.Unlock()

	restarting := StateRestarting
	s.sendUpdate(ServiceUpdate{Name: s.cfg.Name, State: &restarting})
	s.Stop()
	s.Start(ctx)
}

// StopAndNotify stops the service and sends a StateStopped update.
// It serializes with concurrent Restart calls via restartMu.
func (s *Supervisor) StopAndNotify() {
	s.restartMu.Lock()
	defer s.restartMu.Unlock()

	s.Stop()

	stopped := StateStopped
	s.sendUpdate(ServiceUpdate{Name: s.cfg.Name, State: &stopped})
}

func (s *Supervisor) run(ctx context.Context) {
	starting := StateStarting
	s.sendUpdate(ServiceUpdate{Name: s.cfg.Name, State: &starting})

	cmd := makeCommand(ctx, s.cfg)

	// Merge stdout and stderr through a line-splitting writer.
	lw := &lineWriter{name: s.cfg.Name, ch: s.ch}
	cmd.Stdout = lw
	cmd.Stderr = lw

	if err := cmd.Start(); err != nil {
		// If the context was already cancelled (e.g. Stop() called before the
		// process launched), report StateStopped rather than StateCrashed so
		// callers can distinguish a clean shutdown from an actual crash.
		state := StateCrashed
		if ctx.Err() != nil {
			state = StateStopped
		}

		s.sendUpdate(ServiceUpdate{
			Name:  s.cfg.Name,
			State: &state,
			LogLine: &LogEntry{
				Line:      fmt.Sprintf("failed to start: %v", err),
				Timestamp: time.Now(),
			},
		})
		log.Debug("up: process start failed", "service", s.cfg.Name, "err", err)

		return
	}

	s.sendUpdate(ServiceUpdate{Name: s.cfg.Name, PID: cmd.Process.Pid})
	log.Debug("up: process started", "service", s.cfg.Name, "pid", cmd.Process.Pid)

	// Health probe runs until healthy or context cancelled.
	probeCtx, probeCancel := context.WithCancel(ctx)
	defer probeCancel()

	if s.cfg.Probe == ProbeTCP || s.cfg.Probe == ProbeHTTP {
		s.wg.Add(1)

		go func() {
			defer s.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Debug("up: probe panic recovered", "service", s.cfg.Name, "panic", r)
				}
			}()

			s.runProbe(probeCtx)
		}()
	} else {
		// ProbeNone or unset — mark healthy immediately.
		healthy := StateHealthy
		s.sendUpdate(ServiceUpdate{Name: s.cfg.Name, State: &healthy})
	}

	err := cmd.Wait()

	lw.flush()
	probeCancel()

	if ctx.Err() != nil {
		stopped := StateStopped
		s.sendUpdate(ServiceUpdate{Name: s.cfg.Name, State: &stopped})

		return
	}

	// Services are expected to be long-running daemons; any exit — including a
	// clean zero exit — is abnormal while dr up is running.
	crashed := StateCrashed

	exitMsg := "exited: status 0"
	if err != nil {
		exitMsg = fmt.Sprintf("exited: %v", err)
	}

	s.sendUpdate(ServiceUpdate{
		Name:  s.cfg.Name,
		State: &crashed,
		LogLine: &LogEntry{
			Line:      exitMsg,
			Timestamp: time.Now(),
		},
	})
}

func (s *Supervisor) runProbe(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var ok bool

			if s.cfg.Probe == ProbeHTTP {
				ok = probeHTTP(ctx, s.cfg.URL)
			} else {
				ok = probeTCP(s.cfg.Port)
			}

			if ok {
				healthy := StateHealthy
				s.sendUpdate(ServiceUpdate{Name: s.cfg.Name, State: &healthy})

				return
			}
		}
	}
}

func (s *Supervisor) sendUpdate(u ServiceUpdate) {
	s.ch <- u
}

// lineWriter is an io.Writer that splits writes into lines and sends each
// complete line as a ServiceUpdate. It is not safe for concurrent use by
// multiple goroutines (stdout and stderr should be merged before writing here).
type lineWriter struct {
	name string
	ch   chan ServiceUpdate
	buf  bytes.Buffer
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)

	for {
		b := w.buf.Bytes()
		idx := bytes.IndexByte(b, '\n')

		if idx < 0 {
			break
		}

		line := strings.TrimRight(string(b[:idx]), "\r")

		w.buf.Next(idx + 1)

		select {
		case w.ch <- ServiceUpdate{
			Name: w.name,
			LogLine: &LogEntry{
				Line:      line,
				Timestamp: time.Now(),
			},
		}:
		default:
			// Drop log line to avoid blocking the process pipe.
		}
	}

	return len(p), nil
}

// flush emits any buffered partial line (text written without a trailing newline).
// Call this after the process exits so the final output line is not silently dropped.
func (w *lineWriter) flush() {
	if w.buf.Len() == 0 {
		return
	}

	line := strings.TrimRight(w.buf.String(), "\r")

	w.buf.Reset()

	select {
	case w.ch <- ServiceUpdate{
		Name: w.name,
		LogLine: &LogEntry{
			Line:      line,
			Timestamp: time.Now(),
		},
	}:
	default:
	}
}

// getProcessMetrics queries CPU% and RSS in MiB for a PID via gopsutil.
// Returns zeros on error so the caller can display a dash.
func getProcessMetrics(pid int) (cpuPct float64, memMiB float64) {
	proc, err := gopsutil.NewProcess(int32(pid))
	if err != nil {
		return 0, 0
	}

	cpuPct, err = proc.CPUPercent()
	if err != nil {
		log.Debug("up: cpu percent failed", "pid", pid, "err", err)
	}

	memInfo, err := proc.MemoryInfo()
	if err != nil || memInfo == nil {
		return cpuPct, 0
	}

	memMiB = float64(memInfo.RSS) / (1024 * 1024)

	return cpuPct, memMiB
}

// buildEnv produces an environment for the child process. It starts from the
// current process's environment and merges extra on top so that service-specific
// values always take precedence over inherited system variables.
func buildEnv(extra map[string]string) []string {
	inherited := os.Environ()

	// Index inherited keys so we can skip them when appending overrides.
	inheritedKeys := make(map[string]bool, len(inherited))

	for _, kv := range inherited {
		k, _, _ := strings.Cut(kv, "=")
		inheritedKeys[k] = true
	}

	// Collect and sort the service-specific override keys for deterministic output.
	overrideKeys := make([]string, 0, len(extra))

	for k := range extra {
		overrideKeys = append(overrideKeys, k)
	}

	slices.Sort(overrideKeys)

	// Build the result: inherited base (preserving original order) with overridden
	// keys replaced, then sorted service-specific keys that are new additions.
	result := make([]string, 0, len(inherited)+len(extra))

	for _, kv := range inherited {
		k, _, _ := strings.Cut(kv, "=")

		if v, ok := extra[k]; ok {
			result = append(result, k+"="+v)
		} else {
			result = append(result, kv)
		}
	}

	for _, k := range overrideKeys {
		if !inheritedKeys[k] {
			result = append(result, k+"="+extra[k])
		}
	}

	return result
}
