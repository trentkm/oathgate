package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	runtimeDebug "runtime/debug"
	"sync/atomic"
	"time"
)

type debugLog struct {
	path   string
	file   *os.File
	logger *log.Logger

	lastProgress  atomic.Int64
	progressStage atomic.Value
	stallReported atomic.Bool
}

func openDebugLog(path string) (*debugLog, error) {
	if path == "" {
		return &debugLog{}, nil
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve debug log: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return nil, fmt.Errorf("create debug log directory: %w", err)
	}
	file, err := os.OpenFile(absolute, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open debug log: %w", err)
	}
	if err := runtimeDebug.SetCrashOutput(file, runtimeDebug.CrashOptions{}); err != nil {
		file.Close()
		return nil, fmt.Errorf("configure crash log: %w", err)
	}

	debug := &debugLog{
		path:   absolute,
		file:   file,
		logger: log.New(file, "", log.Ldate|log.Ltime|log.Lmicroseconds),
	}
	debug.Progress("startup")
	debug.Printf("session_start pid=%d", os.Getpid())
	return debug, nil
}

func (d *debugLog) Path() string {
	if d == nil {
		return ""
	}
	return d.path
}

func (d *debugLog) Printf(format string, args ...any) {
	if d == nil || d.logger == nil {
		return
	}
	d.logger.Printf(format, args...)
}

func (d *debugLog) Progress(stage string) {
	if d == nil || d.logger == nil {
		return
	}
	d.progressStage.Store(stage)
	d.lastProgress.Store(time.Now().UnixNano())
	d.stallReported.Store(false)
}

func (d *debugLog) StartWatchdog(timeout time.Duration) func() {
	if d == nil || d.logger == nil || timeout <= 0 {
		return func() {}
	}
	d.Progress("watchdog_start")
	stop := make(chan struct{})
	done := make(chan struct{})
	interval := timeout / 4
	interval = max(10*time.Millisecond, min(time.Second, interval))
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				last := time.Unix(0, d.lastProgress.Load())
				stalledFor := time.Since(last)
				if stalledFor < timeout || !d.stallReported.CompareAndSwap(false, true) {
					continue
				}
				stage, _ := d.progressStage.Load().(string)
				stack := make([]byte, 2<<20)
				stackBytes := runtime.Stack(stack, true)
				var memory runtime.MemStats
				runtime.ReadMemStats(&memory)
				d.Printf(
					"watchdog_stall duration=%s stage=%q heap_bytes=%d heap_objects=%d "+
						"goroutines=%d stack_truncated=%t\n%s",
					stalledFor.Round(time.Millisecond),
					stage,
					memory.HeapAlloc,
					memory.HeapObjects,
					runtime.NumGoroutine(),
					stackBytes == len(stack),
					stack[:stackBytes],
				)
				if d.file != nil {
					_ = d.file.Sync()
				}
			case <-stop:
				return
			}
		}
	}()

	var stopped atomic.Bool
	return func() {
		if stopped.CompareAndSwap(false, true) {
			close(stop)
		}
		<-done
	}
}

func (d *debugLog) LogSlowView(start time.Time, scroll, boxCols, boxRows, termCols, termRows int) {
	elapsed := time.Since(start)
	if elapsed < 25*time.Millisecond {
		return
	}
	d.Printf(
		"slow_view duration=%s scroll=%d box=%dx%d terminal=%dx%d",
		elapsed.Round(time.Microsecond),
		scroll,
		boxCols,
		boxRows,
		termCols,
		termRows,
	)
}

func (d *debugLog) Recover(where string) {
	recovered := recover()
	if recovered == nil {
		return
	}
	d.Printf("panic where=%s value=%v\n%s", where, recovered, runtimeDebug.Stack())
	if d != nil && d.file != nil {
		_ = d.file.Sync()
	}
	panic(recovered)
}

func (d *debugLog) Close() error {
	if d == nil || d.file == nil {
		return nil
	}
	d.Printf("session_end")
	_ = d.file.Sync()
	return d.file.Close()
}
