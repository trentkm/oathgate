package main

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/trentkm/oathgate"
)

const (
	headerHeight       = 1
	footerHeight       = 1
	inspectorWidth     = 31
	stackedInspector   = 6
	sideLayoutMinimum  = 88
	maxTerminalColumns = 400
	maxTerminalRows    = 200
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#F4F4F5"))
	subtleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#8B949E"))
	labelStyle = lipgloss.NewStyle().
			Width(10).
			Foreground(lipgloss.Color("#8B949E"))
	activeStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#50C878"))
	controlStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#E5B567"))
	valueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#D8DEE9"))
	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF6B6B"))
	inspectorBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(lipgloss.Color("#4B5563"))
)

type statsTickMsg time.Time

type resizeDoneMsg struct {
	cols int
	rows int
	err  error
}

type sandboxLayout struct {
	width              int
	height             int
	sideInspector      bool
	tooSmall           bool
	terminalAreaWidth  int
	terminalAreaHeight int
	inspectorWidth     int
	inspectorHeight    int
	maxBoxCols         int
	maxBoxRows         int
	terminalOriginX    int
	terminalOriginY    int
}

func calculateLayout(width, height int) sandboxLayout {
	layout := sandboxLayout{
		width:           max(1, width),
		height:          max(1, height),
		tooSmall:        width < 12 || height < 7,
		terminalOriginX: 1,
		terminalOriginY: headerHeight + 1,
	}
	workHeight := max(1, height-headerHeight-footerHeight)

	if width >= sideLayoutMinimum && workHeight >= 8 {
		layout.sideInspector = true
		layout.inspectorWidth = inspectorWidth
		layout.inspectorHeight = workHeight
		layout.terminalAreaWidth = max(4, width-inspectorWidth-1)
		layout.terminalAreaHeight = workHeight
	} else {
		layout.terminalAreaWidth = max(4, width)
		layout.terminalAreaHeight = workHeight
		if workHeight >= 12 {
			layout.inspectorWidth = max(4, width)
			layout.inspectorHeight = min(stackedInspector, workHeight-6)
			layout.terminalAreaHeight -= layout.inspectorHeight
		}
	}

	layout.maxBoxCols = max(2, layout.terminalAreaWidth-2)
	layout.maxBoxRows = max(2, layout.terminalAreaHeight-2)
	return layout
}

type sandbox struct {
	term      oathgate.Model
	transport *observedTransport
	command   string
	debug     *debugLog

	width  int
	height int
	ready  bool

	boxCols  int
	boxRows  int
	termCols int
	termRows int
	autoFit  bool
	linked   bool

	frameTotal  int64
	frameWindow int
	frameRate   int
	wheelUp     int
	wheelDown   int
	wheelSide   int
	wheelMax    time.Duration
	lastAction  string
}

func newSandbox(term oathgate.Model, transport *observedTransport, command string, logs ...*debugLog) sandbox {
	term.Focus()
	boxCols, boxRows := term.Size()
	termCols, termRows := term.TerminalSize()
	var debug *debugLog
	if len(logs) > 0 {
		debug = logs[0]
	}
	debug.Printf(
		"model_create box=%dx%d terminal=%dx%d focused=true",
		boxCols,
		boxRows,
		termCols,
		termRows,
	)
	return sandbox{
		term:       term,
		transport:  transport,
		command:    filepath.Base(command),
		debug:      debug,
		boxCols:    boxCols,
		boxRows:    boxRows,
		termCols:   termCols,
		termRows:   termRows,
		autoFit:    true,
		linked:     boxCols == termCols && boxRows == termRows,
		lastAction: "PTY started",
	}
}

func (s sandbox) Init() tea.Cmd {
	s.debug.Progress("init")
	s.debug.Printf("model_init")
	return tea.Batch(s.term.Init(), statsTick())
}

func statsTick() tea.Cmd {
	return tea.Tick(time.Second, func(now time.Time) tea.Msg {
		return statsTickMsg(now)
	})
}

