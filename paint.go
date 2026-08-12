package spanreed

// Default-background painting. A terminal leaves cells whose background the
// running program never set at the "terminal default", which renders as
// no-background SGR — so the embedding terminal's own ground (wallpaper,
// translucency, theme) shows through the widget. WithDefaultBackground
// replaces that pass-through with a fixed color at render time, making the
// box an opaque rectangle: its own surface rather than a window onto the
// host's.
//
// The rewrite has to happen here, on the rendered stream, because it cannot
// be layered on afterwards: any SGR reset inside the program's output
// (every `\x1b[0m` Claude Code emits) reverts cells to the real default, so
// a wrapper style around the finished string loses the paint at the first
// reset. Only a pass that tracks SGR state through the line can re-assert
// the ground exactly where the default comes back.

import (
	"fmt"
	"image/color"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// bgSequence renders a color as the SGR that paints it as a background.
func bgSequence(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r>>8, g>>8, b>>8)
}

// paintDefaultBg rewrites one rendered line so every run whose background
// is the terminal default gets bg instead: assert bg at the start, pass
// everything through, and re-assert bg immediately after any SGR that
// leaves the background at default again.
func paintDefaultBg(line, bg string) string {
	var out strings.Builder
	out.Grow(len(line) + 4*len(bg))
	out.WriteString(bg)
	var state byte
	for len(line) > 0 {
		seq, _, n, newState := ansi.DecodeSequence(line, state, nil)
		out.WriteString(seq)
		if params, ok := sgrParams(seq); ok && bgDefaultAfter(params) {
			out.WriteString(bg)
		}
		state = newState
		line = line[n:]
	}
	return out.String()
}

// sgrParams extracts the parameter string of an SGR sequence; ok is false
// for anything else (text, other CSI, OSC, ...).
func sgrParams(seq string) (params string, ok bool) {
	if !strings.HasPrefix(seq, "\x1b[") || !strings.HasSuffix(seq, "m") {
		return "", false
	}
	params = seq[2 : len(seq)-1]
	// A private-marker CSI ending in 'm' (DECSET-style) is not SGR.
	if params != "" && (params[0] == '?' || params[0] == '<' || params[0] == '=' || params[0] == '>') {
		return "", false
	}
	return params, true
}

// bgDefaultAfter reports whether applying an SGR parameter list leaves the
// background at the terminal default. Extended-color introducers (38/48/58)
// consume their semicolon-form arguments, so a palette index of 0 or 49 is
// never misread as a reset.
func bgDefaultAfter(params string) bool {
	if params == "" {
		return true // \x1b[m is a full reset
	}
	tokens := strings.Split(params, ";")
	def := false
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		base := token
		if colon := strings.IndexByte(token, ':'); colon >= 0 {
			base = token[:colon]
		}
		switch base {
		case "", "0", "49":
			def = true
		case "40", "41", "42", "43", "44", "45", "46", "47",
			"100", "101", "102", "103", "104", "105", "106", "107":
			def = false
		case "48", "38", "58":
			if base == "48" {
				def = false
			}
			if base == token && i+1 < len(tokens) {
				// Semicolon form: the color arguments ride as separate
				// tokens and must be skipped, not interpreted.
				switch tokens[i+1] {
				case "5":
					i += 2
				case "2":
					i += 4
				}
			}
		}
	}
	return def
}
