// Package menu implements the interactive fullpack CLI shown when the binary
// is run without a config file.
package menu

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/firegoood/FullPck/internal/app"
	"github.com/firegoood/FullPck/internal/manage"
	"github.com/firegoood/FullPck/internal/node"
	"github.com/firegoood/FullPck/internal/schedule"
	"github.com/firegoood/FullPck/internal/tui"
	"github.com/firegoood/FullPck/internal/webui"
)

// ipStore caches the server's public IPv4 so menus never block on a lookup.
var ipStore atomic.Value // holds string

// Run starts the interactive menu loop.
func Run() {
	requireRoot()
	managedNode := node.IsManagedForeign()

	// Bring the monitoring web panel up in the background and start resolving
	// the public IP (shown inside the Web Panel section).
	if !managedNode {
		if _, err := webui.EnsureRunning(); err != nil {
			tui.Warn("Web panel could not start: " + err.Error())
			tui.PressEnter()
		}
	}
	if managedNode {
		// A managed foreign node is controlled through the Iran Controller's
		// existing WebUI listener. Its local CLI remains useful for tunnels and
		// maintenance, but it must not expose a second panel.
		if err := webui.Disable(); err != nil {
			tui.Warn("Web panel could not be disabled on this managed foreign node: " + err.Error())
			tui.PressEnter()
		}
	}

	// The same for the tunnels' own units: a tunnel created by an older version
	// keeps that version's unit file, which is how servers stayed on systemd's
	// default open-file ceiling long after the template had been raised.
	// Nothing is restarted; each tunnel picks its unit up when it next starts.
	manage.EnsureUnits()

	// The watchdog, the Telegram bot and the alerts run in their own service so
	// they survive the panel being stopped. Installing it here is also how an
	// install that predates the service picks it up.
	if err := manage.EnsureMonitorService(); err != nil {
		tui.Warn("Monitor Could Not Start: " + err.Error())
		tui.PressEnter()
	}

	go resolveServerIP()

	// Look for a newer release in the background. The menu itself only ever
	// reads the cached answer, so a slow or blocked GitHub cannot delay a
	// redraw — the notice simply appears once the check comes back.
	go manage.RefreshUpdateCheckIfStale(6 * time.Hour)

	for {
		tui.Clear()
		tui.Logo(app.Version)
		printUpdateBanner()
		tui.Rule()
		printMenuForRole(managedNode)

		choice, ok := tui.PromptOrEnd("Select an option: ")
		if !ok {
			// stdin is gone: nobody is left to answer the menu.
			fmt.Println()
			return
		}
		switch choice {
		case "0":
			manage.ConnectionTest()
		// Both entries ask which direction the tunnel should be built in, and
		// a reverse one is then built by exactly the code that has always
		// built it. See manage.SetupIran.
		case "1":
			manage.SetupIran()
		case "2":
			manage.SetupKharej()
		case "3":
			manageMenu()
		case "4":
			backupMenu()
		case "5":
			if managedNode {
				tui.Warn("Web Panel is available only on the Iran Controller.")
				tui.PressEnter()
				continue
			}
			webPanelMenu()
		case "6":
			optimizeMenu()
		case "7":
			telegramMenu()
		case "8":
			updateMenu()
		case "9":
			uninstallMenu()
		case "10":
			tui.Info("Goodbye!")
			return
		default:
			tui.Error("Invalid option.")
			tui.PressEnter()
		}
	}
}

// printUpdateBanner shows a one-line notice when a newer release exists. It
// reads the cache only, so it costs nothing and prints nothing until the
// background check has an answer.
func printUpdateBanner() {
	tag, ok := manage.UpdateAvailable()
	if !ok {
		return
	}
	fmt.Printf("  %s⬆ %s Is Available%s %s— Option 8%s\n",
		tui.Bold+tui.Red, tag, tui.Reset, tui.Gray, tui.Reset)
}

// printMenu renders the controller menu: red numbers, white titles, gray
// descriptions. Keep the no-argument form for callers and tests that render
// the normal Iran-side menu.
func printMenu() {
	printMenuForRole(false)
}

func printMenuForRole(managedNode bool) {
	fmt.Println()
	menuItem(1, "Setup Iran", "the server your users connect to — it exposes the ports")
	menuItem(2, "Setup Kharej", "the server abroad — it holds the real service")
	menuItem(3, "Manage", "tunnels, ports, transport, status, health check")
	menuItem(4, "Backup & Restore", "save or restore the full configuration")
	if managedNode {
		menuItem(5, "Web Panel", "disabled on a managed foreign node")
	} else {
		menuItem(5, "Web Panel", "monitoring web UI — link, login code, port")
	}
	menuItem(6, "Optimize", "kernel & network tuning — BBR, buffers, limits")
	menuItem(7, "Telegram Bot", "status reports, relayed through a tunnel")
	updateDesc := "safe update with automatic rollback"
	if tag, ok := manage.UpdateAvailable(); ok {
		updateDesc = tag + " is out"
	}
	menuItem(8, "Update", updateDesc)
	menuItem(9, "Uninstall", "remove everything")
	menuItem(10, "Exit", "")
	fmt.Println()
}

// menuItem prints one aligned, colored menu row.
func menuItem(n int, title, desc string) {
	num := tui.Color(tui.Red, fmt.Sprintf("%2d)", n))
	if desc == "" {
		fmt.Printf("  %s %s%-18s%s\n", num, tui.Bold+tui.White, title, tui.Reset)
		return
	}
	fmt.Printf("  %s %s%-18s%s %s%s%s\n",
		num, tui.Bold+tui.White, title, tui.Reset, tui.Gray, desc, tui.Reset)
}

// cachedServerIP returns the resolved public IPv4 if known, otherwise a
// placeholder — it never blocks, so it's safe for redrawn screens.
func cachedServerIP() string {
	if v, _ := ipStore.Load().(string); v != "" {
		return v
	}
	return "detecting…"
}

// resolveServerIP fetches and caches the public IPv4 (blocking). Used where an
// accurate value matters, e.g. when showing the panel credentials.
func resolveServerIP() string {
	if v, _ := ipStore.Load().(string); v != "" {
		return v
	}
	ip := manage.PublicIPv4()
	if ip != "" && ip != "-" {
		ipStore.Store(ip)
		return ip
	}
	return "-"
}

func refreshLabel() string {
	h := schedule.AutoRefreshHours()
	if h <= 0 {
		return "disabled"
	}
	return fmt.Sprintf("every %dh", h)
}

func requireRoot() {
	if os.Geteuid() != 0 {
		tui.Error("FullPack must be run as root (use: sudo fullpack).")
		os.Exit(1)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
