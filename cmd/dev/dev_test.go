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

package dev

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/datarobot/cli/internal/cli"
	"github.com/datarobot/cli/internal/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Config tests -------------------------------------------------------

func TestLoadConfig_Valid(t *testing.T) {
	content := `
services:
  - name: api
    command: "uv run uvicorn app.main:app"
    dir: backend
    port: 8080
    probe: tcp
  - name: frontend
    command: "npm run dev"
    port: 5173
    probe: http
    url: "http://localhost:5173"
`
	path := writeTempConfig(t, content)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Len(t, cfg.Services, 2)

	api := cfg.Services[0]
	assert.Equal(t, "api", api.Name)
	assert.Equal(t, "uv run uvicorn app.main:app", api.Command)
	assert.Equal(t, filepath.Join(filepath.Dir(path), "backend"), api.Dir)
	assert.Equal(t, 8080, api.Port)
	assert.Equal(t, ProbeTCP, api.Probe)

	fe := cfg.Services[1]
	assert.Equal(t, "frontend", fe.Name)
	assert.Equal(t, 5173, fe.Port)
	assert.Equal(t, ProbeHTTP, fe.Probe)
	assert.Equal(t, "http://localhost:5173", fe.URL)
}

func TestLoadConfig_WithEnv(t *testing.T) {
	content := `
services:
  - name: svc
    command: python app.py
    env:
      FOO: bar
      HELLO: world
`
	path := writeTempConfig(t, content)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Len(t, cfg.Services, 1)

	assert.Equal(t, "bar", cfg.Services[0].Env["FOO"])
	assert.Equal(t, "world", cfg.Services[0].Env["HELLO"])
}

func TestLoadConfig_Missing(t *testing.T) {
	_, err := LoadConfig("/no/such/file.yaml")
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	path := writeTempConfig(t, "{{{{not yaml")

	_, err := LoadConfig(path)
	require.Error(t, err)
}

func TestLoadConfig_Empty(t *testing.T) {
	path := writeTempConfig(t, "services: []")

	_, err := LoadConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one service")
}

func TestLoadConfig_HTTPProbeURLConstructed(t *testing.T) {
	content := `
services:
  - name: api
    command: go run .
    port: 8080
    probe: http
`
	path := writeTempConfig(t, content)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8080", cfg.Services[0].URL)
}

func TestLoadConfig_Validation_MissingName(t *testing.T) {
	content := `
services:
  - command: go run .
`
	path := writeTempConfig(t, content)

	_, err := LoadConfig(path)
	require.Error(t, err)
}

func TestLoadConfig_Validation_MissingCommand(t *testing.T) {
	content := `
services:
  - name: api
`
	path := writeTempConfig(t, content)

	_, err := LoadConfig(path)
	require.Error(t, err)
}

func TestLoadConfig_Validation_DuplicateName(t *testing.T) {
	content := `
services:
  - name: api
    command: go run .
  - name: api
    command: python app.py
`
	path := writeTempConfig(t, content)

	_, err := LoadConfig(path)
	require.Error(t, err)
}

func TestLoadConfig_Validation_TCPProbeNoPort(t *testing.T) {
	content := `
services:
  - name: api
    command: go run .
    probe: tcp
`
	path := writeTempConfig(t, content)

	_, err := LoadConfig(path)
	require.Error(t, err)
}

func TestLoadConfig_Validation_HTTPProbeNoURLOrPort(t *testing.T) {
	content := `
services:
  - name: api
    command: go run .
    probe: http
`
	path := writeTempConfig(t, content)

	_, err := LoadConfig(path)
	require.Error(t, err)
}

func TestLoadConfig_Validation_TCPProbePortOutOfRange(t *testing.T) {
	content := `
services:
  - name: api
    command: go run .
    probe: tcp
    port: 99999
`
	path := writeTempConfig(t, content)

	_, err := LoadConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "valid port")
}

func TestLoadConfig_Validation_HTTPProbePortOutOfRange(t *testing.T) {
	content := `
services:
  - name: api
    command: go run .
    probe: http
    port: 70000
`
	path := writeTempConfig(t, content)

	_, err := LoadConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "valid port")
}

func TestLoadConfig_Validation_HTTPProbeInvalidURL(t *testing.T) {
	content := `
services:
  - name: api
    command: go run .
    probe: http
    url: "not-a-url"
`
	path := writeTempConfig(t, content)

	_, err := LoadConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute http/https URL")
}

func TestLoadConfig_Validation_HTTPProbeRelativeURL(t *testing.T) {
	content := `
services:
  - name: api
    command: go run .
    probe: http
    url: "/health"
`
	path := writeTempConfig(t, content)

	_, err := LoadConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute http/https URL")
}

func TestLoadConfig_Validation_HTTPProbeValidURL(t *testing.T) {
	content := `
services:
  - name: api
    command: go run .
    probe: http
    url: "http://localhost:8080/health"
`
	path := writeTempConfig(t, content)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8080/health", cfg.Services[0].URL)
}

func TestLoadConfig_Validation_HTTPProbeHTTPSURL(t *testing.T) {
	content := `
services:
  - name: api
    command: go run .
    probe: http
    url: "https://localhost:8443/ready"
`
	path := writeTempConfig(t, content)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "https://localhost:8443/ready", cfg.Services[0].URL)
}

func TestValidHTTPURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"http://localhost:8080", true},
		{"https://example.com/health", true},
		{"http://127.0.0.1:9000/ready", true},
		{"not-a-url", false},
		{"/relative/path", false},
		{"", false},
		{"ftp://localhost/file", false},
		{"//localhost:8080", false},
	}

	for _, tc := range tests {
		got := validHTTPURL(tc.url)
		assert.Equal(t, tc.want, got, "validHTTPURL(%q)", tc.url)
	}
}

func TestLoadConfig_Validation_UnknownProbeType(t *testing.T) {
	content := `
services:
  - name: api
    command: go run .
    probe: grpc
`
	path := writeTempConfig(t, content)

	_, err := LoadConfig(path)
	require.Error(t, err)
}

func TestLoadConfig_RelativeDirResolvedFromConfigFile(t *testing.T) {
	content := `
services:
  - name: api
    command: go run .
    dir: backend
`
	path := writeTempConfig(t, content)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	dir := cfg.Services[0].Dir
	assert.True(t, filepath.IsAbs(dir), "dir should be absolute, got %q", dir)
	assert.Equal(t, filepath.Join(filepath.Dir(path), "backend"), dir)
}

func TestLoadConfig_AbsoluteDirUnchanged(t *testing.T) {
	content := fmt.Sprintf(`
services:
  - name: api
    command: go run .
    dir: %s
`, t.TempDir())
	path := writeTempConfig(t, content)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	assert.True(t, filepath.IsAbs(cfg.Services[0].Dir))
}

// --- ProcessState tests -------------------------------------------------

func TestProcessStateString(t *testing.T) {
	cases := []struct {
		state ProcessState
		want  string
	}{
		{StateStarting, "Starting"},
		{StateHealthy, "Healthy"},
		{StateCrashed, "Crashed"},
		{StateStopped, "Stopped"},
		{StateRestarting, "Restarting"},
		{ProcessState(99), "Unknown"},
	}

	for _, tc := range cases {
		assert.Equal(t, tc.want, tc.state.String(), "state %d", tc.state)
	}
}

// --- renderCPU / renderMem tests ----------------------------------------

func TestRenderCPU_ZeroShowsDash(t *testing.T) {
	assert.Contains(t, renderCPU(0, StateHealthy), "-")
}

func TestRenderCPU_NonZeroHealthyShowsPercent(t *testing.T) {
	out := renderCPU(42.5, StateHealthy)

	assert.Contains(t, out, "42.5%")
}

func TestRenderCPU_StartingStateShowsPercent(t *testing.T) {
	// CPU should be displayed during StateStarting, not just StateHealthy.
	out := renderCPU(15.3, StateStarting)

	assert.Contains(t, out, "15.3%")
}

func TestRenderCPU_NonRunningStateShowsDash(t *testing.T) {
	assert.Contains(t, renderCPU(42.5, StateCrashed), "-")
	assert.Contains(t, renderCPU(42.5, StateStopped), "-")
	assert.Contains(t, renderCPU(42.5, StateRestarting), "-")
}

func TestRenderMem_ZeroShowsDash(t *testing.T) {
	assert.Contains(t, renderMem(0, StateHealthy), "-")
}

func TestRenderMem_MiBRange(t *testing.T) {
	out := renderMem(512, StateHealthy)

	assert.Contains(t, out, "512MiB")
}