func (s sandbox) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	defer s.debug.Recover("sandbox.Update")
	s.debug.Progress("update")
	defer s.debug.Progress("idle")
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.width, s.height, s.ready = msg.Width, msg.Height, true
		layout := calculateLayout(s.width, s.height)
		s.debug.Printf(
			"window_resize width=%d height=%d side_inspector=%t max_box=%dx%d",
			msg.Width,
			msg.Height,
			layout.sideInspector,
			layout.maxBoxCols,
			layout.maxBoxRows,
		)
		if layout.tooSmall {
			return s, nil
		}
		if s.autoFit {
			return s, s.setBoxSize(layout.maxBoxCols, layout.maxBoxRows)
		}
		cols := min(s.boxCols, layout.maxBoxCols)
		rows := min(s.boxRows, layout.maxBoxRows)
		if cols != s.boxCols || rows != s.boxRows {
			s.lastAction = "box clamped to window"
			return s, s.setBoxSize(cols, rows)
		}
		return s, nil

	case statsTickMsg:
		s.frameRate = s.frameWindow
		s.frameWindow = 0
		stats := s.transport.Snapshot()
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		s.debug.Printf(
			"heartbeat focused=%t scroll=%d frames_per_second=%d frames_total=%d "+
				"output_bytes=%d output_chunks=%d input_bytes=%d writes=%d "+
				"wheel_up=%d wheel_down=%d wheel_horizontal=%d wheel_update_max=%s "+
				"heap_bytes=%d heap_objects=%d goroutines=%d",
			s.term.Focused(),
			s.term.Scrolled(),
			s.frameRate,
			s.frameTotal,
			stats.OutputBytes,
			stats.OutputChunks,
			stats.InputBytes,
			stats.Writes,
			s.wheelUp,
			s.wheelDown,
			s.wheelSide,
			s.wheelMax.Round(time.Microsecond),
			memory.HeapAlloc,
			memory.HeapObjects,
			runtime.NumGoroutine(),
		)
		s.wheelUp = 0
		s.wheelDown = 0
		s.wheelSide = 0
		s.wheelMax = 0
		return s, statsTick()

	case resizeDoneMsg:
		if msg.err != nil {
			s.lastAction = fmt.Sprintf("resize failed: %v", msg.err)
			s.debug.Printf("resize_complete cols=%d rows=%d error=%q", msg.cols, msg.rows, msg.err)
		} else if msg.cols == s.termCols && msg.rows == s.termRows {
			s.lastAction = fmt.Sprintf("terminal resized to %dx%d", msg.cols, msg.rows)
			s.debug.Printf("resize_complete cols=%d rows=%d", msg.cols, msg.rows)
		}
		return s, nil

	case oathgate.FrameMsg:
		if msg.ID == s.term.ID() {
			s.frameTotal++
			s.frameWindow++
		}

	case tea.KeyPressMsg:
		if s.term.Focused() {
			wasFocused := s.term.Focused()
			var cmd tea.Cmd
			s.term, cmd = s.term.Update(msg)
			if wasFocused && !s.term.Focused() {
				s.lastAction = "terminal released"
				s.debug.Printf("focus_change focused=false")
			}
			return s, cmd
		}
		return s.handleControl(msg.String())

	case tea.MouseClickMsg:
		mouse := msg.Mouse()
		if mouse.Button == tea.MouseLeft && s.insideTerminal(mouse.X, mouse.Y) {
			s.term.Focus()
			s.lastAction = "terminal focused"
			s.debug.Printf("focus_change focused=true source=mouse")
		}
		return s, nil

	case tea.MouseWheelMsg:
		mouse := msg.Mouse()
		if s.term.Focused() && s.insideTerminal(mouse.X, mouse.Y) {
			started := time.Now()
			var cmd tea.Cmd
			s.term, cmd = s.term.Update(msg)
			switch mouse.Button {
			case tea.MouseWheelUp:
				s.wheelUp++
			case tea.MouseWheelDown:
				s.wheelDown++
			case tea.MouseWheelLeft, tea.MouseWheelRight:
				s.wheelSide++
			}
			s.wheelMax = max(s.wheelMax, time.Since(started))
			return s, cmd
		}
		return s, nil
	}

	var cmd tea.Cmd
	s.term, cmd = s.term.Update(msg)
	return s, cmd
}

