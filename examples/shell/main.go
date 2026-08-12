// Command shell is oathgate's smallest useful embedding: your shell in a
// box inside a Bubble Tea app. The box owns everything inside its border;
// ctrl+q leaves.
package main

import (
	"context"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/trentkm/oathgate"
	"github.com/trentkm/oathgate/ptyadapter"
)

const chrome = 2 // border cells on each axis

type app struct {
	term  oathgate.Model
	ready bool
}

func (a app) Init() tea.Cmd {
	return a.term.Init()
}

func (a app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.ready = true
		var cmd tea.Cmd
		a.term, cmd = a.term.SetSize(msg.Width-chrome, msg.Height-chrome)
		return a, cmd
	case tea.KeyPressMsg:
		if msg.String() == a.term.ReleaseKey() {
			a.term.Close()
			return a, tea.Quit
		}
	}
	var cmd tea.Cmd
	a.term, cmd = a.term.Update(msg)
	return a, cmd
}

func (a app) View() tea.View {
	view := tea.NewView("")
	view.AltScreen = true
	if !a.ready {
		view.SetContent("loading...")
		return view
	}
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("63"))
	view.SetContent(border.Render(a.term.View()))
	// The widget answers box-relative; the border shifts the origin by one.
	if x, y, ok := a.term.Cursor(); ok {
		view.Cursor = tea.NewCursor(x+1, y+1)
	}
	return view
}

func main() {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	transport, err := ptyadapter.Spawn(context.Background(), ptyadapter.Spec{Command: shell}, 80, 24)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shell:", err)
		os.Exit(1)
	}
	term := oathgate.New(transport, 80, 24)
	term.Focus()
	if _, err := tea.NewProgram(app{term: term}).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "shell:", err)
		os.Exit(1)
	}
}
