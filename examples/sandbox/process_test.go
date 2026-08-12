package main

import "testing"

func TestParseProcessAncestorDetectsKiroTerminalProxy(t *testing.T) {
	ancestor, err := parseProcessAncestor(42, "  21 fish (kiro-cli-term)\n")
	if err != nil {
		t.Fatalf("parseProcessAncestor: %v", err)
	}
	if ancestor.pid != 42 || ancestor.parentPID != 21 {
		t.Fatalf("process is pid=%d parent=%d, want pid=42 parent=21",
			ancestor.pid, ancestor.parentPID)
	}
	if ancestor.name != "kiro-cli-term" || !ancestor.terminalProxy {
		t.Fatalf("process is name=%q proxy=%t, want kiro-cli-term proxy",
			ancestor.name, ancestor.terminalProxy)
	}
}

func TestParseProcessAncestorRedactsArguments(t *testing.T) {
	ancestor, err := parseProcessAncestor(
		84,
		"  42 /opt/homebrew/bin/go run ./examples/sandbox -token secret\n",
	)
	if err != nil {
		t.Fatalf("parseProcessAncestor: %v", err)
	}
	if ancestor.name != "go" {
		t.Fatalf("process name is %q, want go", ancestor.name)
	}
	if got := formatProcessAncestry([]processAncestor{ancestor}); got != "84:go" {
		t.Fatalf("formatted ancestry is %q, want %q", got, "84:go")
	}
}

func TestTerminalProxyAncestor(t *testing.T) {
	want := processAncestor{pid: 2, name: "kiro-cli-term", terminalProxy: true}
	got, ok := terminalProxyAncestor([]processAncestor{
		{pid: 1, name: "sandbox"},
		want,
	})
	if !ok || got != want {
		t.Fatalf("terminalProxyAncestor = (%+v, %t), want (%+v, true)", got, ok, want)
	}
}