func (s sandbox) handleControl(key string) (tea.Model, tea.Cmd) {
	layout := calculateLayout(s.width, s.height)
	s.debug.Printf("control key=%q", key)
	switch key {
	case "q", "ctrl+c":
		s.debug.Printf("quit")
		return s, tea.Quit
	case "enter", "f":
		s.term.Focus()
		s.lastAction = "terminal focused"
		s.debug.Printf("focus_change focused=true source=keyboard")
		return s, nil
	case "left":
		s.autoFit = false
		s.lastAction = "box width changed"
		return s, s.setBoxSize(max(2, s.boxCols-2), s.boxRows)
	case "right":
		s.autoFit = false
		s.lastAction = "box width changed"
		return s, s.setBoxSize(min(layout.maxBoxCols, s.boxCols+2), s.boxRows)
	case "up":
		s.autoFit = false
		s.lastAction = "box height changed"
		return s, s.setBoxSize(s.boxCols, min(layout.maxBoxRows, s.boxRows+1))
	case "down":
		s.autoFit = false
		s.lastAction = "box height changed"
		return s, s.setBoxSize(s.boxCols, max(2, s.boxRows-1))
	case "shift+left":
		return s, s.setTerminalSize(max(2, s.termCols-2), s.termRows)
	case "shift+right":
		return s, s.setTerminalSize(min(maxTerminalColumns, s.termCols+2), s.termRows)
	case "shift+up":
		return s, s.setTerminalSize(s.termCols, min(maxTerminalRows, s.termRows+1))
	case "shift+down":
		return s, s.setTerminalSize(s.termCols, max(2, s.termRows-1))
	case "a":
		s.autoFit = true
		s.lastAction = "box fitted to window"
		return s, s.setBoxSize(layout.maxBoxCols, layout.maxBoxRows)
	case "=":
		s.lastAction = "terminal synchronized"
		return s, s.setTerminalSize(s.boxCols, s.boxRows)
	case "1":
		s.lastAction = "preset: synchronized"
		return s, s.setTerminalSize(s.boxCols, s.boxRows)
	case "2":
		s.lastAction = "preset: wide terminal"
		return s, s.setTerminalSize(min(maxTerminalColumns, s.boxCols+40), s.boxRows)
	case "3":
		s.lastAction = "preset: tall terminal"
		return s, s.setTerminalSize(s.boxCols, min(maxTerminalRows, s.boxRows+16))
	case "4":
		s.lastAction = "preset: large terminal"
		return s, s.setTerminalSize(
			min(maxTerminalColumns, s.boxCols+40),
			min(maxTerminalRows, s.boxRows+16),
		)
	case "home":
		s.term.ScrollToBottom()
		s.lastAction = "scrollback returned to live"
		return s, nil
	default:
		return s, nil
	}
}

func (s *sandbox) setBoxSize(cols, rows int) tea.Cmd {
	layout := calculateLayout(s.width, s.height)
	cols = clamp(cols, 2, layout.maxBoxCols)
	rows = clamp(rows, 2, layout.maxBoxRows)
	oldCols, oldRows := s.boxCols, s.boxRows
	s.boxCols, s.boxRows = cols, rows
	s.debug.Printf(
		"box_resize from=%dx%d to=%dx%d linked=%t auto=%t",
		oldCols,
		oldRows,
		cols,
		rows,
		s.linked,
		s.autoFit,
	)

	var resize tea.Cmd
	s.term, resize = s.term.SetSize(cols, rows)
	if s.linked {
		s.termCols, s.termRows = cols, rows
		return resize
	}

	// SetSize temporarily reunifies the emulator and box. In split mode the
	// real terminal stays independent, so restore the emulator dimensions and
	// discard SetSize's transport resize command.
	s.term.SetTerminalSize(s.termCols, s.termRows)
	return nil
}

