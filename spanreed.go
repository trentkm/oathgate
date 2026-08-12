// Package spanreed is an embeddable terminal for Bubble Tea v2: a
// component that renders a live terminal session inside any TUI, with
// keyboard forwarding while focused, scrollback, tmux-style clipping, and
// box-relative cursor reporting.
package spanreed

import (
	"context"
	"image/color"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

const (
	// framePeriod caps UI wake-ups: a stream writing byte-by-byte must not
	// schedule a render per byte.
	framePeriod = 33 * time.Millisecond
	// scrollBurstPeriod keeps high-resolution trackpads from changing the
	// view faster than a terminal can draw it.
	scrollBurstPeriod = framePeriod
)

// DefaultScrollback bounds the widget's emulator history.
const DefaultScrollback = 2000

var lastID atomic.Int64

// FrameMsg says a widget has a new frame to draw. Route it (like every
// message) through the widget's Update; unrelated instances ignore it.
type FrameMsg struct {
	ID int64
}

type scrollMsg struct {
	ID int64
}

// Option configures New.
type Option func(*Model)

// WithScrollback caps emulator history in lines.
func WithScrollback(lines int) Option {
	return func(m *Model) { m.state.scrollbackLines = lines }
}

// WithReleaseKey names the one key the widget gives back while focused
// (Bubble Tea key string, e.g. "ctrl+q", the default). Everything else is
// the terminal's.
func WithReleaseKey(key string) Option {
	return func(m *Model) { m.releaseKey = key }
}

// WithDefaultBackground paints every cell whose background the hosted
// program left at the terminal default with c instead, and pads short rows
// to the full box width in the same color. The box becomes an opaque
// rectangle — its own ground rather than a window onto the embedding
// terminal's — for embedders that want the widget to read as a distinct
// surface. Off by default: default-background cells pass through unpainted.
// See paint.go for why this must happen at render time.
func WithDefaultBackground(c color.Color) Option {
	return func(m *Model) { m.state.paintBg = bgSequence(c) }
}

// Model is the terminal box. It is a value like any bubble; the live
// state lives behind a shared pointer so copies stay coherent.
type Model struct {
	id         int64
	state      *termState
	releaseKey string
	focused    bool
}

// termState is everything the Model's copies share: the emulator, the
// transport, and the frame pump.
type termState struct {
	transport Transport

	mu   sync.Mutex
	emu  *vt.Emulator
	cols int
	rows int
	// termCols and termRows are the emulator's dimensions. Usually the
	// box's; a transport whose real terminal cannot follow the box (a
	// tmux pane held wide by an attached client) sets them apart via
	// SetTerminalSize, and the view clips.
	termCols int
	termRows int
	scroll   int
	// Wheel events arrive every few milliseconds on high-resolution
	// trackpads. Hold their combined movement until one scheduled update so
	// intermediate Bubble Tea views remain unchanged.
	scrollDelta   int
	scrollPending bool
	closed        bool

	scrollbackLines int
	// paintBg, when non-empty, is the SGR that paints default-background
	// cells at render time (see WithDefaultBackground and paint.go).
	paintBg    string
	frames     chan struct{}
	lastNotify time.Time
	pending    *time.Timer
}

// New builds a terminal box of cols x rows over a transport, replays the
// transport's seed, and starts consuming its stream. Call Close when the
// box is done; call Init (or hand its command to your program) to start
// receiving FrameMsg wake-ups.
func New(transport Transport, cols, rows int, opts ...Option) Model {
	m := Model{
		id:         lastID.Add(1),
		releaseKey: "ctrl+q",
		state: &termState{
			transport:       transport,
			cols:            max(2, cols),
			rows:            max(2, rows),
			scrollbackLines: DefaultScrollback,
			frames:          make(chan struct{}, 1),
		},
	}
	for _, opt := range opts {
		opt(&m)
	}
	s := m.state
	s.termCols, s.termRows = s.cols, s.rows
	emu := vt.NewEmulator(s.cols, s.rows)
	emu.Scrollback().SetMaxLines(s.scrollbackLines)
	s.emu = emu
	// The emulator synthesizes answers to queries that ride the output
	// stream, into an internal pipe whose writes block until read. The
	// transport's other end is the authority that really answers; here the
	// pipe is drained so a query can never wedge the render lock.
	go func() {
		_, _ = io.Copy(io.Discard, emu)
	}()
	s.mu.Lock()
	s.emu.Write(transport.Seed())
	s.mu.Unlock()
	go s.pump()
	return m
}

func (s *termState) pump() {
	for chunk := range s.transport.Output() {
		s.mu.Lock()
		s.emu.Write(chunk)
		if s.emu.IsAltScreen() {
			s.scroll = 0
			s.scrollDelta = 0
		}
		s.mu.Unlock()
		s.notify()
	}
	s.notify()
}

// notify wakes the UI at most once per framePeriod, with a trailing edge
// so the final burst of a quiet stream still lands.
func (s *termState) notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if now.Sub(s.lastNotify) >= framePeriod {
		s.lastNotify = now
		select {
		case s.frames <- struct{}{}:
		default:
		}
		return
	}
	if s.pending == nil {
		wait := framePeriod - now.Sub(s.lastNotify)
		s.pending = time.AfterFunc(wait, func() {
			s.mu.Lock()
			s.pending = nil
			s.lastNotify = time.Now()
			s.mu.Unlock()
			select {
			case s.frames <- struct{}{}:
			default:
			}
		})
	}
}

