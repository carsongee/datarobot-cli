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
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/datarobot/cli/internal/log"
	"github.com/datarobot/cli/tui"
)

// serviceColors is a palette assigned to services in order.
var serviceColors = []lipgloss.Color{
	lipgloss.Color("#7770F9"), // purple
	lipgloss.Color("#81FBA5"), // green
	lipgloss.Color("#F6EB61"), // yellow
	lipgloss.Color("#5C41FF"), // indigo
	lipgloss.Color("#FF8C00"), // orange
	lipgloss.Color("#B4B0FF"), // lavender
}

// serviceInfo holds all runtime state for one service in the TUI model.
type serviceInfo struct {
	cfg       ServiceConfig
	state     ProcessState
	logs      logRing
	pid       int
	cpuPct    float64
	memMiB    float64
	startedAt time.Time
	color     lipgloss.Color
	muted     bool
}

// Model is the top-level Bubble Tea model for `dr dev`.
type Model struct {
	services         []serviceInfo
	supervisors      []*Supervisor
	updateCh         chan ServiceUpdate
	parentCtx        context.Context
	selected         int
	logView          viewport.Model
	filterInput      textinput.Model
	filtering        bool // true while the filter text input is active
	width            int
	height           int
	logAutoScrl      bool // follow the latest log output
	initialized      bool // true after the first WindowSizeMsg
	quitting         bool // true while shutdown is in progress
	logFilteredCount int  // lines visible after applying filter
	logTotalCount    int  // total lines in current service's log ring
}

// Internal message types -----------------------------------------------

type serviceUpdateMsg ServiceUpdate

type metricsTickMsg time.Time

// restartDoneMsg is returned by the restart tea.Cmd (carries no data).
type restartDoneMsg struct{}

// shutdownDoneMsg signals that all supervisors have stopped gracefully.
type shutdownDoneMsg struct{}

// NewModel constructs the TUI model from a parsed config.
// extraEnv is merged into every service's environment (pre-flight values take
// lower precedence than keys already declared in the service config).
func NewModel(ctx context.Context, cfg *Config, extraEnv map[string]string) Model {
	const updateBuf = 2000

	ch := make(chan ServiceUpdate, updateBuf)
	services := make([]serviceInfo, len(cfg.Services))
	supervisors := make([]*Supervisor, len(cfg.Services))

	for i, sc := range cfg.Services {
		merged := make(map[string]string, len(extraEnv)+len(sc.Env))

		for k, v := range extraEnv {
			merged[k] = v
		}

		for k, v := range sc.Env {
			merged[k] = v
		}

		sc.Env = merged
		services[i] = serviceInfo{
			cfg:       sc,
			state:     StateStarting,
			startedAt: time.Now(),
			color:     serviceColors[i%len(serviceColors)],
		}
		supervisors[i] = NewSupervisor(sc, ch)
	}

	fi := textinput.New()
	fi.Placeholder = "filter logs…"
	fi.CharLimit = 120

	return Model{
		services:    services,
		supervisors: supervisors,
		updateCh:    ch,
		parentCtx:   ctx,
		logAutoScrl: true,
		filterInput: fi,
	}
}

// --- Bubble Tea interface -----------------------------------------------

func (m Model) Init() tea.Cmd {
	// Start all supervisors immediately. They run in goroutines and send
	// updates to m.updateCh, which the model drains via listenForUpdates.
	for _, sup := range m.supervisors {
		sup.Start(m.parentCtx)
	}

	return tea.Batch(
		listenForUpdates(m.updateCh),
		metricsTickCmd(),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)

	case serviceUpdateMsg:
		m.applyServiceUpdate(ServiceUpdate(msg))
		m.refreshLogViewport()

		return m, listenForUpdates(m.updateCh)

	case metricsTickMsg:
		m.collectMetrics()

		return m, metricsTickCmd()

	case restartDoneMsg:
		return m, nil

	case shutdownDoneMsg:
		return m, tea.Quit
	}

	// Forward unhandled messages to the text input when filtering is active so
	// cursor blink ticks and other internal textinput messages are processed.
	if m.filtering {
		var cmd tea.Cmd

		m.filterInput, cmd = m.filterInput.Update(msg)

		return m, cmd
	}

	return m, nil
}

