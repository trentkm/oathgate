package main

import (
	"context"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/trentkm/spanreed"
)

type testTransport struct {
	output    chan []byte
	closeOnce sync.Once
}

func newTestTransport() *testTransport {
	return &testTransport{output: make(chan []byte)}
}

func (t *testTransport) Seed() []byte {
	return nil
}

func (t *testTransport) Output() <-chan []byte {
	return t.output
}

func (t *testTransport) Write([]byte) error {
	return nil
}

func (t *testTransport) Resize(context.Context, int, int) error {
	return nil
}

func (t *testTransport) Close() {
	t.closeOnce.Do(func() {
		close(t.output)
	})
}

func TestCalculateLayoutUsesSideInspectorWhenWide(t *testing.T) {
	layout := calculateLayout(120, 40)
	if !layout.sideInspector {
		t.Fatal("wide layout did not place inspector beside terminal")
	}
	if layout.terminalAreaWidth != 88 || layout.terminalAreaHeight != 38 {
		t.Fatalf("terminal area is %dx%d, want 88x38",
			layout.terminalAreaWidth, layout.terminalAreaHeight)
	}
	if layout.maxBoxCols != 86 || layout.maxBoxRows != 36 {
		t.Fatalf("box limit is %dx%d, want 86x36",
			layout.maxBoxCols, layout.maxBoxRows)
	}
}

func TestCalculateLayoutStacksInspectorWhenNarrow(t *testing.T) {
	layout := calculateLayout(70, 30)
	if layout.sideInspector {
		t.Fatal("narrow layout placed inspector beside terminal")
	}
	if layout.inspectorHeight != 6 {
		t.Fatalf("inspector height is %d, want 6", layout.inspectorHeight)
	}
	if layout.maxBoxCols != 68 || layout.maxBoxRows != 20 {
		t.Fatalf("box limit is %dx%d, want 68x20",
			layout.maxBoxCols, layout.maxBoxRows)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{
		0:           "0 B",
		1023:        "1023 B",
		1024:        "1.0 KiB",
		1024 * 1024: "1.0 MiB",
	}
	for input, want := range cases {
		if got := formatBytes(input); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", input, got, want)
		}
	}
}

func TestViewFillsWindowAndOffsetsTerminalCursor(t *testing.T) {
	raw := newTestTransport()
	observed := observeTransport(raw)
	term := spanreed.New(observed, initialCols, initialRows)
	defer term.Close()

	model := newSandbox(term, observed, "/bin/sh")
	next, resize := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = next.(sandbox)
	if resize != nil {
		resize()
	}

	view := model.View()
	lines := strings.Split(view.Content, "\n")
	if len(lines) != 40 {
		t.Fatalf("view is %d rows, want 40", len(lines))
	}
	for index, line := range lines {
		if width := ansi.StringWidth(line); width != 120 {
			t.Fatalf("row %d is %d cells wide, want 120", index, width)
		}
	}
	if view.Cursor == nil {
		t.Fatal("focused terminal has no cursor")
	}
	if view.Cursor.X != 1 || view.Cursor.Y != 2 {
		t.Fatalf("cursor is at (%d,%d), want (1,2)", view.Cursor.X, view.Cursor.Y)
	}
}
