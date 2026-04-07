package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

var version = "dev"

type model struct {
	apiAddr string
	status  string
}

func newModel(apiAddr string) model {
	return model{
		apiAddr: apiAddr,
		status:  "disconnected",
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m model) View() string {
	return fmt.Sprintf(
		"clambhook %s\n\nStatus: %s\nAPI: %s\n\nPress q to quit.\n",
		version, m.status, m.apiAddr,
	)
}

func main() {
	apiAddr := "127.0.0.1:9090"
	if len(os.Args) > 1 {
		apiAddr = os.Args[1]
	}

	p := tea.NewProgram(newModel(apiAddr))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
