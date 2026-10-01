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
        Repeat with each Iran Controller's code to connect this Node to more
        than one Controller. Existing enrollments remain connected.

  fullpack node retire-http <http-origin>
        Remove this HTTP enrollment after separately joining its HTTPS
        replacement on the same host and port with a certificate pin.
        Save a private backup first. Other Controllers and tunnels remain.

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
	case "retire-http":
		nodeRetireHTTP(args[1:])
	case "-h", "--help", "help":
		fmt.Print(nodeUsage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", args[0], nodeUsage)
		os.Exit(2)
	}
}

func nodeRetireHTTP(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: fullpack node retire-http <http-origin>")
		os.Exit(2)
	}
	backup, err := node.RetireHTTPController(args[0])
	if backup != "" {
		fmt.Printf("Private Controller backup: %s\n", backup)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("HTTP enrollment retired. The monitor will keep the remaining Controllers connected without restarting tunnels.")
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
	if err := core.EnsureMonitorService(); err != nil {
		fmt.Fprintln(os.Stderr, "Agent saved but monitor service could not be ensured:", err)
		os.Exit(1)
	}
	fmt.Printf("Node %s enrolled. The outbound Agent will connect to %s.\n", cfg.Name, cfg.ControllerURL)
}