func (m Model) View() string {
	if !m.initialized {
		return tui.DimStyle.Render("Initializing…") + "\n"
	}

	if m.quitting {
		return tui.DimStyle.Render("Stopping all services…") + "\n"
	}

	var sb strings.Builder

	sb.WriteString(m.renderHeader())
	sb.WriteString(m.renderServiceTable())
	sb.WriteString(m.renderLogPanel())
	sb.WriteString(m.renderFooter())

	return sb.String()
}

// --- Event handlers ---------------------------------------------------

func (m Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height

	if !m.initialized {
		m.initialized = true
		m.logView = viewport.New(m.width, m.logViewHeight())
	} else {
		m.logView.Width = m.width
		m.logView.Height = m.logViewHeight()
	}

	m.refreshLogViewport()

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ignore all keys while shutdown is in progress.
	if m.quitting {
		return m, nil
	}

	// While filter input is active, route keys to it.
	if m.filtering {
		return m.handleFilterKey(msg)
	}

	switch msg.String() {
	case "q", "esc":
		m.quitting = true

		return m, m.stopAllCmd()

	case "j", "down":
		return m.handleNavigate(1)

	case "k", "up":
		return m.handleNavigate(-1)

	case "r":
		return m.handleRestart()

	case "m":
		return m.handleMute()

	case "/":
		m.filtering = true
		m.refreshLogViewport()

		return m, m.filterInput.Focus()
	}

	return m.handleViewportKey(msg)
}

// handleViewportKey handles G (scroll to bottom), o (open browser), and any
// other key that should fall through to the log viewport for scrolling.
func (m Model) handleViewportKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "G":
		m.logAutoScrl = true
		m.logView.GotoBottom()

		return m, nil

	case "o":
		return m.handleOpenURL()
	}

	return m.handleScrollViewport(msg)
}

func (m Model) handleScrollViewport(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	m.logView, cmd = m.logView.Update(msg)

	if m.logView.AtBottom() {
		m.logAutoScrl = true
	} else {
		m.logAutoScrl = false
	}

	return m, cmd
}

func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filtering = false
		m.filterInput.Blur()
		m.filterInput.SetValue("")
		m.refreshLogViewport()

		return m, nil

	case "enter":
		m.filtering = false
		m.filterInput.Blur()
		m.refreshLogViewport()

		return m, nil
	}

	var cmd tea.Cmd

	m.filterInput, cmd = m.filterInput.Update(msg)
	m.refreshLogViewport()

	return m, cmd
}

func (m Model) handleNavigate(delta int) (tea.Model, tea.Cmd) {
	next := m.selected + delta

	if next >= 0 && next < len(m.services) {
		m.selected = next
		m.logAutoScrl = true
		m.refreshLogViewport()
	}

	return m, nil
}

func (m Model) handleMute() (tea.Model, tea.Cmd) {
	m.services[m.selected].muted = !m.services[m.selected].muted
	m.refreshLogViewport()

	return m, nil
}

func (m Model) handleOpenURL() (tea.Model, tea.Cmd) {
	svc := &m.services[m.selected]

	url := svc.cfg.URL
	if url == "" && svc.cfg.Port > 0 {
		url = fmt.Sprintf("http://localhost:%d", svc.cfg.Port)
	}

	if url == "" {
		return m, nil
	}

	return m, openBrowserCmd(url)
}

// openBrowserCmd returns a tea.Cmd that opens url in the system default browser.
// It uses the platform-appropriate command and runs it in a goroutine so it never
// blocks the TUI event loop.
func openBrowserCmd(url string) tea.Cmd {
	return openBrowserCmdForOS(runtime.GOOS, url)
}

func openBrowserCmdForOS(goos, url string) tea.Cmd {
	return func() tea.Msg {
		args := browserCmdArgs(goos, url)
		cmd := exec.Command(args[0], args[1:]...)

		if err := cmd.Start(); err != nil {
			log.Debug("dev: open browser failed", "url", url, "err", err)
		}

		return nil
	}
}

// browserCmdArgs returns the command and arguments needed to open url in the
// default browser for the given operating system. Extracted for testability.
func browserCmdArgs(goos, url string) []string {
	switch goos {
	case "darwin":
		return []string{"open", url}
	case "windows":
		return []string{"cmd", "/c", "start", url}
	default:
		return []string{"xdg-open", url}
	}
}