// Init starts the frame pump's UI side.
func (m Model) Init() tea.Cmd {
	return m.wait()
}

func (m Model) wait() tea.Cmd {
	s := m.state
	id := m.id
	return func() tea.Msg {
		<-s.frames
		return FrameMsg{ID: id}
	}
}

// Update handles frame wake-ups always, and the keyboard while focused:
// every key becomes terminal input except the release key, which blurs.
// Mouse wheel scrolls history. Route all messages here, as with any
// bubble.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case FrameMsg:
		if msg.ID != m.id {
			return m, nil
		}
		return m, m.wait()
	case scrollMsg:
		if msg.ID != m.id {
			return m, nil
		}
		m.flushScroll()
		return m, nil
	case tea.KeyPressMsg:
		if !m.focused {
			return m, nil
		}
		if msg.String() == m.releaseKey {
			m.focused = false
			return m, nil
		}
		data := KeyToBytes(msg)
		if len(data) == 0 {
			return m, nil
		}
		m.ScrollToBottom()
		return m, m.writeCmd(data)
	case tea.MouseWheelMsg:
		if !m.focused {
			return m, nil
		}
		mouse := msg.Mouse()
		switch mouse.Button {
		case tea.MouseWheelUp:
			return m, m.queueScroll(3)
		case tea.MouseWheelDown:
			return m, m.queueScroll(-3)
		}
		return m, nil
	}
	return m, nil
}

func (m Model) writeCmd(data []byte) tea.Cmd {
	s := m.state
	return func() tea.Msg {
		_ = s.transport.Write(data)
		return nil
	}
}

// Write sends bytes to the terminal from code — pasting, scripted input —
// independent of focus.
func (m Model) Write(data []byte) error {
	return m.state.transport.Write(data)
}

// Focus routes the keyboard to the terminal. Blur (or the release key)
// hands it back.
func (m *Model) Focus()            { m.focused = true }
func (m *Model) Blur()             { m.focused = false }
func (m Model) Focused() bool      { return m.focused }
func (m Model) ID() int64          { return m.id }
func (m Model) ReleaseKey() string { return m.releaseKey }

