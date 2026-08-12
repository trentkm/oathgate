// Command sandbox is an instrumented spanreed embedding for reproducing
// rendering, clipping, input, scrollback, and resize problems against a real
// PTY.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/trentkm/spanreed"
	"github.com/trentkm/spanreed/ptyadapter"
)

const (
	initialCols = 80
	initialRows = 24
)

func main() {
	scrollback := flag.Int("scrollback", spanreed.DefaultScrollback, "maximum scrollback lines")
	logPath := flag.String("log", "spanreed-sandbox.log", "debug log path; empty disables logging")
	scrollStress := flag.Int(
		"scroll-stress",
		0,
		"inject this many wheel events after startup; zero disables the stress run",
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: %s [flags] [command [args...]]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	command := os.Getenv("SHELL")
	args := []string(nil)
	if flag.NArg() > 0 {
		command = flag.Arg(0)
		args = flag.Args()[1:]
	}
	if command == "" {
		command = "/bin/sh"
	}

	debug, err := openDebugLog(*logPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sandbox:", err)
		os.Exit(1)
	}
	defer debug.Close()
	stopWatchdog := debug.StartWatchdog(4 * time.Second)
	defer stopWatchdog()
	stopSignals := logTerminationSignals(debug)
	defer stopSignals()
	if debug.Path() != "" {
		fmt.Fprintln(os.Stderr, "sandbox: debug log", debug.Path())
	}
	ancestry := processAncestry(os.Getpid())
	if len(ancestry) > 0 {
		debug.Printf("process_ancestry chain=%q", formatProcessAncestry(ancestry))
	}
	startupNotice := ""
	if proxy, ok := terminalProxyAncestor(ancestry); ok {
		startupNotice = "kiro-cli-term detected; retry from an unwrapped terminal"
		debug.Printf(
			"terminal_proxy_warning name=%q pid=%d guidance=%q",
			proxy.name,
			proxy.pid,
			"start a new terminal with Q_TERM_DISABLED=1",
		)
		fmt.Fprintln(os.Stderr, "sandbox: warning:", startupNotice)
	}
	debug.Printf(
		"launch command=%q args=%d scrollback=%d",
		command,
		len(args),
		max(0, *scrollback),
	)

	transport, err := ptyadapter.Spawn(context.Background(), ptyadapter.Spec{
		Command:    command,
		Args:       args,
		Scrollback: max(0, *scrollback),
	}, initialCols, initialRows)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sandbox:", err)
		os.Exit(1)
	}

	observed := observeTransport(transport, debug)
	term := spanreed.New(
		observed,
		initialCols,
		initialRows,
		spanreed.WithScrollback(max(0, *scrollback)),
	)
	defer term.Close()

	model := newSandbox(term, observed, command, debug)
	if startupNotice != "" {
		model.lastAction = startupNotice
	}
	program := tea.NewProgram(
		model,
		tea.WithFPS(30),
	)
	startScrollStress(program, max(0, *scrollStress), debug)
	if _, err := program.Run(); err != nil {
		debug.Printf("program_error error=%q", err)
		fmt.Fprintln(os.Stderr, "sandbox:", err)
		os.Exit(1)
	}
}

func startScrollStress(program *tea.Program, events int, debug *debugLog) {
	if events == 0 {
		return
	}
	go func() {
		const (
			startupDelay = 750 * time.Millisecond
			eventDelay   = 5 * time.Millisecond
		)
		time.Sleep(startupDelay)
		started := time.Now()
		debug.Printf("scroll_stress_start events=%d interval=%s", events, eventDelay)
		for index := range events {
			button := tea.MouseWheelUp
			if index >= (events+1)/2 {
				button = tea.MouseWheelDown
			}
			program.Send(tea.MouseWheelMsg{
				X:      2,
				Y:      headerHeight + 2,
				Button: button,
			})
			time.Sleep(eventDelay)
		}
		debug.Printf(
			"scroll_stress_complete events=%d duration=%s",
			events,
			time.Since(started).Round(time.Millisecond),
		)
	}()
}

func logTerminationSignals(debug *debugLog) func() {
	signals := make(chan os.Signal, 1)
	stop := make(chan struct{})
	done := make(chan struct{})
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGTERM)
	go func() {
		defer close(done)
		select {
		case received := <-signals:
			debug.Printf("process_signal signal=%s", received)
			if debug.file != nil {
				_ = debug.file.Sync()
			}
			signal.Reset(received)
			if systemSignal, ok := received.(syscall.Signal); ok {
				_ = syscall.Kill(os.Getpid(), systemSignal)
			}
		case <-stop:
		}
	}()

	return func() {
		signal.Stop(signals)
		close(stop)
		<-done
	}
}