func (m Model) handleRestart() (tea.Model, tea.Cmd) {
	idx := m.selected

	// Skip if a restart is already in flight.
	if m.services[idx].state == StateRestarting {
		return m, nil
	}

	sup := m.supervisors[idx]
	ctx := m.parentCtx

	// Reset metrics immediately so stale values don't linger.
	m.services[idx].cpuPct = 0
	m.services[idx].memMiB = 0
	m.services[idx].pid = 0
	m.logAutoScrl = true

	// Run restart in a goroutine so it doesn't block the UI. The supervisor
	// sends StateRestarting/StateStarting/StateHealthy via the update channel.
	return m, func() tea.Msg {
		defer func() {
			if r := recover(); r != nil {
				log.Debug("dev: restart goroutine panic recovered", "service", sup.cfg.Name, "panic", r)
			}
		}()

		sup.Restart(ctx)

		return restartDoneMsg{}
	}
}

// --- Internal helpers --------------------------------------------------

func listenForUpdates(ch chan ServiceUpdate) tea.Cmd {
	return func() tea.Msg {
		return serviceUpdateMsg(<-ch)
	}
}

func metricsTickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return metricsTickMsg(t)
	})
}

func (m *Model) applyServiceUpdate(u ServiceUpdate) {
	for i := range m.services {
		if m.services[i].cfg.Name != u.Name {
			continue
		}

		if u.State != nil {
			m.services[i].state = *u.State

			switch *u.State {
			case StateRestarting:
				m.services[i].startedAt = time.Now()
				m.services[i].logs.add(LogEntry{
					Line:      "── restarted ──",
					Timestamp: time.Now(),
					IsSep:     true,
				})
			case StateStarting:
				m.services[i].startedAt = time.Now()
			case StateCrashed, StateStopped:
				m.services[i].pid = 0
				m.services[i].cpuPct = 0
				m.services[i].memMiB = 0
			case StateHealthy:
				// No additional state to update — metrics arrive separately via metricsTickMsg.
			}
		}

		if u.PID != 0 {
			m.services[i].pid = u.PID
		}

		if u.LogLine != nil {
			m.services[i].logs.add(*u.LogLine)
		}

		return
	}
}

func (m *Model) collectMetrics() {
	for i := range m.services {
		if m.services[i].pid <= 0 {
			continue
		}

		cpu, mem := getProcessMetrics(m.services[i].pid)
		m.services[i].cpuPct = cpu
		m.services[i].memMiB = mem
	}
}

// StopAll stops every supervisor synchronously. It is safe to call more than
// once and safe to call concurrently with the in-TUI shutdown path — each
// Supervisor.Stop is idempotent (cancel + Wait). Call this from a defer in
// RunE so child processes are never left orphaned regardless of how the TUI
// exits (graceful q/esc, Ctrl-C via InterruptibleModel, or panic).
func (m Model) StopAll() {
	var wg sync.WaitGroup

	for _, sup := range m.supervisors {
		wg.Add(1)

		go func(s *Supervisor) {
			defer wg.Done()
			defer func() { _ = recover() }()

			s.Stop()
		}(sup)
	}

	wg.Wait()
}

// stopAllCmd returns a tea.Cmd that stops all supervisors concurrently and
// then sends shutdownDoneMsg so Update can call tea.Quit without blocking.
func (m Model) stopAllCmd() tea.Cmd {
	return func() tea.Msg {
		m.StopAll()

		return shutdownDoneMsg{}
	}
}

func (m *Model) refreshLogViewport() {
	if len(m.services) == 0 || !m.initialized {
		return
	}

	m.logView.Height = m.logViewHeight()

	svc := &m.services[m.selected]
	entries := svc.logs.all()
	colorStyle := lipgloss.NewStyle().Foreground(svc.color)
	filter := strings.ToLower(m.filterInput.Value())

	lines := filterAndRenderEntries(entries, filter, colorStyle)

	m.logFilteredCount = len(lines)
	m.logTotalCount = len(entries)

	var content string

	switch {
	case len(lines) > 0:
		content = strings.Join(lines, "\n")
	case len(entries) == 0:
		content = tui.DimStyle.Render("  Waiting for output…")
	default:
		content = tui.DimStyle.Render("  No matching lines for filter \"" + filter + "\"")
	}

	m.logView.SetContent(content)

	if m.logAutoScrl {
		m.logView.GotoBottom()
	}
}

