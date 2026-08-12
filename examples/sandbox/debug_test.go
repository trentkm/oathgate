package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDebugLogPersistsEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sandbox.log")
	debug, err := openDebugLog(path)
	if err != nil {
		t.Fatalf("openDebugLog: %v", err)
	}
	debug.Printf("scroll offset=120")
	if err := debug.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	logged := string(content)
	for _, want := range []string{"session_start", "scroll offset=120", "session_end"} {
		if !strings.Contains(logged, want) {
			t.Errorf("debug log does not contain %q:\n%s", want, logged)
		}
	}
}

func TestWatchdogPersistsStallStack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sandbox.log")
	debug, err := openDebugLog(path)
	if err != nil {
		t.Fatalf("openDebugLog: %v", err)
	}
	stop := debug.StartWatchdog(25 * time.Millisecond)
	debug.Progress("test_stall")

	deadline := time.Now().Add(time.Second)
	for {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("ReadFile: %v", readErr)
		}
		logged := string(content)
		if strings.Contains(logged, "watchdog_stall") {
			if !strings.Contains(logged, `stage="test_stall"`) {
				t.Fatalf("watchdog did not record the stalled stage:\n%s", logged)
			}
			if !strings.Contains(logged, "goroutine ") {
				t.Fatalf("watchdog did not record goroutine stacks:\n%s", logged)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("watchdog did not record a stall:\n%s", logged)
		}
		time.Sleep(10 * time.Millisecond)
	}

	stop()
	if err := debug.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
