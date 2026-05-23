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
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
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

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.Empty(t, cfg.Services)
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

func TestRenderCPU(t *testing.T) {
	assert.NotEmpty(t, renderCPU(0, StateHealthy))
	assert.NotEmpty(t, renderCPU(42.5, StateHealthy))
	assert.NotEmpty(t, renderCPU(42.5, StateCrashed))
	assert.NotEmpty(t, renderCPU(42.5, StateStopped))
}

func TestRenderMem(t *testing.T) {
	assert.NotEmpty(t, renderMem(0, StateHealthy))
	assert.NotEmpty(t, renderMem(512, StateHealthy))
	assert.NotEmpty(t, renderMem(1024, StateHealthy))
	assert.NotEmpty(t, renderMem(512, StateCrashed))
}

func TestRenderMemUnit(t *testing.T) {
	assert.Contains(t, renderMem(512, StateHealthy), "MiB")
	assert.Contains(t, renderMem(2048, StateHealthy), "GiB")
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

func TestTruncate(t *testing.T) {
	assert.Equal(t, "hello", truncate("hello", 10))
	assert.Equal(t, "hell…", truncate("hello world", 5))
	assert.Equal(t, "hello", truncate("hello", 5))
}

func TestRenderStatus(t *testing.T) {
	// Verify each state produces non-empty output.
	states := []ProcessState{StateHealthy, StateStarting, StateCrashed, StateStopped, StateRestarting}

	for _, s := range states {
		assert.NotEmpty(t, renderStatus(s), "renderStatus(%v)", s)
	}
}

// --- logRing tests ------------------------------------------------------

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
