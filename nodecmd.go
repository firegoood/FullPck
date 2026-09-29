package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/firegoood/FullPck/internal/manage/core"
	"github.com/firegoood/FullPck/internal/node"
	"github.com/firegoood/FullPck/internal/webui"
)

// `fullpack node join` provisions an outbound Agent on a managed Node.

const nodeUsage = `fullpack node — the reverse Agent side of this server

  fullpack node join
        Read a one-time BPENROLL1 code interactively, provision a distinct
        permanent Agent credential, and save it under /etc/fullpack.

The node dials the configured Controller over WebSocket from the existing
fullpack-monitor.service. It does not open an inbound management listener.
`

func runNode(args []string) {
	if len(args) == 0 {
		fmt.Print(nodeUsage)
		os.Exit(2)
	}
	switch args[0] {
	case "join":
		nodeJoin(args[1:])
	case "-h", "--help", "help":
		fmt.Print(nodeUsage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", args[0], nodeUsage)
		os.Exit(2)
	}
}

func nodeJoin(args []string) {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "node join does not accept secrets as command-line arguments")
		os.Exit(2)
	}
	fmt.Print("Enrollment code: ")
	line, err := readEnrollmentCode()
	if err != nil && strings.TrimSpace(line) == "" {
		fmt.Fprintln(os.Stderr, "could not read enrollment code:", err)
		os.Exit(2)
	}
	cfg, err := node.JoinWithEnrollment(strings.TrimSpace(line), &http.Client{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Enrollment marks this machine as a managed foreign node. Remove any
	// panel that may have been started during the initial interactive install
	// before keeping the monitor/Agent service alive.
	if err := webui.Disable(); err != nil {
		fmt.Fprintln(os.Stderr, "Node saved but WebUI could not be disabled:", err)
		os.Exit(1)
	}
	// The join is the only place that needs to ensure the existing monitor
	// service. No separate Agent unit is installed.
	if err := core.RestartMonitorService(); err != nil {
		fmt.Fprintln(os.Stderr, "Agent saved but monitor service could not be ensured:", err)
		os.Exit(1)
	}
	fmt.Printf("Node %s enrolled. The outbound Agent will connect to the Controller.\n", cfg.Name)
}
