package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

var version = "dev"

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