// SetSize moves the box, the emulator, and (asynchronously) the
// underlying terminal.
func (m Model) SetSize(cols, rows int) (Model, tea.Cmd) {
	cols, rows = max(2, cols), max(2, rows)
	s := m.state
	s.mu.Lock()
	if cols != s.cols || rows != s.rows {
		s.emu.Resize(cols, rows)
		s.cols, s.rows = cols, rows
		s.termCols, s.termRows = cols, rows
	}
	s.mu.Unlock()
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.transport.Resize(ctx, cols, rows)
		return nil
	}
}

// SetTerminalSize moves the emulator apart from the box: for transports
// whose real terminal cannot follow the box size, the widget mirrors the
// terminal and the view clips (bottom rows, left columns), exactly as
// tmux shows a large window to a small client. SetSize re-unifies them.
func (m Model) SetTerminalSize(cols, rows int) {
	cols, rows = max(2, cols), max(2, rows)
	s := m.state
	s.mu.Lock()
	if cols != s.termCols || rows != s.termRows {
		s.emu.Resize(cols, rows)
		s.termCols, s.termRows = cols, rows
	}
	s.mu.Unlock()
}

// TerminalSize reports the emulator's dimensions (see SetTerminalSize).
func (m Model) TerminalSize() (cols, rows int) {
	s := m.state
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.termCols, s.termRows
}

// Size reports the box dimensions.
func (m Model) Size() (cols, rows int) {
	s := m.state
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cols, s.rows
}

// QueueScroll coalesces a scroll delta exactly as the widget's own wheel
// handling does: movement accumulates and lands in one scheduled update,
// so a high-resolution trackpad cannot change the view faster than a
// terminal can draw it. For embedders that route wheel events themselves
// (positionally, say) instead of focusing the widget. Run the returned
// command like any other; it may be nil.
func (m Model) QueueScroll(delta int) tea.Cmd {
	return m.queueScroll(delta)
}

// ScrollBy moves the view within main-screen history; positive is up into
// the past. The alternate screen has no past to move through.
func (m Model) ScrollBy(delta int) {
	s := m.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.emu.IsAltScreen() {
		return
	}
	s.scroll = clamp(s.scroll+delta, 0, s.emu.ScrollbackLen())
}

func (m Model) queueScroll(delta int) tea.Cmd {
	s := m.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.emu.IsAltScreen() {
		return nil
	}
	s.scrollDelta += delta
	if s.scrollPending {
		return nil
	}
	s.scrollPending = true
	id := m.id
	return tea.Tick(scrollBurstPeriod, func(time.Time) tea.Msg {
		return scrollMsg{ID: id}
	})
}

func (m Model) flushScroll() {
	s := m.state
	s.mu.Lock()
	defer s.mu.Unlock()
	delta := s.scrollDelta
	s.scrollDelta = 0
	s.scrollPending = false
	if s.closed || delta == 0 || s.emu.IsAltScreen() {
		return
	}
	s.scroll = clamp(s.scroll+delta, 0, s.emu.ScrollbackLen())
}

// ScrollToBottom returns to the live tail.
func (m Model) ScrollToBottom() {
	s := m.state
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scroll = 0
	s.scrollDelta = 0
}

// Scrolled reports how many lines above the live tail the view sits.
func (m Model) Scrolled() int {
	s := m.state
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scroll
}

// AltScreen reports whether the terminal is on its alternate screen.
func (m Model) AltScreen() bool {
	s := m.state
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emu.IsAltScreen()
}