func (m Model) logViewHeight() int {
	// Rows consumed: header(1) + sep(1) + col-header(1) + sep(1) + service-rows
	// + sep(1, final) + log-label(1) + footer(1) = 7 + services
	// + filter-input row when active
	fixed := 7 + len(m.services)
	if m.filtering {
		fixed++
	}

	h := m.height - fixed

	if h < 5 {
		h = 5
	}

	return h
}

// --- View renderers ---------------------------------------------------

func (m Model) renderHeader() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(tui.DrGreen).Render("dr dev")
	count := fmt.Sprintf(" · %d service", len(m.services))

	if len(m.services) != 1 {
		count += "s"
	}

	headerBg := tui.GetAdaptiveColor(lipgloss.Color("235"), lipgloss.Color("252"))
	bar := lipgloss.NewStyle().
		Width(m.width).
		Background(headerBg).
		Render(title + tui.DimStyle.Render(count))

	return bar + "\n"
}

func (m Model) renderServiceTable() string {
	const (
		colStatus = 12
		colCPU    = 7
		colMem    = 8
		colPort   = 7
		colAge    = 8
	)

	// 12 = cursor(2) + 5 column-separators × 2 chars each(10)
	nameWidth := m.width - colStatus - colCPU - colMem - colPort - colAge - 12

	if nameWidth < 10 {
		nameWidth = 10
	}

	sep := tui.DimStyle.Render(strings.Repeat("─", m.width))
	colHead := tui.DimStyle

	header := "  " +
		padCell(colHead.Render("SERVICE"), nameWidth) + "  " +
		padCell(colHead.Render("STATUS"), colStatus) + "  " +
		padCell(colHead.Render("CPU"), colCPU) + "  " +
		padCell(colHead.Render("MEM"), colMem) + "  " +
		padCell(colHead.Render("PORT"), colPort) + "  " +
		padCell(colHead.Render("AGE"), colAge)

	var sb strings.Builder

	sb.WriteString(sep + "\n")
	sb.WriteString(header + "\n")
	sb.WriteString(sep + "\n")

	for i := range m.services {
		sb.WriteString(m.renderServiceRow(i, nameWidth, colStatus, colCPU, colMem, colPort, colAge))
		sb.WriteString("\n")
	}

	sb.WriteString(sep + "\n")

	return sb.String()
}

func (m Model) renderServiceRow(idx, nameWidth, colStatus, colCPU, colMem, colPort, colAge int) string {
	svc := &m.services[idx]
	colorStyle := lipgloss.NewStyle().Foreground(svc.color)

	cursor := "  "
	if idx == m.selected {
		cursor = lipgloss.NewStyle().Foreground(tui.DrGreen).Bold(true).Render("▶ ")
	}

	var name string

	if svc.muted {
		// Reserve 4 chars for the dim " [m]" badge so total printable width stays nameWidth.
		trimmed := truncate(svc.cfg.Name, nameWidth-4)
		name = colorStyle.Render(trimmed) + tui.DimStyle.Render(" [m]")
	} else {
		name = colorStyle.Render(truncate(svc.cfg.Name, nameWidth))
	}

	port := "-"
	if svc.cfg.Port > 0 {
		port = strconv.Itoa(svc.cfg.Port)
	}

	return cursor +
		padCell(name, nameWidth) + "  " +
		padCell(renderStatus(svc.state), colStatus) + "  " +
		padCell(renderCPU(svc.cpuPct, svc.state), colCPU) + "  " +
		padCell(renderMem(svc.memMiB, svc.state), colMem) + "  " +
		padCell(port, colPort) + "  " +
		padCell(formatDuration(time.Since(svc.startedAt)), colAge)
}

func renderStatus(state ProcessState) string {
	switch state {
	case StateHealthy:
		return lipgloss.NewStyle().Foreground(tui.DrGreen).Render("✓ Healthy")
	case StateStarting:
		return lipgloss.NewStyle().Foreground(tui.DrYellow).Render("↻ Starting")
	case StateCrashed:
		return lipgloss.NewStyle().Foreground(tui.DrRed).Bold(true).Render("✗ Crashed")
	case StateStopped:
		return tui.DimStyle.Render("■ Stopped")
	case StateRestarting:
		return lipgloss.NewStyle().Foreground(tui.DrYellow).Render("↺ Restart…")
	default:
		return tui.DimStyle.Render("? Unknown")
	}
}

