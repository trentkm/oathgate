// Package ptyadapter is spanreed's batteries-included transport: spawn a
// process on a PTY inside your own process, no daemon required. Closing
// the transport ends the process — this is a terminal you own, not a
// session you visit.
//
// It embeds a windrunner engine rather than reimplementing one, so
// programs that query their terminal (cursor position, device attributes)
// get real answers from an authoritative emulator.
package ptyadapter

import (
	"context"
	"fmt"

	"github.com/trentkm/windrunner"

	"github.com/trentkm/spanreed"
)

// Spec describes the process to spawn. Zero values inherit sensible
// defaults (see windrunner.SpawnSpec).
type Spec struct {
	Command    string
	Args       []string
	Dir        string
	Env        []string
	Scrollback int
}

// Spawn starts the process on a fresh PTY and returns the transport for
// it. The returned transport's Close kills the process.
func Spawn(ctx context.Context, spec Spec, cols, rows int) (spanreed.Transport, error) {
	engine := windrunner.NewEngine()
	session, err := engine.Spawn(windrunner.SpawnSpec{
		Command:    spec.Command,
		Args:       spec.Args,
		Dir:        spec.Dir,
		Env:        spec.Env,
		Cols:       cols,
		Rows:       rows,
		Scrollback: spec.Scrollback,
	})
	if err != nil {
		engine.Close()
		return nil, fmt.Errorf("ptyadapter: %w", err)
	}
	snapshot, subscription := session.Attach(256)
	return &transport{
		engine:       engine,
		session:      session,
		seed:         snapshot.ANSI,
		subscription: subscription,
	}, nil
}

type transport struct {
	engine       *windrunner.Engine
	session      *windrunner.Session
	seed         []byte
	subscription *windrunner.Subscription
}

func (t *transport) Seed() []byte          { return t.seed }
func (t *transport) Output() <-chan []byte { return t.subscription.Output() }
func (t *transport) Write(p []byte) error  { _, err := t.session.Write(p); return err }
func (t *transport) Close()                { t.engine.Close() }

func (t *transport) Resize(ctx context.Context, cols, rows int) error {
	return t.session.Resize(cols, rows)
}

// Session exposes the underlying windrunner session for callers that need
// exit codes or metadata; most embedders never touch it.
func (t *transport) Session() *windrunner.Session { return t.session }
