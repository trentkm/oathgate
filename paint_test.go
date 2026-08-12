package spanreed

import (
	"image/color"
	"strings"
	"testing"
)

func TestBgDefaultAfter(t *testing.T) {
	cases := []struct {
		params string
		want   bool
	}{
		{"", true},        // \x1b[m — full reset
		{"0", true},       // full reset
		{"49", true},      // explicit default background
		{"0;33", true},    // reset, then foreground only
		{"31", false},     // foreground only: background untouched
		{"1;4", false},    // attributes only
		{"44", false},     // background set
		{"104", false},    // bright background set
		{"48;5;0", false}, // palette arg 0 is a color, not a reset
		{"48;5;49", false},
		{"48;2;0;0;0", false},
		{"48:2:10:20:30", false}, // colon form
		{"38;5;0", false},        // fg palette arg 0: bg untouched
		{"38;2;49;49;49", false}, // fg RGB args must not read as 49
		{"58;5;0", false},        // underline color args likewise
		{"44;0", true},           // set then reset: default wins
		{"0;44", false},          // reset then set: set wins
		{"38;5;196;49", true},    // fg color, then default bg
		{"?25", false},           // private marker: not SGR params we track
	}
	for _, c := range cases {
		if got := bgDefaultAfter(c.params); got != c.want {
			t.Errorf("bgDefaultAfter(%q) = %v, want %v", c.params, got, c.want)
		}
	}
}

func TestPaintDefaultBg(t *testing.T) {
	bg := "\x1b[48;2;20;21;24m"

	// Plain text: painted once at the start.
	if got, want := paintDefaultBg("hi", bg), bg+"hi"; got != want {
		t.Errorf("plain: got %q, want %q", got, want)
	}

	// A reset mid-line brings the paint back.
	in := "a\x1b[41mb\x1b[0mc"
	want := bg + "a\x1b[41mb\x1b[0m" + bg + "c"
	if got := paintDefaultBg(in, bg); got != want {
		t.Errorf("reset: got %q, want %q", got, want)
	}

	// An explicit background is left alone; a foreground-only change does
	// not re-trigger the paint.
	in = "\x1b[44mx\x1b[31my"
	want = bg + "\x1b[44mx\x1b[31my"
	if got := paintDefaultBg(in, bg); got != want {
		t.Errorf("explicit bg: got %q, want %q", got, want)
	}
}

func TestViewPaintsDefaultBackground(t *testing.T) {
	transport := newFakeTransport("plain \x1b[41mred\x1b[0m tail\r\n")
	m := New(transport, 20, 4, WithDefaultBackground(color.RGBA{R: 20, G: 21, B: 24, A: 255}))
	defer m.Close()
	waitForView(t, m, "plain")

	bg := "\x1b[48;2;20;21;24m"
	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) != 4 {
		t.Fatalf("view has %d rows, want 4", len(lines))
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, bg) {
			t.Errorf("row %d does not open with the paint: %q", i, line)
		}
		if got := len([]rune(plain(line))); got != 20 {
			t.Errorf("row %d pads to %d cells, want 20", i, got)
		}
	}
	// The reset after "red" must not leave a hole: paint re-asserted.
	if !strings.Contains(view, "\x1b[0m"+bg) && !strings.Contains(view, "\x1b[m"+bg) {
		t.Errorf("no repaint after reset in %q", lines[0])
	}
}

func TestViewWithoutPaintLeavesDefaultAlone(t *testing.T) {
	transport := newFakeTransport("bare\r\n")
	m := New(transport, 12, 3)
	defer m.Close()
	waitForView(t, m, "bare")
	if strings.Contains(m.View(), "48;2;") {
		t.Errorf("unpainted widget emits background paint: %q", m.View())
	}
}
