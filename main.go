// Command claude-mgr is a cross-project dashboard for Claude Code sessions.
//
// Phase 1 ships only the headless `--dump` view of the session index; the tmux
// controller UI arrives in later phases.
package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"claude-mgr/internal/index"
	"claude-mgr/internal/server"
	"claude-mgr/internal/tmux"
	"claude-mgr/internal/ui"
)

func main() {
	// Hidden subcommand: run the rail UI in the current terminal (the tmux left
	// pane). Checked before flag parsing so it isn't mistaken for a flag.
	if len(os.Args) > 1 && os.Args[1] == "__controller" {
		if err := runController(); err != nil {
			fmt.Fprintln(os.Stderr, "claude-mgr controller:", err)
			os.Exit(1)
		}
		return
	}

	dump := flag.Bool("dump", false, "print the discovered session index and exit")
	serve := flag.String("serve", "", "also serve the web UI on this address, e.g. 127.0.0.1:8787")
	flag.Parse()

	if *dump {
		if err := runDump(); err != nil {
			fmt.Fprintln(os.Stderr, "claude-mgr:", err)
			os.Exit(1)
		}
		return
	}

	// The controller runs in a separate process spawned by tmux; pass the serve
	// address through the environment it inherits from this (server-starting)
	// launcher. Only takes effect when the dashboard is started fresh.
	if *serve != "" {
		_ = os.Setenv("CLAUDE_MGR_SERVE", *serve)
	}

	if err := launch(); err != nil {
		fmt.Fprintln(os.Stderr, "claude-mgr:", err)
		os.Exit(1)
	}
}

// launch ensures the dashboard's tmux session exists and attaches to it. The
// controller runs in the session's left pane via the __controller subcommand.
func launch() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := tmux.EnsureSession("'" + exe + "' __controller"); err != nil {
		return err
	}
	return tmux.Attach() // replaces this process with the tmux client
}

// runController runs the Bubble Tea rail. When CLAUDE_MGR_SERVE is set it also
// starts the web server in-process, sharing the model's overlay so the TUI and
// HTTP handlers never race a second writer.
func runController() error {
	store, err := index.NewStore()
	if err != nil {
		return err
	}
	m := ui.New(store)
	if addr := os.Getenv("CLAUDE_MGR_SERVE"); addr != "" {
		go server.Run(addr, store, m.Overlay()) //nolint:errcheck // errors go to the server log; stderr would corrupt the TUI
	}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err = p.Run()
	return err
}

func runDump() error {
	store, err := index.NewStore()
	if err != nil {
		return err
	}

	start := time.Now()
	sessions, err := store.Scan()
	if err != nil {
		return err
	}
	elapsed := time.Since(start)

	now := time.Now()
	groups := index.GroupByProject(sessions)

	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	for _, g := range groups {
		fmt.Fprintf(w, "\n\033[1m%s\033[0m\t(%d)\n", g.Label, len(g.Sessions))
		for _, s := range g.Sessions {
			fmt.Fprintf(w, "  %s  %s\t%s\t\033[2m%s\033[0m\n",
				s.Status.Dot(), index.RelTime(s.LastActive, now), s.AutoTitle, shortID(s.SessionID))
		}
	}
	w.Flush()

	fmt.Printf("\n%d sessions in %d projects · scanned in %s · cache %d hit / %d parsed\n",
		len(sessions), len(groups), elapsed.Round(time.Millisecond), store.Hits, store.Misses)
	return nil
}

func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}