func renderCPU(pct float64, state ProcessState) string {
	if state != StateHealthy && state != StateStarting {
		return tui.DimStyle.Render("-")
	}

	if pct == 0 {
		return tui.DimStyle.Render("-")
	}

	return fmt.Sprintf("%.1f%%", pct)
}

func renderMem(mib float64, state ProcessState) string {
	if state != StateHealthy && state != StateStarting {
		return tui.DimStyle.Render("-")
	}

	if mib == 0 {
		return tui.DimStyle.Render("-")
	}

	if mib < 1024 {
		return fmt.Sprintf("%.0fMiB", mib)
	}

	return fmt.Sprintf("%.1fGiB", mib/1024)
}

func (m Model) renderLogPanel() string {
	if len(m.services) == 0 {
		return ""
	}

	svc := &m.services[m.selected]
	colorStyle := lipgloss.NewStyle().Foreground(svc.color)

	muteTag := ""
	if svc.muted {
		muteTag = " " + tui.DimStyle.Render("[muted]")
	}

	filterTag := ""

	if filter := m.filterInput.Value(); filter != "" {
		if m.logTotalCount > 0 {
			filterTag = " " + tui.DimStyle.Render(fmt.Sprintf("filter: %s (%d/%d)", filter, m.logFilteredCount, m.logTotalCount))
		} else {
			filterTag = " " + tui.DimStyle.Render("filter: "+filter)
		}
	}

	label := tui.DimStyle.Render("Logs: ") + colorStyle.Bold(true).Render(svc.cfg.Name) + muteTag + filterTag

	// filterRow is rendered in both muted and normal paths so the layout is
	// structurally identical (logViewHeight already reserves a row for it).
	filterRow := ""
	if m.filtering {
		filterRow = tui.DimStyle.Render("/") + m.filterInput.View() + "\n"
	}

	if svc.muted {
		h := m.logViewHeight()
		blank := strings.Repeat("\n", h-1)

		return label + "\n" + filterRow + tui.DimStyle.Render("  (logs muted — press m to unmute)") + blank + "\n"
	}

	m.logView.Width = m.width
	m.logView.Height = m.logViewHeight()

	return label + "\n" + filterRow + m.logView.View() + "\n"
}

func (m Model) renderFooter() string {
	if m.filtering {
		return tui.DimStyle.Render("enter apply filter  ·  esc clear filter") + "\n"
	}

	return tui.DimStyle.Render("j/k navigate  ·  r restart  ·  m mute  ·  / filter  ·  G bottom  ·  o open  ·  q quit") + "\n"
}

// formatDuration returns a compact human-readable duration.
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}

	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	mins := d / time.Minute
	d -= mins * time.Minute
	sec := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, mins)
	}

	if mins > 0 {
		return fmt.Sprintf("%dm%ds", mins, sec)
	}

	return fmt.Sprintf("%ds", sec)
}

// truncate clips s to at most n display characters, appending "…" if clipped.
func truncate(s string, n int) string {
	runes := []rune(s)

	if len(runes) <= n {
		return s
	}

	return string(runes[:n-1]) + "…"
}

// padCell pads text to exactly width visual columns. Unlike fmt.Sprintf("%-*s"),
// this correctly handles ANSI escape sequences embedded in text.
func padCell(text string, width int) string {
	return lipgloss.NewStyle().Width(width).Render(text)
}

// filterAndRenderEntries returns the formatted log lines that pass the filter.
// Separator entries (IsSep) are rendered dimmed without a timestamp or color.
func filterAndRenderEntries(entries []LogEntry, filter string, colorStyle lipgloss.Style) []string {
	var lines []string

	for _, e := range entries {
		if filter != "" && !strings.Contains(strings.ToLower(e.Line), filter) {
			continue
		}

		if e.IsSep {
			lines = append(lines, tui.DimStyle.Render("  "+e.Line))
		} else {
			ts := e.Timestamp.Format("15:04:05")
			lines = append(lines, tui.DimStyle.Render(ts)+"  "+colorStyle.Render(e.Line))
		}
	}

	return lines
}