func TestRenderMem_GiBRange(t *testing.T) {
	out := renderMem(2048, StateHealthy)

	assert.Contains(t, out, "2.0GiB")
}

func TestRenderMem_NonRunningStateShowsDash(t *testing.T) {
	assert.Contains(t, renderMem(512, StateCrashed), "-")
	assert.Contains(t, renderMem(512, StateStopped), "-")
	assert.Contains(t, renderMem(512, StateRestarting), "-")
}

func TestRenderMem_StartingStateShowsValue(t *testing.T) {
	out := renderMem(256, StateStarting)

	assert.Contains(t, out, "256MiB")
}

// --- applyServiceUpdate tests -------------------------------------------

func TestApplyServiceUpdate_State(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{
			{Name: "svc", Command: "true"},
		},
	}, nil)

	healthy := StateHealthy
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &healthy})

	assert.Equal(t, StateHealthy, m.services[0].state)
}

func TestApplyServiceUpdate_PID(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{
			{Name: "svc", Command: "true"},
		},
	}, nil)

	m.applyServiceUpdate(ServiceUpdate{Name: "svc", PID: 12345})

	assert.Equal(t, 12345, m.services[0].pid)
}

func TestApplyServiceUpdate_LogLine(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{
			{Name: "svc", Command: "true"},
		},
	}, nil)

	entry := LogEntry{Line: "hello", Timestamp: time.Now()}
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", LogLine: &entry})

	got := m.services[0].logs.all()
	require.Len(t, got, 1)
	assert.Equal(t, "hello", got[0].Line)
}

func TestApplyServiceUpdate_UnknownName(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{
			{Name: "svc", Command: "true"},
		},
	}, nil)

	healthy := StateHealthy
	m.applyServiceUpdate(ServiceUpdate{Name: "other", State: &healthy})

	assert.Equal(t, StateStarting, m.services[0].state)
}

func TestApplyServiceUpdate_CrashClearsPIDAndMetrics(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{
			{Name: "svc", Command: "true"},
		},
	}, nil)

	// Simulate a running process.
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", PID: 99})
	m.services[0].cpuPct = 12.5
	m.services[0].memMiB = 64

	crashed := StateCrashed
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &crashed})

	assert.Equal(t, 0, m.services[0].pid)
	assert.InDelta(t, 0.0, m.services[0].cpuPct, 1e-9)
	assert.InDelta(t, 0.0, m.services[0].memMiB, 1e-9)
}

func TestApplyServiceUpdate_StopClearsPIDAndMetrics(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{
			{Name: "svc", Command: "true"},
		},
	}, nil)

	m.applyServiceUpdate(ServiceUpdate{Name: "svc", PID: 42})

	stopped := StateStopped
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &stopped})

	assert.Equal(t, 0, m.services[0].pid)
}

func TestApplyServiceUpdate_RestartingResetsStartedAt(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{
			{Name: "svc", Command: "true"},
		},
	}, nil)

	before := m.services[0].startedAt

	time.Sleep(2 * time.Millisecond)

	restarting := StateRestarting
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &restarting})

	assert.True(t, m.services[0].startedAt.After(before),
		"startedAt should be updated when state transitions to Restarting")
}

func TestApplyServiceUpdate_RestartingAddsRestartSeparatorLog(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true"}},
	}, nil)

	restarting := StateRestarting
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &restarting})

	entries := m.services[0].logs.all()
	require.Len(t, entries, 1)
	assert.Equal(t, "── restarted ──", entries[0].Line)
}

func TestApplyServiceUpdate_StartingDoesNotAddSeparatorLog(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true"}},
	}, nil)

	starting := StateStarting
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &starting})

	entries := m.services[0].logs.all()
	assert.Empty(t, entries, "initial start should not add a separator log")
}

func TestApplyServiceUpdate_StartingResetsStartedAt(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{
			{Name: "svc", Command: "true"},
		},
	}, nil)

	before := m.services[0].startedAt

	time.Sleep(2 * time.Millisecond)

	starting := StateStarting
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &starting})

	assert.True(t, m.services[0].startedAt.After(before),
		"startedAt should be updated when state transitions to Starting")
}

func TestApplyServiceUpdate_HealthyDoesNotResetStartedAt(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{
			{Name: "svc", Command: "true"},
		},
	}, nil)

	before := m.services[0].startedAt

	healthy := StateHealthy
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &healthy})

	assert.Equal(t, before, m.services[0].startedAt,
		"startedAt should not change when transitioning to Healthy")
}

func TestApplyServiceUpdate_StateAndLogLineInOneUpdate(t *testing.T) {
	// This mirrors the ServiceUpdate produced by the cmd.Start() error path
	// in supervisor.run(), which includes both a state change and a log line.
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{
			{Name: "svc", Command: "true"},
		},
	}, nil)

	crashed := StateCrashed
	m.applyServiceUpdate(ServiceUpdate{
		Name:  "svc",
		State: &crashed,
		LogLine: &LogEntry{
			Line:      "failed to start: exec not found",
			Timestamp: time.Now(),
		},
	})

	assert.Equal(t, StateCrashed, m.services[0].state)
	require.Equal(t, 1, m.services[0].logs.size)
	assert.Equal(t, "failed to start: exec not found", m.services[0].logs.all()[0].Line)
}

// --- layout / rendering tests -------------------------------------------

// newInitializedModel returns a model with layout fields set so rendering
// methods can be called without a live terminal.
func newInitializedModel(t *testing.T, services []ServiceConfig) Model {
	t.Helper()

	m := NewModel(t.Context(), &Config{Services: services}, nil)
	m.width = 120
	m.height = 40
	m.initialized = true
	m.logView.Width = m.width
	m.logView.Height = m.logViewHeight()

	return m
}

func TestLogViewHeight_Basic(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "a", Command: "true"}, {Name: "b", Command: "true"}})

	h := m.logViewHeight()

	// height(40) - 7 fixed rows - 2 service rows = 31
	assert.Equal(t, 31, h)
}

func TestLogViewHeight_FilteringAddsRow(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "a", Command: "true"}})
	m.filtering = false
	hWithout := m.logViewHeight()

	m.filtering = true
	hWith := m.logViewHeight()

	assert.Equal(t, hWithout-1, hWith)
}

func TestLogViewHeight_MinFive(t *testing.T) {
	m := newInitializedModel(t, make([]ServiceConfig, 30))
	for i := range m.services {
		m.services[i].cfg.Name = fmt.Sprintf("svc%d", i)
		m.services[i].cfg.Command = "true"
	}

	h := m.logViewHeight()

	assert.Equal(t, 5, h)
}

func TestRenderHeader_ContainsServiceCount(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{
		{Name: "api", Command: "true"},
		{Name: "frontend", Command: "true"},
	})

	out := m.renderHeader()

	assert.Contains(t, out, "2 services")
}

func TestRenderHeader_SingularService(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "api", Command: "true"}})

	out := m.renderHeader()

	assert.Contains(t, out, "1 service")
	assert.NotContains(t, out, "1 services")
}

func TestRenderFooter_ContainsKeybindings(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})

	out := m.renderFooter()

	assert.Contains(t, out, "restart")
	assert.Contains(t, out, "mute")
	assert.Contains(t, out, "quit")
	assert.Contains(t, out, "open")
}

func TestRenderFooter_FilterMode(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.filtering = true

	out := m.renderFooter()

	assert.Contains(t, out, "filter")
}

func TestRenderServiceRow_ColumnsPresent(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "myservice", Command: "true", Port: 8080}})

	row := m.renderServiceRow(0, 30, 12, 7, 8, 7, 8)

	// Service name appears in the row.
	assert.Contains(t, row, "myservice")
	// Port appears in the row.
	assert.Contains(t, row, "8080")
}

func TestRenderServiceRow_NoDashPortWhenZero(t *testing.T) {
	// Services without a port (no probe or probe=none) should show "-" in PORT column.
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})

	row := m.renderServiceRow(0, 30, 12, 7, 8, 7, 8)

	assert.Contains(t, row, "-")
}

func TestRenderServiceRow_LongNameTruncated(t *testing.T) {
	// Service names longer than nameWidth should be clipped with "…".
	m := newInitializedModel(t, []ServiceConfig{{Name: "very-long-service-name-exceeds-width", Command: "true"}})

	row := m.renderServiceRow(0, 10, 12, 7, 8, 7, 8)

	assert.Contains(t, row, "…")
	assert.NotContains(t, row, "very-long-service-name-exceeds-width")
}