func (s *sandbox) setTerminalSize(cols, rows int) tea.Cmd {
	cols = clamp(cols, 2, maxTerminalColumns)
	rows = clamp(rows, 2, maxTerminalRows)
	oldCols, oldRows := s.termCols, s.termRows
	s.termCols, s.termRows = cols, rows
	s.linked = cols == s.boxCols && rows == s.boxRows
	s.term.SetTerminalSize(cols, rows)
	s.debug.Printf(
		"terminal_resize_request from=%dx%d to=%dx%d linked=%t",
		oldCols,
		oldRows,
		cols,
		rows,
		s.linked,
	)

	transport := s.transport
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := transport.Resize(ctx, cols, rows)
		return resizeDoneMsg{cols: cols, rows: rows, err: err}
	}
}

func (s sandbox) insideTerminal(x, y int) bool {
	if !s.ready {
		return false
	}
	layout := calculateLayout(s.width, s.height)
	return x >= layout.terminalOriginX &&
		x < layout.terminalOriginX+s.boxCols &&
		y >= layout.terminalOriginY &&
		y < layout.terminalOriginY+s.boxRows
}

func (s sandbox) View() tea.View {
	defer s.debug.Recover("sandbox.View")
	s.debug.Progress("view")
	defer s.debug.Progress("idle")
	started := time.Now()
	defer func() {
		s.debug.LogSlowView(
			started,
			s.term.Scrolled(),
			s.boxCols,
			s.boxRows,
			s.termCols,
			s.termRows,
		)
	}()

	view := tea.NewView("")
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	view.WindowTitle = "Oathgate sandbox"
	if !s.ready {
		view.SetContent("starting oathgate sandbox...")
		return view
	}

	layout := calculateLayout(s.width, s.height)
	if layout.tooSmall {
		content := lipgloss.NewStyle().
			Width(layout.width).
			Height(layout.height).
			Render(ansi.Truncate("Window is too small for the sandbox.", layout.width, ""))
		view.SetContent(content)
		return view
	}

	header := s.renderHeader(layout.width)
	terminal := s.renderTerminal()
	terminalArea := lipgloss.NewStyle().
		Width(layout.terminalAreaWidth).
		Height(layout.terminalAreaHeight).
		Render(terminal)

	body := terminalArea
	if layout.inspectorWidth > 0 && layout.inspectorHeight > 0 {
		inspector := s.renderInspector(
			layout.inspectorWidth,
			layout.inspectorHeight,
			!layout.sideInspector,
		)
		if layout.sideInspector {
			body = lipgloss.JoinHorizontal(lipgloss.Top, terminalArea, " ", inspector)
		} else {
			body = lipgloss.JoinVertical(lipgloss.Left, terminalArea, inspector)
		}
	}
	footer := s.renderFooter(layout.width)
	view.SetContent(lipgloss.JoinVertical(lipgloss.Left, header, body, footer))

	if s.term.Focused() {
		if x, y, ok := s.term.Cursor(); ok {
			view.Cursor = tea.NewCursor(
				layout.terminalOriginX+x,
				layout.terminalOriginY+y,
			)
		}
	}
	return view
}

func (s sandbox) renderHeader(width int) string {
	focus := subtleStyle.Render("SANDBOX")
	if s.term.Focused() {
		focus = activeStyle.Render("TERMINAL")
	}
	left := titleStyle.Render("OATHGATE") + subtleStyle.Render("  "+s.command)
	right := focus
	return fixedLine(left, right, width)
}

