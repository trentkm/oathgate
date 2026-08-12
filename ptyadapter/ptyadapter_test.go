package ptyadapter

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/trentkm/oathgate"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func plain(view string) string { return ansiPattern.ReplaceAllString(view, "") }

func waitForView(t *testing.T, m oathgate.Model, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(plain(m.View()), want) {
		if time.Now().After(deadline) {
			t.Fatalf("view never showed %q:\n%s", want, plain(m.View()))
		}
		time.Sleep(15 * time.Millisecond)
	}
}

// TestWidgetOverOwnedPTY is the whole product in one test: spawn a shell,
// render it in the widget, type into it, see the echo.
func TestWidgetOverOwnedPTY(t *testing.T) {
	transport, err := Spawn(context.Background(), Spec{
		Command: "/bin/sh",
		Args:    []string{"-c", `printf 'boxed shell ready\n'; read line; printf 'echo:%s\n' "$line"; sleep 60`},
	}, 60, 12)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	m := oathgate.New(transport, 60, 12)
	defer m.Close()

	waitForView(t, m, "boxed shell ready")
	if err := m.Write([]byte("through the gate\r")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitForView(t, m, "echo:through the gate")
}