func TestRenderServiceRow_MutedBadge(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.services[0].muted = true

	row := m.renderServiceRow(0, 30, 12, 7, 8, 7, 8)

	assert.Contains(t, row, "[m]")
}

func TestRenderServiceRow_SelectedCursor(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{
		{Name: "a", Command: "true"},
		{Name: "b", Command: "true"},
	})
	m.selected = 0

	selected := m.renderServiceRow(0, 30, 12, 7, 8, 7, 8)
	unselected := m.renderServiceRow(1, 30, 12, 7, 8, 7, 8)

	// Selected row must be visually wider due to cursor (ANSI codes differ).
	// Both rows should contain their respective service names.
	assert.Contains(t, selected, "a")
	assert.Contains(t, unselected, "b")
	// The selected row contains the cursor indicator; unselected begins with spaces.
	assert.Contains(t, selected, "▶")
	assert.NotContains(t, unselected, "▶")
}

func TestRenderServiceRow_HealthyWithMetrics(t *testing.T) {
	// End-to-end: verify renderServiceRow integrates renderCPU / renderMem for
	// a healthy service with non-zero CPU and memory.
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true", Port: 9000}})
	m.services[0].state = StateHealthy
	m.services[0].cpuPct = 25.3
	m.services[0].memMiB = 256

	row := m.renderServiceRow(0, 30, 12, 7, 8, 7, 8)

	assert.Contains(t, row, "25.3%")
	assert.Contains(t, row, "256MiB")
	assert.Contains(t, row, "9000")
}

func TestRenderServiceRow_CrashedHidesCPUAndMEM(t *testing.T) {
	// Crashed services must show "-" in CPU and MEM columns even if the
	// collector left non-zero values (applyServiceUpdate clears them, but
	// renderCPU/renderMem must also guard against state transitions that
	// arrive before the next metrics tick clears them).
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.services[0].state = StateCrashed
	m.services[0].cpuPct = 50.0 // deliberately stale
	m.services[0].memMiB = 128  // deliberately stale

	row := m.renderServiceRow(0, 30, 12, 7, 8, 7, 8)

	assert.NotContains(t, row, "50.0%")
	assert.NotContains(t, row, "128MiB")
}

func TestRenderServiceRow_GiBMemoryFormat(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.services[0].state = StateHealthy
	m.services[0].memMiB = 2048 // 2 GiB

	row := m.renderServiceRow(0, 30, 12, 7, 8, 7, 8)

	assert.Contains(t, row, "2.0GiB")
	assert.NotContains(t, row, "2048MiB")
}

// --- helper function tests ----------------------------------------------

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Second, "5s"},
		{90 * time.Second, "1m30s"},
		{3665 * time.Second, "1h1m"},
	}

	for _, tc := range cases {
		assert.Equal(t, tc.want, formatDuration(tc.d), "formatDuration(%v)", tc.d)
	}
}

func TestFormatDuration_NegativeDurationShowsZero(t *testing.T) {
	assert.Equal(t, "0s", formatDuration(-5*time.Second))
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "hello", truncate("hello", 10))
	assert.Equal(t, "hell…", truncate("hello world", 5))
	assert.Equal(t, "hello", truncate("hello", 5))
}

func TestTruncate_MultibyteUnicode(t *testing.T) {
	// Each kanji is one rune (3 bytes in UTF-8). truncate must count runes, not bytes.
	s := "日本語テスト" // 6 runes

	assert.Equal(t, "日本語テスト", truncate(s, 10), "should not truncate when within limit")
	assert.Equal(t, "日本語…", truncate(s, 4), "should truncate at rune boundary")
}

func TestRenderStatus(t *testing.T) {
	// Verify each state produces non-empty output.
	states := []ProcessState{StateHealthy, StateStarting, StateCrashed, StateStopped, StateRestarting}

	for _, s := range states {
		assert.NotEmpty(t, renderStatus(s), "renderStatus(%v)", s)
	}
}

func TestRenderStatus_UnknownStateReturnsUnknown(t *testing.T) {
	out := renderStatus(ProcessState(999))

	assert.Contains(t, out, "Unknown")
}

// --- logRing tests ------------------------------------------------------

func TestLogRing_EmptyAllReturnsNil(t *testing.T) {
	var r logRing

	got := r.all()

	assert.Empty(t, got)
}

func TestLogRing_BelowCapacity(t *testing.T) {
	var r logRing

	for i := range 10 {
		r.add(entry(i))
	}

	got := r.all()
	require.Len(t, got, 10)
	assert.Equal(t, "line-0", got[0].Line)
	assert.Equal(t, "line-9", got[9].Line)
}

func TestLogRing_ExactCapacity(t *testing.T) {
	var r logRing

	for i := range maxLogLines {
		r.add(entry(i))
	}

	got := r.all()
	require.Len(t, got, maxLogLines)
	assert.Equal(t, "line-0", got[0].Line)
	assert.Equal(t, fmt.Sprintf("line-%d", maxLogLines-1), got[maxLogLines-1].Line)
}

func TestLogRing_OverCapacity(t *testing.T) {
	var r logRing

	total := maxLogLines + 50

	for i := range total {
		r.add(entry(i))
	}

	got := r.all()
	require.Len(t, got, maxLogLines)

	// Oldest retained entry should be line-50 (lines 0-49 were overwritten).
	assert.Equal(t, "line-50", got[0].Line)
	assert.Equal(t, fmt.Sprintf("line-%d", total-1), got[maxLogLines-1].Line)
}

func TestLogRing_OrderPreserved(t *testing.T) {
	var r logRing

	for i := range maxLogLines * 2 {
		r.add(entry(i))
	}

	got := r.all()
	require.Len(t, got, maxLogLines)

	for i := 1; i < len(got); i++ {
		before := got[i-1].Timestamp.Before(got[i].Timestamp)
		equal := got[i-1].Timestamp.Equal(got[i].Timestamp)
		assert.True(t, before || equal,
			"entries must be in chronological order at index %d", i)
	}
}

// --- lineWriter tests ---------------------------------------------------

func TestLineWriter_CompleteLine(t *testing.T) {
	ch := make(chan ServiceUpdate, 10)
	w := &lineWriter{name: "svc", ch: ch}

	n, err := w.Write([]byte("hello world\n"))
	require.NoError(t, err)
	assert.Equal(t, 12, n)
	require.Len(t, ch, 1)
	assert.Equal(t, "hello world", (<-ch).LogLine.Line)
}

func TestLineWriter_MultipleLines(t *testing.T) {
	ch := make(chan ServiceUpdate, 10)
	w := &lineWriter{name: "svc", ch: ch}

	_, _ = w.Write([]byte("line1\nline2\nline3\n"))

	assert.Len(t, ch, 3)
}

func TestLineWriter_PartialLine(t *testing.T) {
	ch := make(chan ServiceUpdate, 10)
	w := &lineWriter{name: "svc", ch: ch}

	_, _ = w.Write([]byte("partial"))

	assert.Empty(t, ch) // no newline yet

	_, _ = w.Write([]byte(" complete\n"))

	require.Len(t, ch, 1)
	assert.Equal(t, "partial complete", (<-ch).LogLine.Line)
}

func TestLineWriter_SplitAcrossWrites(t *testing.T) {
	ch := make(chan ServiceUpdate, 10)
	w := &lineWriter{name: "svc", ch: ch}

	_, _ = w.Write([]byte("hel"))
	_, _ = w.Write([]byte("lo\n"))

	require.Len(t, ch, 1)
	assert.Equal(t, "hello", (<-ch).LogLine.Line)
}

func TestLineWriter_StripsCR(t *testing.T) {
	ch := make(chan ServiceUpdate, 10)
	w := &lineWriter{name: "svc", ch: ch}

	_, _ = w.Write([]byte("windows line\r\n"))

	require.Len(t, ch, 1)
	assert.Equal(t, "windows line", (<-ch).LogLine.Line)
}

func TestLineWriter_FlushPartialLine(t *testing.T) {
	ch := make(chan ServiceUpdate, 10)
	w := &lineWriter{name: "svc", ch: ch}

	_, _ = w.Write([]byte("no newline at end"))

	assert.Empty(t, ch) // not emitted yet

	w.flush()

	require.Len(t, ch, 1)
	assert.Equal(t, "no newline at end", (<-ch).LogLine.Line)
}

func TestLineWriter_FlushEmpty(t *testing.T) {
	ch := make(chan ServiceUpdate, 10)
	w := &lineWriter{name: "svc", ch: ch}

	w.flush() // should be a no-op

	assert.Empty(t, ch)
}

