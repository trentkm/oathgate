package spanreed

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// fakeTransport drives the widget from a test: bytes in, writes recorded.
type fakeTransport struct {
	seed   []byte
	output chan []byte

	mu      sync.Mutex
	written [][]byte
	resized [2]int
	closed  bool
}

func newFakeTransport(seed string) *fakeTransport {
	return &fakeTransport{seed: []byte(seed), output: make(chan []byte, 16)}
}

func (f *fakeTransport) Seed() []byte          { return f.seed }
func (f *fakeTransport) Output() <-chan []byte { return f.output }

func (f *fakeTransport) Write(p []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	chunk := make([]byte, len(p))
	copy(chunk, p)
	f.written = append(f.written, chunk)
	return nil
}

func (f *fakeTransport) Resize(_ context.Context, cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resized = [2]int{cols, rows}
	return nil
}

func (f *fakeTransport) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.output)
	}
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func plain(view string) string { return ansiPattern.ReplaceAllString(view, "") }

func waitForView(t *testing.T, m Model, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(plain(m.View()), want) {
		if time.Now().After(deadline) {
			t.Fatalf("view never showed %q:\n%s", want, plain(m.View()))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSeedAndStreamRender(t *testing.T) {
	transport := newFakeTransport("seeded content\r\n")
	m := New(transport, 40, 6)
	defer m.Close()
	waitForView(t, m, "seeded content")

	transport.output <- []byte("streamed line\r\n")
	waitForView(t, m, "streamed line")
}

func TestViewIsAlwaysExactlyTheBox(t *testing.T) {
	transport := newFakeTransport("wide line that runs well past twenty columns\r\n")
	m := New(transport, 20, 4)
	defer m.Close()
	waitForView(t, m, "wide line")

	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) != 4 {
		t.Fatalf("view is %d rows, want exactly 4", len(lines))
	}
	for index, line := range lines {
		if width := len(plain(line)); width > 20 {
			t.Fatalf("row %d is %d cells wide, want <= 20: %q", index, width, plain(line))
		}
	}
}

func TestFocusRoutesKeysAndReleaseKeyBlurs(t *testing.T) {
	transport := newFakeTransport("")
	m := New(transport, 40, 6)
	defer m.Close()

	// Unfocused: keys pass by.
	m, _ = m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	transport.mu.Lock()
	if len(transport.written) != 0 {
		t.Fatal("unfocused widget forwarded a key")
	}
	transport.mu.Unlock()

	m.Focus()
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = next
	if cmd == nil {
		t.Fatal("focused key produced no write command")
	}
	cmd()
	transport.mu.Lock()
	if len(transport.written) != 1 || string(transport.written[0]) != "x" {
		t.Fatalf("forwarded %q, want [x]", transport.written)
	}
	transport.mu.Unlock()

	m, _ = m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if m.Focused() {
		t.Fatal("release key did not blur")
	}
}

func TestCursorIsBoxRelative(t *testing.T) {
	transport := newFakeTransport("\x1b[3;5Hmark")
	m := New(transport, 40, 6)
	defer m.Close()
	waitForView(t, m, "mark")

	x, y, ok := m.Cursor()
	if !ok {
		t.Fatal("cursor unavailable")
	}
	// CUP 3;5 is 0-based (4,2); "mark" advances x by 4.
	if x != 8 || y != 2 {
		t.Fatalf("cursor at (%d,%d), want (8,2)", x, y)
	}
}

func TestScrollbackStitchesAndCursorHidesWhileScrolled(t *testing.T) {
	transport := newFakeTransport("")
	m := New(transport, 40, 4)
	defer m.Close()
	var lines []string
	for _, line := range []string{"one", "two", "three", "four", "five", "six"} {
		lines = append(lines, line+"\r\n")
	}
	transport.output <- []byte(strings.Join(lines, ""))
	waitForView(t, m, "six")

	m.ScrollBy(1 << 30)
	if m.Scrolled() == 0 {
		t.Fatal("scroll did not move")
	}
	if !strings.Contains(plain(m.View()), "one") {
		t.Fatalf("scrolled view missing history:\n%s", plain(m.View()))
	}
	if _, _, ok := m.Cursor(); ok {
		t.Fatal("cursor should hide while scrolled back")
	}
	m.ScrollToBottom()
	if m.Scrolled() != 0 {
		t.Fatal("ScrollToBottom did not return")
	}
}

func TestMouseWheelBurstIsAppliedOnce(t *testing.T) {
	var seed strings.Builder
	for index := 0; index < 500; index++ {
		seed.WriteString("wheel history ")
		seed.WriteString(strconv.Itoa(index))
		seed.WriteString("\r\n")
	}
	transport := newFakeTransport(seed.String())
	m := New(transport, 40, 8, WithScrollback(500))
	defer m.Close()
	m.Focus()

	liveView := m.View()
	var scheduled tea.Cmd
	for index := 0; index < 100; index++ {
		var cmd tea.Cmd
		m, cmd = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
		if index == 0 {
			if cmd == nil {
				t.Fatal("first wheel event did not schedule a burst")
			}
			scheduled = cmd
		} else if cmd != nil {
			t.Fatalf("wheel event %d scheduled a second burst", index+1)
		}
		if m.Scrolled() != 0 {
			t.Fatalf("wheel event %d changed scroll before the burst fired", index+1)
		}
		if got := m.View(); got != liveView {
			t.Fatalf("wheel event %d changed the view before the burst fired", index+1)
		}
	}
	if scheduled == nil {
		t.Fatal("wheel burst was not scheduled")
	}

	burstMsg := scheduled()
	if _, ok := burstMsg.(scrollMsg); !ok {
		t.Fatalf("wheel burst emitted %T, want scrollMsg", burstMsg)
	}
	m, _ = m.Update(burstMsg)
	if got := m.Scrolled(); got != 300 {
		t.Fatalf("combined wheel burst scrolled %d lines, want 300", got)
	}

	var cmd tea.Cmd
	m, cmd = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if cmd == nil {
		t.Fatal("next wheel burst was not scheduled")
	}
	m.ScrollToBottom()
	m, _ = m.Update(cmd())
	if got := m.Scrolled(); got != 0 {
		t.Fatalf("ScrollToBottom left a delayed scroll of %d lines", got)
	}
}

func TestAlternateScreenDropsPendingMouseWheel(t *testing.T) {
	transport := newFakeTransport("one\r\ntwo\r\nthree\r\nfour\r\n")
	m := New(transport, 20, 2)
	defer m.Close()
	m.Focus()

	var cmd tea.Cmd
	m, cmd = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if cmd == nil {
		t.Fatal("wheel event did not schedule a burst")
	}
	transport.output <- []byte("\x1b[?1049h")
	deadline := time.Now().Add(time.Second)
	for !m.AltScreen() {
		if time.Now().After(deadline) {
			t.Fatal("terminal did not enter alternate screen")
		}
		time.Sleep(time.Millisecond)
	}

	m, _ = m.Update(cmd())
	if got := m.Scrolled(); got != 0 {
		t.Fatalf("alternate screen applied a delayed scroll of %d lines", got)
	}
}

func TestHorizontalMouseWheelDoesNothing(t *testing.T) {
	transport := newFakeTransport("one\r\ntwo\r\nthree\r\nfour\r\n")
	m := New(transport, 20, 2)
	defer m.Close()
	m.Focus()

	view := m.View()
	for _, button := range []tea.MouseButton{tea.MouseWheelLeft, tea.MouseWheelRight} {
		var cmd tea.Cmd
		m, cmd = m.Update(tea.MouseWheelMsg{Button: button})
		if cmd != nil {
			t.Fatalf("%s wheel event scheduled a scroll", button)
		}
		if got := m.Scrolled(); got != 0 {
			t.Fatalf("%s wheel event scrolled %d lines", button, got)
		}
		if got := m.View(); got != view {
			t.Fatalf("%s wheel event changed the view", button)
		}
	}
}

func TestFrameMsgRoutingByID(t *testing.T) {
	transportA := newFakeTransport("")
	transportB := newFakeTransport("")
	a := New(transportA, 20, 4)
	b := New(transportB, 20, 4)
	defer a.Close()
	defer b.Close()

	_, cmd := a.Update(FrameMsg{ID: a.ID()})
	if cmd == nil {
		t.Fatal("own FrameMsg did not re-arm the wait")
	}
	_, cmd = b.Update(FrameMsg{ID: a.ID()})
	if cmd != nil {
		t.Fatal("foreign FrameMsg re-armed the wrong widget")
	}
}

func TestSetSizePropagates(t *testing.T) {
	transport := newFakeTransport("")
	m := New(transport, 40, 10)
	defer m.Close()
	m, cmd := m.SetSize(60, 20)
	if cmd != nil {
		cmd()
	}
	cols, rows := m.Size()
	if cols != 60 || rows != 20 {
		t.Fatalf("box is %dx%d, want 60x20", cols, rows)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.resized != [2]int{60, 20} {
		t.Fatalf("transport resized to %v, want [60 20]", transport.resized)
	}
}

func TestTallTerminalClipFollowsTheCursor(t *testing.T) {
	transport := newFakeTransport("")
	m := New(transport, 40, 10)
	defer m.Close()
	m.SetTerminalSize(80, 30)
	// Action at the TOP of the tall terminal: a grown pane's typical
	// state. The box must show it, not the blank bottom.
	transport.output <- []byte("\x1b[2;1Htop action here")
	waitForView(t, m, "top action here")
	x, y, ok := m.Cursor()
	if !ok {
		t.Fatal("cursor unavailable")
	}
	if y != 1 || x != 15 {
		t.Fatalf("cursor at (%d,%d), want (15,1)", x, y)
	}
	// Action at the BOTTOM: the window slides down with the cursor.
	transport.output <- []byte("\x1b[30;1Hbottom action")
	waitForView(t, m, "bottom action")
	lines := strings.Split(plain(m.View()), "\n")
	if !strings.Contains(lines[9], "bottom action") {
		t.Fatalf("bottom action not on box bottom row:\n%s", plain(m.View()))
	}
}

func TestTallTerminalClipKeepsRowsBelowTheCursor(t *testing.T) {
	transport := newFakeTransport("")
	m := New(transport, 40, 10)
	defer m.Close()
	m.SetTerminalSize(80, 30)
	// The claude-code shape: a status bar on the LAST terminal row, the
	// cursor parked on an input line two rows above it. A clip window
	// that merely ends at the cursor hides the status bar forever.
	transport.output <- []byte("\x1b[30;1Hstatus bar\x1b[28;1Hinput line \x1b[28;12H")
	waitForView(t, m, "status bar")
	lines := strings.Split(plain(m.View()), "\n")
	if !strings.Contains(lines[9], "status bar") {
		t.Fatalf("status bar not on box bottom row:\n%s", plain(m.View()))
	}
	if !strings.Contains(lines[7], "input line") {
		t.Fatalf("input line not two rows above:\n%s", plain(m.View()))
	}
	x, y, ok := m.Cursor()
	if !ok || y != 7 || x != 11 {
		t.Fatalf("cursor at (%d,%d) ok=%v, want (11,7)", x, y, ok)
	}
}

func TestDeepScrollMatchesFullHistoryReference(t *testing.T) {
	var seed strings.Builder
	for index := 0; index < 300; index++ {
		seed.WriteString("history line ")
		seed.WriteString(strconv.Itoa(index))
		seed.WriteString("\r\n")
	}
	transport := newFakeTransport(seed.String())
	m := New(transport, 40, 8, WithScrollback(300))
	defer m.Close()

	for _, offset := range []int{1, 25, 150, 1 << 30} {
		m.ScrollToBottom()
		m.ScrollBy(offset)
		if got, want := m.View(), fullHistoryReferenceView(m); got != want {
			t.Fatalf("view at offset %d differs from full-history reference\ngot:\n%s\nwant:\n%s",
				offset, plain(got), plain(want))
		}
	}
}

func fullHistoryReferenceView(m Model) string {
	s := m.state
	s.mu.Lock()
	defer s.mu.Unlock()

	live := strings.Split(s.emu.Render(), "\n")
	back := s.emu.Scrollback()
	rows := make([]string, 0, back.Len()+len(live))
	for index := 0; index < back.Len(); index++ {
		rows = append(rows, back.Line(index).Render())
	}
	rows = append(rows, live...)
	top := max(0, len(rows)-s.rows-s.scroll)
	return fit(strings.Join(rows[top:min(len(rows), top+s.rows)], "\n"), s.cols, s.rows, s.paintBg)
}

func TestDeepScrollViewAllocationsAreBounded(t *testing.T) {
	var seed strings.Builder
	for index := 0; index < DefaultScrollback+100; index++ {
		seed.WriteString("allocation regression line with styled \x1b[32mcontent\x1b[0m\r\n")
	}
	transport := newFakeTransport(seed.String())
	m := New(transport, 80, 24)
	defer m.Close()
	m.ScrollBy(DefaultScrollback / 2)

	allocations := testing.AllocsPerRun(10, func() {
		_ = m.View()
	})
	if allocations > 1000 {
		t.Fatalf("deep scroll view made %.0f allocations, want at most 1000", allocations)
	}
}

func BenchmarkViewDeepInScrollback(b *testing.B) {
	var seed strings.Builder
	for index := 0; index < DefaultScrollback+100; index++ {
		seed.WriteString("scrollback benchmark line with enough content to render\r\n")
	}
	transport := newFakeTransport(seed.String())
	m := New(transport, 80, 24)
	defer m.Close()
	m.ScrollBy(DefaultScrollback / 2)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = m.View()
	}
}
