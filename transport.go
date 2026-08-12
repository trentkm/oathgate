package spanreed

import "context"

// Transport is where a terminal lives. The widget never knows: an
// in-process PTY (ptyadapter), a session in a windrunner daemon
// (wradapter), an SSH channel — anything that can hand over the state so
// far and the bytes from now on.
type Transport interface {
	// Seed is the terminal's serialized state at attach time — screen,
	// scrollback, cursor, as an ANSI stream. Everything on Output happened
	// after it.
	Seed() []byte
	// Output carries raw terminal output; it closes when the session ends.
	Output() <-chan []byte
	// Write delivers input bytes exactly as typed.
	Write(p []byte) error
	// Resize moves the underlying terminal.
	Resize(ctx context.Context, cols, rows int) error
	// Close detaches. Whether the session survives is the transport's
	// contract: a daemon session lives on, an owned PTY dies.
	Close()
}