func (s sandbox) renderTerminal() string {
	borderColor := lipgloss.Color("#4B5563")
	if s.term.Focused() {
		borderColor = lipgloss.Color("#50C878")
	}
	return lipgloss.NewStyle().
		Width(s.boxCols + 2).
		Height(s.boxRows + 2).
		Border(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		Render(s.term.View())
}

func (s sandbox) renderInspector(width, height int, compact bool) string {
	innerWidth := max(1, width-2)
	innerHeight := max(1, height-2)
	stats := s.transport.Snapshot()

	cursor := "hidden"
	if x, y, ok := s.term.Cursor(); ok {
		cursor = fmt.Sprintf("%d, %d", x, y)
	}
	focus := "sandbox"
	if s.term.Focused() {
		focus = "terminal"
	}
	screen := "main"
	if s.term.AltScreen() {
		screen = "alternate"
	}
	link := "split"
	if s.linked {
		link = "linked"
	}
	boxMode := "manual"
	if s.autoFit {
		boxMode = "auto"
	}
	stream := "open"
	if stats.StreamClosed {
		stream = "ended"
	}
	logName := "disabled"
	if s.debug.Path() != "" {
		logName = filepath.Base(s.debug.Path())
	}

	var lines []string
	if compact {
		lines = []string{
			controlStyle.Render("STATE") + "  " +
				fmt.Sprintf("focus %s | box %dx%d (%s) | terminal %dx%d (%s)",
					focus, s.boxCols, s.boxRows, boxMode, s.termCols, s.termRows, link),
			controlStyle.Render("VIEW") + "   " +
				fmt.Sprintf("cursor %s | screen %s | scroll %d | frames %d/s",
					cursor, screen, s.term.Scrolled(), s.frameRate),
			controlStyle.Render("IO") + "     " +
				fmt.Sprintf("out %s/%d | in %s/%d | resizes %d | stream %s",
					formatBytes(stats.OutputBytes), stats.OutputChunks,
					formatBytes(stats.InputBytes), stats.Writes, stats.Resizes, stream),
			controlStyle.Render("LAST") + "   " + s.lastAction,
			controlStyle.Render("LOG") + "    " + logName,
		}
		if stats.LastError != "" {
			lines = append(lines, errorStyle.Render("ERROR")+"  "+stats.LastError)
		}
	} else {
		lines = []string{
			controlStyle.Render("INSPECTOR"),
			metric("focus", focus),
			metric("box", fmt.Sprintf("%dx%d  %s", s.boxCols, s.boxRows, boxMode)),
			metric("terminal", fmt.Sprintf("%dx%d  %s", s.termCols, s.termRows, link)),
			metric("cursor", cursor),
			metric("screen", screen),
			metric("scroll", fmt.Sprintf("%d lines", s.term.Scrolled())),
			metric("frames", fmt.Sprintf("%d/s  %d total", s.frameRate, s.frameTotal)),
			metric("output", fmt.Sprintf("%s  %d chunks", formatBytes(stats.OutputBytes), stats.OutputChunks)),
			metric("input", fmt.Sprintf("%s  %d writes", formatBytes(stats.InputBytes), stats.Writes)),
			metric("resizes", fmt.Sprintf("%d  last %dx%d", stats.Resizes, stats.LastResize[0], stats.LastResize[1])),
			metric("stream", stream),
			metric("log", logName),
			"",
			subtleStyle.Render(ansi.Truncate(s.lastAction, innerWidth, "")),
		}
		if stats.LastError != "" {
			lines = append(lines, errorStyle.Render(stats.LastError))
		}
	}

	content := fitLines(lines, innerWidth, innerHeight)
	return inspectorBorderStyle.
		Width(width).
		Height(height).
		Render(content)
}

func (s sandbox) renderFooter(width int) string {
	var left, right string
	if s.term.Focused() {
		left = activeStyle.Render("TERMINAL")
		right = subtleStyle.Render("ctrl+q release | mouse wheel scroll")
	} else {
		left = controlStyle.Render("SANDBOX")
		right = subtleStyle.Render(
			"enter focus | arrows box | shift+arrows terminal | a fit | = sync | 1-4 presets | q quit",
		)
	}
	return fixedLine(left, right, width)
}

func metric(label, value string) string {
	return labelStyle.Render(label) + valueStyle.Render(value)
}

func fixedLine(left, right string, width int) string {
	if width <= 0 {
		return ""
	}
	leftWidth := ansi.StringWidth(left)
	rightWidth := ansi.StringWidth(right)
	if leftWidth+1+rightWidth > width {
		return ansi.Truncate(left+"  "+right, width, "")
	}
	return left + strings.Repeat(" ", width-leftWidth-rightWidth) + right
}

func fitLines(lines []string, width, height int) string {
	if height <= 0 {
		return ""
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	for index, line := range lines {
		lines[index] = ansi.Truncate(line, width, "")
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func formatBytes(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	value := float64(bytes)
	units := []string{"KiB", "MiB", "GiB"}
	for _, unit := range units {
		value /= 1024
		if value < 1024 || unit == units[len(units)-1] {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return ""
}

func clamp(value, low, high int) int {
	return min(max(value, low), high)
}
