package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("clambhook-tui %s\n", version)
		os.Exit(0)
	}

	apiAddr := "127.0.0.1:9090"
	if flag.NArg() > 0 {
		apiAddr = flag.Arg(0)
	}

	p := tea.NewProgram(newModel(apiAddr), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