func TestLineWriter_WriteDropsWhenChannelFull(t *testing.T) {
	ch := make(chan ServiceUpdate) // unbuffered: send always hits default
	w := &lineWriter{name: "svc", ch: ch}

	n, err := w.Write([]byte("hello\n"))
	require.NoError(t, err)
	assert.Equal(t, 6, n)
	assert.Empty(t, ch)
}

func TestLineWriter_FlushDropsWhenChannelFull(t *testing.T) {
	ch := make(chan ServiceUpdate) // unbuffered: flush always hits default
	w := &lineWriter{name: "svc", ch: ch}

	w.buf.WriteString("partial line")
	w.flush()

	assert.Empty(t, ch)
}

func TestSendUpdate_DropsWhenChannelFull(t *testing.T) {
	ch := make(chan ServiceUpdate) // unbuffered: always hits default
	sup := &Supervisor{cfg: ServiceConfig{Name: "svc"}, ch: ch}

	sup.sendUpdate(ServiceUpdate{Name: "svc"})

	assert.Empty(t, ch)
}

// --- buildEnv tests -----------------------------------------------------

func TestBuildEnv_ServiceVarsOverrideInherited(t *testing.T) {
	// Force a known key into the inherited environment.
	t.Setenv("DR_DEV_TEST_KEY", "inherited")

	result := buildEnv(map[string]string{"DR_DEV_TEST_KEY": "override"})

	var found string

	for _, kv := range result {
		if len(kv) >= len("DR_DEV_TEST_KEY=") && kv[:len("DR_DEV_TEST_KEY=")] == "DR_DEV_TEST_KEY=" {
			found = kv[len("DR_DEV_TEST_KEY="):]
		}
	}

	assert.Equal(t, "override", found, "service env should override inherited env")
}

func TestBuildEnv_InheritsSystemEnv(t *testing.T) {
	t.Setenv("DR_DEV_TEST_INHERIT", "yes")

	result := buildEnv(nil)

	var found bool

	for _, kv := range result {
		if kv == "DR_DEV_TEST_INHERIT=yes" {
			found = true
		}
	}

	assert.True(t, found, "system env should be inherited")
}

// --- padCell tests ------------------------------------------------------

func TestPadCell_PadsPlainText(t *testing.T) {
	result := padCell("hi", 10)

	assert.Equal(t, 10, lipgloss.Width(result))
}

func TestPadCell_HandlesANSIColor(t *testing.T) {
	colored := lipgloss.NewStyle().Foreground(lipgloss.Color("34")).Render("hello")

	result := padCell(colored, 20)

	assert.Equal(t, 20, lipgloss.Width(result))
}

// --- handleRestart guard tests ------------------------------------------

func TestHandleRestart_SkipsWhenAlreadyRestarting(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true"}},
	}, nil)

	restarting := StateRestarting
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &restarting})

	_, cmd := m.handleRestart()

	assert.Nil(t, cmd, "restart should be skipped while already restarting")
}

// --- helpers ------------------------------------------------------------

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "drdev.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

// --- probe tests --------------------------------------------------------

func TestProbeTCP_ConnectsToListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port

	assert.True(t, probeTCP(port))
}

func TestProbeTCP_RefusedConnection(t *testing.T) {
	// Port 1 is reserved and should always refuse connections in test environments.
	// Use a guaranteed-closed ephemeral port instead by binding and immediately closing.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	assert.False(t, probeTCP(port))
}

func TestProbeHTTP_HealthyServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	assert.True(t, probeHTTP(srv.URL))
}

func TestProbeHTTP_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	assert.False(t, probeHTTP(srv.URL))
}

func TestProbeHTTP_ClientError404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	// 4xx is < 500 so the service is considered healthy (it's responding).
	assert.True(t, probeHTTP(srv.URL))
}

func TestProbeHTTP_UnreachableURL(t *testing.T) {
	assert.False(t, probeHTTP("http://127.0.0.1:1"))
}

// --- preflight tests ----------------------------------------------------

func TestRunPreflight_SkipReturnsEmpty(t *testing.T) {
	env, err := runPreflight(context.Background(), true)

	require.NoError(t, err)
	assert.Nil(t, env)
}

func TestRunPreflight_NoSkipReturnsEmpty(t *testing.T) {
	// Stub implementation returns nil for now.
	env, err := runPreflight(context.Background(), false)

	require.NoError(t, err)
	assert.Nil(t, env)
}

func entry(i int) LogEntry {
	return LogEntry{
		Line:      fmt.Sprintf("line-%d", i),
		Timestamp: time.Now().Add(time.Duration(i) * time.Millisecond),
	}
}

// --- handleWindowSize tests ------------------------------------------------

func TestHandleWindowSize_SetsInitializedAndDimensions(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true"}},
	}, nil)
	assert.False(t, m.initialized)

	result, cmd := m.handleWindowSize(tea.WindowSizeMsg{Width: 100, Height: 30})
	tm := result.(Model)

	assert.True(t, tm.initialized)
	assert.Equal(t, 100, tm.width)
	assert.Equal(t, 30, tm.height)
	assert.Nil(t, cmd)
}

func TestHandleWindowSize_SetsLogViewDimensions(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "a", Command: "true"}, {Name: "b", Command: "true"}},
	}, nil)

	result, _ := m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 20})
	tm := result.(Model)

	assert.Equal(t, 80, tm.logView.Width)
	assert.Equal(t, tm.logViewHeight(), tm.logView.Height)
}

func TestHandleWindowSize_ResizePreservesScrollPosition(t *testing.T) {
	// First resize — initializes the viewport.
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true"}},
	}, nil)
	result, _ := m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = result.(Model)

	// Add enough log content to be able to scroll.
	for i := range 60 {
		m.services[0].logs.add(LogEntry{Line: fmt.Sprintf("line %d", i), Timestamp: time.Now()})
	}

	m.logAutoScrl = false
	m.refreshLogViewport()
	m.logView.GotoTop()
	scrollY := m.logView.YOffset

	// Second resize — should NOT reset scroll position (just adjust dimensions).
	result2, _ := m.handleWindowSize(tea.WindowSizeMsg{Width: 100, Height: 35})
	m2 := result2.(Model)

	assert.Equal(t, 100, m2.width)
	assert.Equal(t, scrollY, m2.logView.YOffset, "scroll position should be preserved on resize")
}

// --- handleNavigate tests --------------------------------------------------

func TestHandleNavigate_DownMoves(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{
		{Name: "a", Command: "true"},
		{Name: "b", Command: "true"},
	})
	m.selected = 0

	result, _ := m.handleNavigate(1)
	tm := result.(Model)

	assert.Equal(t, 1, tm.selected)
}

func TestHandleNavigate_UpMoves(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{
		{Name: "a", Command: "true"},
		{Name: "b", Command: "true"},
	})
	m.selected = 1

	result, _ := m.handleNavigate(-1)
	tm := result.(Model)

	assert.Equal(t, 0, tm.selected)
}

func TestHandleNavigate_UpAtBoundaryNoChange(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "a", Command: "true"}})
	m.selected = 0

	result, _ := m.handleNavigate(-1)
	tm := result.(Model)

	assert.Equal(t, 0, tm.selected)
}

func TestHandleNavigate_DownAtBoundaryNoChange(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{
		{Name: "a", Command: "true"},
		{Name: "b", Command: "true"},
	})
	m.selected = 1

	result, _ := m.handleNavigate(1)
	tm := result.(Model)

	assert.Equal(t, 1, tm.selected)
}

func TestHandleNavigate_ResetsLogAutoScroll(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{
		{Name: "a", Command: "true"},
		{Name: "b", Command: "true"},
	})
	m.selected = 0
	m.logAutoScrl = false // simulate user having scrolled up

	result, _ := m.handleNavigate(1)
	tm := result.(Model)

	assert.True(t, tm.logAutoScrl, "navigating to another service should re-enable auto-scroll")
}

func TestHandleNavigate_AtBoundaryDoesNotResetAutoScroll(t *testing.T) {
	// When navigation is blocked by a boundary, logAutoScrl should not change.
	m := newInitializedModel(t, []ServiceConfig{{Name: "a", Command: "true"}})
	m.selected = 0
	m.logAutoScrl = false

	result, _ := m.handleNavigate(-1) // already at top
	tm := result.(Model)

	assert.False(t, tm.logAutoScrl, "boundary no-op should not reset auto-scroll")
}

// --- handleMute tests ------------------------------------------------------

func TestHandleMute_TogglesOn(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	assert.False(t, m.services[0].muted)

	result, _ := m.handleMute()
	tm := result.(Model)

	assert.True(t, tm.services[0].muted)
}

