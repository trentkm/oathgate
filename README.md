# oathgate

An embeddable terminal for Bubble Tea — a box you can put anywhere that a
real terminal lives inside.

Oathgate is a [Bubble Tea v2](https://github.com/charmbracelet/bubbletea)
component that renders a live terminal session as part of your TUI: full
VT emulation (pure Go, via `charmbracelet/x/vt`), keyboard forwarding when
focused, scrollback, clipping, and real-cursor placement. It is the
missing xterm.js of the Bubble Tea ecosystem: the *view* component,
distinct from the engine underneath.

```go
transport, _ := ptyadapter.Spawn(ctx, ptyadapter.Spec{Command: "bash"}, 80, 24)
term := oathgate.New(transport, 80, 24)
// term.Update(msg), term.View(), term.Cursor() — a bubble like any other.
```

## Design rules

- **Transports, not couplings.** The widget consumes a five-method
  `Transport` (seed + stream + write + resize + close) and never knows
  where the terminal lives. Adapters included:
  - `ptyadapter` — spawn a process on a PTY in your own process, no
    daemon, batteries included (an embedded
    [windrunner](https://github.com/trentkm/windrunner) engine, so
    programs that query their terminal get real answers).
  - `wradapter` — attach to a session in a windrunner daemon: the
    terminal outlives your app, and several apps can watch one session.
- **Cursor positions are box-relative.** A nested component cannot know
  where it sits on screen, so `Cursor()` answers relative to the widget's
  top-left and the embedding app adds its origin. Learned the hard way;
  encoded in the API.
- **The widget owns its box.** `View()` is always exactly the size you
  set: a larger underlying terminal is clipped the way tmux clips (bottom
  rows, left columns), a smaller one is padded.

## What it is not

Not a multiplexer, not a session manager, not an app. One widget, one
terminal. Fleets, rosters, and persistence live in the layers built on
top ([windrunner](https://github.com/trentkm/windrunner) for the engine,
[stormlight](https://github.com/trentkm/stormlight) for a product).

## Status

Early 0.x; the API is settling against its first consumer (stormlight's
Spanreed pane). Unix only for now.

## Debug sandbox

The instrumented sandbox runs a real shell while exposing Oathgate's box,
emulator, cursor, frame, stream, input, and resize state:

```sh
go run ./examples/sandbox
go run ./examples/sandbox -- /bin/sh
go run ./examples/sandbox -scroll-stress 1200 -- \
  /bin/sh -c 'seq 1 10000; exec /bin/sh'
```

The shell starts focused. Press `ctrl+q` to release it, then use the arrow
keys to resize the box, shifted arrow keys to resize the underlying terminal,
`=` to synchronize them, or `1` through `4` to load clipping presets. Press
`enter` to focus the shell again and `q` to quit from sandbox mode.

Each run appends diagnostics to `oathgate-sandbox.log`, including aggregated
scroll activity, frame and I/O counters, heap usage, slow renders, transport
errors, and full Go crash output. Terminal output and typed content are not
logged. Use `-log path/to/file` to choose another file or `-log=` to disable
logging. `-scroll-stress` injects half of its wheel events upward and half
downward at trackpad-like frequency.

The log also records the parent process chain. If it reports
`terminal_proxy_warning` for `kiro-cli-term`, start a new Ghostty instance
without that PTY proxy before reproducing:

```sh
open -na Ghostty.app --args --window-save-state=never \
  --command="/usr/bin/env Q_TERM_DISABLED=1 $SHELL"
```

The variable must be present when the terminal's shell starts. Starting a
nested shell inside an already wrapped terminal does not bypass its existing
proxy.

## License

MIT