// View renders exactly rows lines of exactly cols width: scrolled history
// stitched above live rows when scrolled back, clipped tmux-style (bottom
// rows, left columns) if the emulator ever runs larger than the box.
func (m Model) View() string {
	s := m.state
	s.mu.Lock()
	defer s.mu.Unlock()
	var grid string
	if s.scroll == 0 || s.emu.IsAltScreen() {
		lines := strings.Split(s.emu.Render(), "\n")
		if len(lines) > s.rows {
			// A terminal taller than the box is clipped bottom-anchored —
			// terminal apps keep their live region (input line, status
			// bar) at the bottom, and those rows may sit BELOW the cursor,
			// so a window that merely ends at the cursor hides them. The
			// window slides up only when the cursor would fall above it: a
			// freshly-grown pane has its action (and cursor) at the top,
			// and blindly keeping bottom rows would show blank space.
			bottom := len(lines)
			if cursorRow := s.emu.CursorPosition().Y; cursorRow < bottom-s.rows {
				bottom = max(cursorRow+1, s.rows)
			}
			lines = lines[bottom-s.rows : bottom]
		}
		grid = strings.Join(lines, "\n")
	} else {
		back := s.emu.Scrollback()
		backRows := back.Len()
		totalRows := backRows + s.termRows
		top := max(0, totalRows-s.rows-s.scroll)
		bottom := min(totalRows, top+s.rows)
		lines := make([]string, 0, bottom-top)

		// Render only the visible history window. Rendering the entire
		// scrollback on every wheel event makes deep history progressively
		// slower and can overwhelm the Bubble Tea renderer.
		for index := top; index < min(bottom, backRows); index++ {
			lines = append(lines, back.Line(index).Render())
		}
		if bottom > backRows {
			live := strings.Split(s.emu.Render(), "\n")
			liveTop := max(0, top-backRows)
			liveBottom := min(len(live), bottom-backRows)
			for index := liveTop; index < liveBottom; index++ {
				lines = append(lines, live[index])
			}
		}
		grid = strings.Join(lines, "\n")
	}
	return fit(grid, s.cols, s.rows, s.paintBg)
}

// Cursor reports the terminal cursor relative to the box's top-left cell.
// A nested component cannot know where it sits on screen, so the
// embedding app adds its own origin. ok is false while scrolled back or
// when the cursor is outside the box.
func (m Model) Cursor() (x, y int, ok bool) {
	s := m.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scroll != 0 {
		return 0, 0, false
	}
	cursor := s.emu.CursorPosition()
	// The view clips a too-tall terminal bottom-anchored, sliding up only
	// to keep the cursor visible (see View); the cursor shifts with that
	// same window.
	clipTop := 0
	if s.termRows > s.rows {
		bottom := s.termRows
		if cursor.Y < bottom-s.rows {
			bottom = max(cursor.Y+1, s.rows)
		}
		clipTop = bottom - s.rows
	}
	row := cursor.Y - clipTop
	if cursor.X < 0 || cursor.X >= s.cols || row < 0 || row >= s.rows {
		return 0, 0, false
	}
	return cursor.X, row, true
}

// Close detaches from the transport. The widget stops updating; whether
// the session lives on is the transport's contract.
func (m Model) Close() {
	s := m.state
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	if s.pending != nil {
		s.pending.Stop()
		s.pending = nil
	}
	s.scrollDelta = 0
	s.mu.Unlock()
	s.transport.Close()
	if pw, ok := s.emu.InputPipe().(io.Closer); ok {
		pw.Close()
	}
}

// fit clamps a rendered grid to exactly cols x rows: too-tall keeps the
// bottom rows, too-wide keeps the left columns, too-small pads. A non-empty
// bg additionally paints default-background cells and pads every row to the
// full width in that color, so the box renders as an opaque rectangle.
func fit(grid string, cols, rows int, bg string) string {
	lines := strings.Split(grid, "\n")
	if len(lines) > rows {
		lines = lines[len(lines)-rows:]
	}
	if bg != "" {
		// Padding rows join the loop so they come out painted, not empty.
		for len(lines) < rows {
			lines = append(lines, "")
		}
	}
	for index, line := range lines {
		width := ansi.StringWidth(line)
		if width > cols {
			line = ansi.Truncate(line, cols, "")
			width = cols
		}
		if bg != "" {
			line = paintDefaultBg(line, bg)
			if width < cols {
				// Re-assert bg before padding: the line may end inside an
				// explicitly-styled run whose background would otherwise
				// bleed into the pad.
				line += bg + strings.Repeat(" ", cols-width)
			}
		}
		lines[index] = line + ansi.ResetStyle
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func clamp(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}