func TestHandleMute_TogglesOff(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.services[0].muted = true

	result, _ := m.handleMute()
	tm := result.(Model)

	assert.False(t, tm.services[0].muted)
}

// --- handleKey tests -------------------------------------------------------

func TestHandleKey_QSetsQuitting(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})

	result, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	tm := result.(Model)

	assert.True(t, tm.quitting)
	assert.NotNil(t, cmd)
}

func TestHandleKey_EscSetsQuitting(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})

	result, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	tm := result.(Model)

	assert.True(t, tm.quitting)
}

func TestHandleKey_JNavigatesDown(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{
		{Name: "a", Command: "true"},
		{Name: "b", Command: "true"},
	})
	m.selected = 0

	result, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	tm := result.(Model)

	assert.Equal(t, 1, tm.selected)
}

func TestHandleKey_KNavigatesUp(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{
		{Name: "a", Command: "true"},
		{Name: "b", Command: "true"},
	})
	m.selected = 1

	result, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	tm := result.(Model)

	assert.Equal(t, 0, tm.selected)
}

func TestHandleKey_DownArrowNavigatesDown(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{
		{Name: "a", Command: "true"},
		{Name: "b", Command: "true"},
	})
	m.selected = 0

	result, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	tm := result.(Model)

	assert.Equal(t, 1, tm.selected)
}

func TestHandleKey_UpArrowNavigatesUp(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{
		{Name: "a", Command: "true"},
		{Name: "b", Command: "true"},
	})
	m.selected = 1

	result, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	tm := result.(Model)

	assert.Equal(t, 0, tm.selected)
}

func TestHandleKey_MTogglesMute(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})

	result, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	tm := result.(Model)

	assert.True(t, tm.services[0].muted)
}

func TestHandleKey_SlashActivatesFilter(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})

	result, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	tm := result.(Model)

	assert.True(t, tm.filtering)
	assert.NotNil(t, cmd) // filterInput.Focus() returns a Cmd
}

func TestHandleKey_GEnablesAutoScroll(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.logAutoScrl = false

	result, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	tm := result.(Model)

	assert.True(t, tm.logAutoScrl)
}

func TestHandleKey_OOpensURLWhenServiceHasPort(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true", Port: 8080}})

	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})

	assert.NotNil(t, cmd, "o key should return a Cmd when service has a port")
}

func TestHandleKey_OReturnsNilCmdWhenNoURL(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})

	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})

	assert.Nil(t, cmd, "o key should return nil Cmd when service has no port or URL")
}

func TestHandleOpenURL_UsesConfigURL(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{
		{Name: "svc", Command: "true", Probe: ProbeHTTP, URL: "http://localhost:8080/health"},
	})

	_, cmd := m.handleOpenURL()

	assert.NotNil(t, cmd, "handleOpenURL should return a Cmd when cfg.URL is set")
}

func TestHandleOpenURL_FallsBackToPort(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{
		{Name: "svc", Command: "true", Port: 9000},
	})

	_, cmd := m.handleOpenURL()

	assert.NotNil(t, cmd, "handleOpenURL should return a Cmd when Port > 0 and URL empty")
}

func TestHandleOpenURL_ReturnsNilCmdWhenNoPortOrURL(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})

	_, cmd := m.handleOpenURL()

	assert.Nil(t, cmd)
}

func TestOpenBrowserCmd_ReturnsNonNil(t *testing.T) {
	require.NotNil(t, openBrowserCmd("http://localhost:9999"))
}

func TestOpenBrowserCmdForOS_ErrorPathDoesNotPanic(t *testing.T) {
	// "linux" produces "xdg-open" which does not exist on macOS/CI, exercising
	// the cmd.Start() error branch without a real display or browser.
	cmd := openBrowserCmdForOS("linux", "http://localhost:9999")
	require.NotNil(t, cmd)
	assert.NotPanics(t, func() { cmd() })
}

func TestBrowserCmdArgs_Darwin(t *testing.T) {
	args := browserCmdArgs("darwin", "http://localhost:8080")

	require.Len(t, args, 2)
	assert.Equal(t, "open", args[0])
	assert.Equal(t, "http://localhost:8080", args[1])
}

func TestBrowserCmdArgs_Windows(t *testing.T) {
	args := browserCmdArgs("windows", "http://localhost:8080")

	require.Len(t, args, 4)
	assert.Equal(t, "cmd", args[0])
	assert.Equal(t, "/c", args[1])
	assert.Equal(t, "start", args[2])
	assert.Equal(t, "http://localhost:8080", args[3])
}

func TestBrowserCmdArgs_Linux(t *testing.T) {
	args := browserCmdArgs("linux", "http://example.com")

	require.Len(t, args, 2)
	assert.Equal(t, "xdg-open", args[0])
	assert.Equal(t, "http://example.com", args[1])
}

func TestBrowserCmdArgs_UnknownOS(t *testing.T) {
	args := browserCmdArgs("freebsd", "http://example.com")

	require.Len(t, args, 2)
	assert.Equal(t, "xdg-open", args[0])
}

func TestHandleViewportKey_GEnablesAutoScroll(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.logAutoScrl = false

	result, _ := m.handleViewportKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	tm := result.(Model)

	assert.True(t, tm.logAutoScrl)
}

func TestHandleViewportKey_OReturnsCmd(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true", Port: 3000}})

	_, cmd := m.handleViewportKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})

	assert.NotNil(t, cmd)
}

func TestHandleViewportKey_UnknownKeyGoesToViewport(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})

	// 'x' is not handled by handleViewportKey → falls through to handleScrollViewport.
	result, _ := m.handleViewportKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	_ = result.(Model)
}

func TestHandleKey_IgnoredWhileQuitting(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{
		{Name: "a", Command: "true"},
		{Name: "b", Command: "true"},
	})
	m.quitting = true
	m.selected = 0

	result, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	tm := result.(Model)

	assert.Equal(t, 0, tm.selected)
	assert.Nil(t, cmd)
}

// --- handleFilterKey tests -------------------------------------------------

func TestHandleFilterKey_EscClearsFilterAndExitsMode(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.filtering = true
	m.filterInput.SetValue("hello")

	result, _ := m.handleFilterKey(tea.KeyMsg{Type: tea.KeyEsc})
	tm := result.(Model)

	assert.False(t, tm.filtering)
	assert.Empty(t, tm.filterInput.Value())
}

func TestHandleFilterKey_EnterCommitsFilterAndExitsMode(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.filtering = true
	m.filterInput.SetValue("hello")

	result, _ := m.handleFilterKey(tea.KeyMsg{Type: tea.KeyEnter})
	tm := result.(Model)

	assert.False(t, tm.filtering)
	assert.Equal(t, "hello", tm.filterInput.Value())
}

// --- View tests ------------------------------------------------------------

func TestView_UninitializedShowsInitializing(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true"}},
	}, nil)

	out := m.View()

	assert.Contains(t, out, "Initializing")
}

func TestView_QuittingShowsStopping(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.quitting = true

	out := m.View()

	assert.Contains(t, out, "Stopping")
}

func TestView_InitializedContainsServiceAndKey(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "myapi", Command: "true", Port: 8080}})

	out := m.View()

	assert.Contains(t, out, "myapi")
	assert.Contains(t, out, "8080")
	assert.Contains(t, out, "quit")
}

// --- renderServiceTable tests ----------------------------------------------

func TestRenderServiceTable_ContainsAllServiceNames(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{
		{Name: "api", Command: "true", Port: 8080},
		{Name: "frontend", Command: "true", Port: 5173},
	})

	out := m.renderServiceTable()

	assert.Contains(t, out, "api")
	assert.Contains(t, out, "frontend")
}

func TestRenderServiceTable_ContainsColumnHeaders(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})

	out := m.renderServiceTable()

	assert.Contains(t, out, "SERVICE")
	assert.Contains(t, out, "STATUS")
	assert.Contains(t, out, "CPU")
}

// --- renderLogPanel tests --------------------------------------------------

func TestRenderLogPanel_ContainsServiceName(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "mysvc", Command: "true"}})

	out := m.renderLogPanel()

	assert.Contains(t, out, "mysvc")
}

func TestRenderLogPanel_MutedShowsMessage(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.services[0].muted = true

	out := m.renderLogPanel()

	assert.Contains(t, out, "muted")
}

func TestRenderLogPanel_MutedAndFilteringShowsFilterRow(t *testing.T) {
	// When a service is muted AND the filter input is active, the filter input
	// row should still appear so the user can see their filter (matching
	// logViewHeight which always reserves a row for it when filtering=true).
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.services[0].muted = true
	m.filtering = true
	m.filterInput.SetValue("myquery")

	out := m.renderLogPanel()

	assert.Contains(t, out, "muted")
	assert.Contains(t, out, "myquery")
}

func TestRenderLogPanel_ShowsLogLines(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.services[0].logs.add(LogEntry{Line: "hello world", Timestamp: time.Now()})
	m.refreshLogViewport()

	out := m.renderLogPanel()

	assert.Contains(t, out, "hello world")
}

func TestRenderLogPanel_FilterTagShownWhenFilterSet(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.filterInput.SetValue("myfilter")

	out := m.renderLogPanel()

	assert.Contains(t, out, "myfilter")
}

func TestRenderLogPanel_FilterTagShowsCountWhenLogsExist(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.services[0].logs.add(LogEntry{Line: "match this", Timestamp: time.Now()})
	m.services[0].logs.add(LogEntry{Line: "skip this", Timestamp: time.Now()})
	m.filterInput.SetValue("match")
	m.refreshLogViewport()

	out := m.renderLogPanel()

	// Should show "filter: match (1/2)" — 1 match out of 2 total lines.
	assert.Contains(t, out, "match")
	assert.Contains(t, out, "1/2")
}

func TestRenderLogPanel_FilterTagNoCountWhenNoLogs(t *testing.T) {
	// When the service has produced no output yet (empty log ring), the count
	// is suppressed — showing "(0/0)" would be confusing.
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.filterInput.SetValue("myfilter")
	m.refreshLogViewport()

	out := m.renderLogPanel()

	assert.Contains(t, out, "myfilter")
	assert.NotContains(t, out, "0/0")
}

func TestRenderLogPanel_FilterCountUpdatesOnServiceNavigation(t *testing.T) {
	// Filter is global; counts must reflect the currently selected service.
	m := newInitializedModel(t, []ServiceConfig{
		{Name: "svc-a", Command: "true"},
		{Name: "svc-b", Command: "true"},
	})
	m.services[0].logs.add(LogEntry{Line: "match line", Timestamp: time.Now()})
	// svc-b has no matching logs.
	m.filterInput.SetValue("match")
	m.selected = 0
	m.refreshLogViewport()

	outA := m.renderLogPanel()

	m.selected = 1
	m.refreshLogViewport()

	outB := m.renderLogPanel()

	assert.Contains(t, outA, "1/1", "service A has 1 match out of 1 log line")
	// service B has no logs so no count is shown.
	assert.NotContains(t, outB, "/")
}

// --- refreshLogViewport tests ----------------------------------------------

func TestRefreshLogViewport_UninitializedReturnsEarly(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true"}},
	}, nil)
	// initialized is false by default — refreshLogViewport must be a no-op.
	m.refreshLogViewport() // must not panic
}

func TestRefreshLogViewport_NoLogsShowsWaitingMessage(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})

	m.refreshLogViewport()

	content := m.logView.View()

	assert.Contains(t, content, "Waiting for output")
}

func TestRefreshLogViewport_FilterAllExcludedShowsNoMatchMessage(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.services[0].logs.add(LogEntry{Line: "hello world", Timestamp: time.Now()})
	m.filterInput.SetValue("zzznomatch")
	m.refreshLogViewport()

	content := m.logView.View()

	assert.Contains(t, content, "No matching lines")
	assert.Contains(t, content, "zzznomatch")
}

func TestRefreshLogViewport_FilterExcludesNonMatchingLines(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.services[0].logs.add(LogEntry{Line: "match this line", Timestamp: time.Now()})
	m.services[0].logs.add(LogEntry{Line: "skip foobar", Timestamp: time.Now()})
	m.filterInput.SetValue("match")
	m.refreshLogViewport()

	content := m.logView.View()

	assert.Contains(t, content, "match this line")
	assert.NotContains(t, content, "skip foobar")
}

func TestRefreshLogViewport_SeparatorRenderedDimWithoutTimestamp(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})

	m.services[0].logs.add(LogEntry{Line: "regular line", Timestamp: time.Now()})
	m.services[0].logs.add(LogEntry{Line: "── restarted ──", Timestamp: time.Now(), IsSep: true})

	m.refreshLogViewport()

	content := m.logView.View()
	assert.Contains(t, content, "── restarted ──")
	assert.Contains(t, content, "regular line")
}

func TestFilterAndRenderEntries_SeparatorRenderedWithoutTimestamp(t *testing.T) {
	color := lipgloss.NewStyle()
	entries := []LogEntry{
		{Line: "log line", Timestamp: time.Now()},
		{Line: "── restarted ──", Timestamp: time.Now(), IsSep: true},
	}

	lines := filterAndRenderEntries(entries, "", color)

	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], "log line")
	assert.Contains(t, lines[1], "── restarted ──")
	// Separator should not contain a timestamp prefix (HH:MM:SS).
	assert.NotRegexp(t, `\d{2}:\d{2}:\d{2}`, lines[1])
}

func TestFilterAndRenderEntries_SeparatorExcludedByFilter(t *testing.T) {
	color := lipgloss.NewStyle()
	entries := []LogEntry{
		{Line: "── restarted ──", Timestamp: time.Now(), IsSep: true},
		{Line: "error starting", Timestamp: time.Now()},
	}

	lines := filterAndRenderEntries(entries, "error", color)

	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "error starting")
}

func TestRefreshLogViewport_AutoScrollGoesToBottom(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.logAutoScrl = true

	for i := range 50 {
		m.services[0].logs.add(LogEntry{
			Line:      fmt.Sprintf("line %d", i),
			Timestamp: time.Now(),
		})
	}

	m.refreshLogViewport()

	assert.True(t, m.logView.AtBottom())
}

// --- Update dispatch tests -------------------------------------------------

func TestUpdate_WindowSizeMessage(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true"}},
	}, nil)

	result, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	tm := result.(Model)

	assert.True(t, tm.initialized)
	assert.Equal(t, 80, tm.width)
}

func TestUpdate_ServiceUpdateMessageAppliesState(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true"}},
	}, nil)
	m.initialized = true

	healthy := StateHealthy
	result, _ := m.Update(serviceUpdateMsg{Name: "svc", State: &healthy})
	tm := result.(Model)

	assert.Equal(t, StateHealthy, tm.services[0].state)
}

func TestUpdate_ServiceUpdateWhileQuitting_StillApplied(t *testing.T) {
	// Service state updates should still be applied while quitting so the
	// TUI reflects each service reaching StateStopped during the shutdown.
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true"}},
	}, nil)
	m.initialized = true
	m.quitting = true

	stopped := StateStopped
	result, _ := m.Update(serviceUpdateMsg{Name: "svc", State: &stopped})
	tm := result.(Model)

	assert.Equal(t, StateStopped, tm.services[0].state)
}

func TestUpdate_ShutdownDoneReturnsQuit(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true"}},
	}, nil)

	_, cmd := m.Update(shutdownDoneMsg{})

	require.NotNil(t, cmd)

	msg := cmd()
	_, isQuit := msg.(tea.QuitMsg)
	assert.True(t, isQuit)
}

func TestUpdate_RestartDoneReturnsNilCmd(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true"}},
	}, nil)

	_, cmd := m.Update(restartDoneMsg{})

	assert.Nil(t, cmd)
}

func TestUpdate_MetricsTickReturnsNextTick(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true"}},
	}, nil)

	_, cmd := m.Update(metricsTickMsg(time.Now()))

	assert.NotNil(t, cmd)
}

// --- Init tests ------------------------------------------------------------

func TestInit_StartsAllSupervisorsAndReturnsBatchCmd(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{
			{Name: "svc1", Command: "sleep 60", Probe: ProbeNone},
			{Name: "svc2", Command: "sleep 60", Probe: ProbeNone},
		},
	}, nil)

	cmd := m.Init()

	// Init must return a non-nil Batch cmd (listenForUpdates + metricsTickCmd).
	require.NotNil(t, cmd)

	// Clean up: stop the supervisors that Init started.
	m.StopAll()
}

// --- collectMetrics tests --------------------------------------------------

func TestCollectMetrics_SkipsZeroPID(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})

	m.collectMetrics()

	assert.InDelta(t, 0.0, m.services[0].cpuPct, 1e-9)
	assert.InDelta(t, 0.0, m.services[0].memMiB, 1e-9)
}

func TestGetProcessMetrics_CurrentProcess(t *testing.T) {
	pid := os.Getpid()

	_, mem := getProcessMetrics(pid)

	assert.Greater(t, mem, 0.0, "current process should report non-zero RSS")
}

// --- StopAll tests ---------------------------------------------------------

func TestStopAll_NoSupervisors(t *testing.T) {
	m := NewModel(t.Context(), &Config{Services: []ServiceConfig{}}, nil)

	m.StopAll() // must not panic
}

func TestStopAll_UnstartedSupervisors(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true"}},
	}, nil)

	m.StopAll() // unstarted supervisors have nil cancel — must not panic or deadlock
}

// --- handleScrollViewport tests --------------------------------------------

func TestHandleScrollViewport_DoesNotPanic(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})

	for i := range 60 {
		m.services[0].logs.add(LogEntry{Line: fmt.Sprintf("line %d", i), Timestamp: time.Now()})
	}

	m.refreshLogViewport()

	// Send a key not explicitly handled by handleKey — routes to handleScrollViewport.
	result, _ := m.handleScrollViewport(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	_ = result.(Model)
}

func TestHandleKey_UnknownKeyGoesToViewport(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})

	// "f" is not handled explicitly — goes to handleScrollViewport.
	result, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	_ = result.(Model)
}

// --- handleRestart (happy path) tests --------------------------------------

func TestHandleRestart_ImmediatelyClearsMetricsAndEnablesAutoScroll(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})

	// Seed non-zero metrics and disable auto-scroll.
	m.services[0].pid = 999
	m.services[0].cpuPct = 15.0
	m.services[0].memMiB = 128
	m.logAutoScrl = false

	healthy := StateHealthy
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &healthy})

	result, _ := m.handleRestart()
	tm := result.(Model)

	assert.Equal(t, 0, tm.services[0].pid)
	assert.InDelta(t, 0.0, tm.services[0].cpuPct, 1e-9)
	assert.InDelta(t, 0.0, tm.services[0].memMiB, 1e-9)
	assert.True(t, tm.logAutoScrl, "restart should re-enable auto-scroll to follow new output")
}

func TestHandleRestart_LaunchesCommandWhenNotRestarting(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true"}},
	}, nil)

	healthy := StateHealthy
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &healthy})

	_, cmd := m.handleRestart()

	assert.NotNil(t, cmd)
}

// --- handleFilterKey (default case) ----------------------------------------

func TestHandleFilterKey_TypingCharUpdatesInput(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.filtering = true

	result, _ := m.handleFilterKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	tm := result.(Model)

	assert.True(t, tm.filtering)
}

// --- stopAllCmd test -------------------------------------------------------

func TestStopAllCmd_ReturnsShutdownDone(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true"}},
	}, nil)

	cmd := m.stopAllCmd()
	require.NotNil(t, cmd)

	msg := cmd()
	_, ok := msg.(shutdownDoneMsg)
	assert.True(t, ok)
}

// --- Update key-message dispatch -------------------------------------------

func TestUpdate_KeyMessageDispatchesToHandleKey(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{
		{Name: "a", Command: "true"},
		{Name: "b", Command: "true"},
	})
	m.selected = 0

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	tm := result.(Model)

	assert.Equal(t, 1, tm.selected)
}

// --- listenForUpdates test -------------------------------------------------

func TestListenForUpdates_ReturnsUpdateFromChannel(t *testing.T) {
	ch := make(chan ServiceUpdate, 1)

	u := ServiceUpdate{Name: "svc"}
	ch <- u

	cmd := listenForUpdates(ch)
	require.NotNil(t, cmd)

	msg := cmd()
	got, ok := msg.(serviceUpdateMsg)

	require.True(t, ok)
	assert.Equal(t, "svc", got.Name)
}

// --- collectMetrics with live PID ------------------------------------------

func TestCollectMetrics_WithLivePID(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.services[0].pid = os.Getpid()

	// collectMetrics must not panic when a valid PID is provided.
	m.collectMetrics()
}

// --- handleScrollViewport logAutoScrl branch --------------------------------

func TestHandleScrollViewport_SetsAutoScrollFalseWhenNotAtBottom(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})

	// Fill the log with enough lines to exceed the viewport height.
	for i := range 200 {
		m.services[0].logs.add(LogEntry{Line: fmt.Sprintf("line %d", i), Timestamp: time.Now()})
	}

	m.refreshLogViewport()

	// Scroll to the top so viewport is NOT at the bottom.
	m.logView.GotoTop()

	// A key that scrolls (e.g. 'j' forwarded here) will trigger AtBottom check.
	result, _ := m.handleScrollViewport(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	tm := result.(Model)

	assert.False(t, tm.logAutoScrl)
}

// --- handleKey "r" (restart) -----------------------------------------------

func TestHandleKey_RTriggersRestart(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})

	healthy := StateHealthy
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &healthy})

	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})

	assert.NotNil(t, cmd)
}

// --- handleKey "s" (stop) -------------------------------------------------

func TestHandleKey_STriggersStop(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})

	healthy := StateHealthy
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &healthy})

	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})

	assert.NotNil(t, cmd)
}

// --- Update with filtering active + unhandled message ---------------------

type unknownTestMsg struct{}

func TestUpdate_FilteringForwardsUnhandledMessage(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.filtering = true

	// Send a message not matched by any case in Update's switch.
	result, _ := m.Update(unknownTestMsg{})
	tm := result.(Model)

	// filtering should still be true — we just forwarded the message.
	assert.True(t, tm.filtering)
}

func TestUpdate_UnhandledMessageNotFilteringReturnsNilCmd(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.filtering = false

	_, cmd := m.Update(unknownTestMsg{})

	assert.Nil(t, cmd)
}

// --- handleKey filtering dispatch ------------------------------------------

func TestHandleKey_FilteringRoutesToHandleFilterKey(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.filtering = true

	// Any key while filtering should route to handleFilterKey.
	result, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	tm := result.(Model)

	assert.True(t, tm.filtering)
}

// --- renderLogPanel edge cases ---------------------------------------------

func TestRenderLogPanel_EmptyServicesReturnsEmpty(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{})

	out := m.renderLogPanel()

	assert.Empty(t, out)
}

func TestRenderLogPanel_FilteringShowsInputRow(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.filtering = true

	out := m.renderLogPanel()

	assert.NotEmpty(t, out)
}

// --- NewModel with extraEnv and service Env --------------------------------

func TestNewModel_ExtraEnvMergedIntoServiceEnv(t *testing.T) {
	cfg := &Config{
		Services: []ServiceConfig{
			{Name: "svc", Command: "true", Env: map[string]string{"SVC_VAR": "1"}},
		},
	}
	extraEnv := map[string]string{"EXTRA_VAR": "2"}

	m := NewModel(t.Context(), cfg, extraEnv)

	require.Len(t, m.services, 1)
	assert.Equal(t, "1", m.services[0].cfg.Env["SVC_VAR"])
	assert.Equal(t, "2", m.services[0].cfg.Env["EXTRA_VAR"])
}

func TestNewModel_ServiceEnvTakesPrecedenceOverExtraEnv(t *testing.T) {
	// When both service Env and extraEnv define the same key, service Env wins.
	cfg := &Config{
		Services: []ServiceConfig{
			{Name: "svc", Command: "true", Env: map[string]string{"SHARED": "service-wins"}},
		},
	}
	extraEnv := map[string]string{"SHARED": "preflight-value"}

	m := NewModel(t.Context(), cfg, extraEnv)

	assert.Equal(t, "service-wins", m.services[0].cfg.Env["SHARED"],
		"service-level env must override preflight extraEnv for the same key")
}

func TestNewModel_ColorCyclesFor7Services(t *testing.T) {
	// With 7 services and only 6 palette entries, the 7th service should
	// wrap around to the first color — no panic and correct modulo assignment.
	svcs := make([]ServiceConfig, 7)
	for i := range svcs {
		svcs[i] = ServiceConfig{Name: fmt.Sprintf("svc%d", i), Command: "true"}
	}

	m := NewModel(t.Context(), &Config{Services: svcs}, nil)

	require.Len(t, m.services, 7)
	assert.Equal(t, m.services[0].color, m.services[6].color,
		"7th service color should wrap around to the same as the 1st")
}

// --- renderServiceTable narrow viewport ------------------------------------

func TestRenderServiceTable_NarrowViewportClampsNameWidth(t *testing.T) {
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.width = 5 // very narrow — forces nameWidth < 10 guard

	out := m.renderServiceTable()

	assert.NotEmpty(t, out)
}

func TestRenderServiceTable_NarrowViewportWithMutedServiceDoesNotPanic(t *testing.T) {
	// Muted services use truncate(name, nameWidth-4). When nameWidth is clamped
	// to the minimum (10), nameWidth-4=6. This must not panic with an out-of-range slice.
	m := newInitializedModel(t, []ServiceConfig{{Name: "svc", Command: "true"}})
	m.width = 5
	m.services[0].muted = true

	assert.NotPanics(t, func() { m.renderServiceTable() })
}

// --- handleRestart cmd body coverage ---------------------------------------

func TestHandleRestart_CmdBodyRunsRestart(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true", Probe: ProbeNone}},
	}, nil)

	// Stop all supervisors when the test ends so their goroutines don't race
	// with log machinery in subsequent tests.
	t.Cleanup(m.StopAll)

	healthy := StateHealthy
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &healthy})

	_, cmd := m.handleRestart()
	require.NotNil(t, cmd)

	// Call the command body to cover the defer/Restart lines.
	msg := cmd()
	_, ok := msg.(restartDoneMsg)

	assert.True(t, ok)
}

func TestHandleRestart_PanicInRestartIsRecovered(t *testing.T) {
	// Close the update channel BEFORE calling Restart so that the first
	// sendUpdate inside Supervisor.Restart panics with "send on closed channel".
	// The handleRestart cmd body catches this via its recover() defer.
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true", Probe: ProbeNone}},
	}, nil)

	healthy := StateHealthy
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &healthy})

	_, cmd := m.handleRestart()
	require.NotNil(t, cmd)

	// Closing the channel causes the next sendUpdate call to panic.
	close(m.updateCh)

	// The panic must be caught — cmd() must return, not crash the process.
	assert.NotPanics(t, func() {
		cmd()
	})
}

// --- handleStop -----------------------------------------------------------

func TestHandleStop_SkipsWhenAlreadyStopped(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true", Probe: ProbeNone}},
	}, nil)
	t.Cleanup(m.StopAll)

	stopped := StateStopped
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &stopped})

	_, cmd := m.handleStop()
	assert.Nil(t, cmd, "should return nil cmd when already stopped")
}

func TestHandleStop_SkipsWhenRestarting(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true", Probe: ProbeNone}},
	}, nil)
	t.Cleanup(m.StopAll)

	restarting := StateRestarting
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &restarting})

	_, cmd := m.handleStop()
	assert.Nil(t, cmd, "should return nil cmd when restarting")
}

func TestHandleStop_ImmediatelyClearsMetrics(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true", Probe: ProbeNone}},
	}, nil)
	t.Cleanup(m.StopAll)

	healthy := StateHealthy
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &healthy})

	m.services[0].cpuPct = 42.0
	m.services[0].memMiB = 128.0
	m.services[0].pid = 999

	newM, _ := m.handleStop()
	updated := newM.(Model)

	assert.InDelta(t, 0.0, updated.services[0].cpuPct, 1e-9)
	assert.InDelta(t, 0.0, updated.services[0].memMiB, 1e-9)
	assert.Equal(t, 0, updated.services[0].pid)
}

func TestHandleStop_LaunchesCommandWhenRunning(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true", Probe: ProbeNone}},
	}, nil)
	t.Cleanup(m.StopAll)

	healthy := StateHealthy
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &healthy})

	_, cmd := m.handleStop()
	assert.NotNil(t, cmd, "should return a cmd when healthy")
}

func TestHandleStop_CmdBodyCallsStopAndNotify(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true", Probe: ProbeNone}},
	}, nil)
	t.Cleanup(m.StopAll)

	healthy := StateHealthy
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &healthy})

	_, cmd := m.handleStop()
	require.NotNil(t, cmd)

	// cmd() calls StopAndNotify which blocks until stop is done; result must be nil.
	result := cmd()
	assert.Nil(t, result)
}

func TestHandleStop_PanicInStopIsRecovered(t *testing.T) {
	m := NewModel(t.Context(), &Config{
		Services: []ServiceConfig{{Name: "svc", Command: "true", Probe: ProbeNone}},
	}, nil)

	healthy := StateHealthy
	m.applyServiceUpdate(ServiceUpdate{Name: "svc", State: &healthy})

	_, cmd := m.handleStop()
	require.NotNil(t, cmd)

	// Close the channel so StopAndNotify's sendUpdate panics.
	close(m.updateCh)

	assert.NotPanics(t, func() { cmd() })
}

// --- getProcessMetrics invalid PID ----------------------------------------

func TestGetProcessMetrics_InvalidPIDReturnsZero(t *testing.T) {
	cpu, mem := getProcessMetrics(-1)

	assert.InDelta(t, 0.0, cpu, 1e-9)
	assert.InDelta(t, 0.0, mem, 1e-9)
}

func TestGetProcessMetrics_PermissionDeniedMemInfoReturnsZeroMem(t *testing.T) {
	// PID 1 (init/launchd) is a valid process but MemoryInfo returns EPERM,
	// covering the err != nil branch inside getProcessMetrics.
	cpu, mem := getProcessMetrics(1)

	// CPU may be 0 or a small value; memory must be 0 due to the error path.
	assert.InDelta(t, 0.0, mem, 1e-9, "mem should be 0 when MemoryInfo is denied")
	assert.GreaterOrEqual(t, cpu, 0.0)
}

// --- metricsTickCmd closure coverage ---------------------------------------

func TestMetricsTickCmd_ClosureReturnsMetricsTickMsg(t *testing.T) {
	t.Parallel()

	cmd := metricsTickCmd()
	require.NotNil(t, cmd)

	// Calling cmd() blocks for 2 s until the tick fires, then returns metricsTickMsg.
	msg := cmd()

	_, ok := msg.(metricsTickMsg)
	assert.True(t, ok)
}

// --- Cmd() error-path tests ------------------------------------------------

func TestCmd_MissingConfigReturnsErrSilent(t *testing.T) {
	c := Cmd()

	var errBuf bytes.Buffer

	c.SetErr(&errBuf)

	require.NoError(t, c.Flags().Set("config", "/tmp/drdev-definitely-does-not-exist.yaml"))

	err := c.RunE(c, []string{})

	require.ErrorIs(t, err, cli.ErrSilent)
	assert.Contains(t, errBuf.String(), "not found")
}

func TestCmd_EmptyServicesReturnsError(t *testing.T) {
	path := writeTempConfig(t, "services: []\n")

	c := Cmd()
	require.NoError(t, c.Flags().Set("config", path))

	err := c.RunE(c, []string{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one service")
}

func TestCmd_InvalidYAMLReturnsError(t *testing.T) {
	path := writeTempConfig(t, "services: [\n  bad yaml\n")

	c := Cmd()
	require.NoError(t, c.Flags().Set("config", path))

	err := c.RunE(c, []string{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading config")
}

func TestCmd_ValidationErrorReturnsError(t *testing.T) {
	// Missing command field triggers validation failure.
	path := writeTempConfig(t, "services:\n  - name: svc\n")

	c := Cmd()
	require.NoError(t, c.Flags().Set("config", path))

	err := c.RunE(c, []string{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading config")
}

func TestCmd_TelemetryExtractorIncludesConfigAndSkipPreflight(t *testing.T) {
	c := Cmd()
	require.NoError(t, c.Flags().Set("config", "custom.yaml"))
	require.NoError(t, c.Flags().Set("skip-preflight", "true"))

	event, ok := telemetry.EventFor(c, []string{})

	require.True(t, ok)
	assert.Equal(t, "custom.yaml", event.EventProperties["config_file"])
	assert.Equal(t, true, event.EventProperties["skip_preflight"])
}

func TestCmd_PreflightErrorReturnsError(t *testing.T) {
	// Substitute runPreflightFn with a stub that returns an error to cover the
	// "pre-flight: %w" error-propagation path in Cmd()'s RunE.
	old := runPreflightFn

	t.Cleanup(func() { runPreflightFn = old })

	runPreflightFn = func(_ context.Context, _ bool) (map[string]string, error) {
		return nil, errors.New("pulumi stack not found")
	}

	path := writeTempConfig(t, "services:\n  - name: svc\n    command: echo hello\n")
	c := Cmd()
	require.NoError(t, c.Flags().Set("config", path))

	err := c.RunE(c, []string{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "pre-flight")
	assert.Contains(t, err.Error(), "pulumi stack not found")
}

func TestCmd_NoTTY_ReturnsRunningTUIError(t *testing.T) {
	// A valid config with one service. This exercises the runPreflight call
	// and the tui.Run call. In a test environment (no TTY), bubbletea returns
	// "could not open a new TTY" which is wrapped as "running TUI: ...".
	content := `
services:
  - name: svc
    command: echo hello
`
	path := writeTempConfig(t, content)
	c := Cmd()
	require.NoError(t, c.Flags().Set("config", path))

	err := c.RunE(c, []string{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "running TUI")
}
