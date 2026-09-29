package webui

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// Everything on the metrics screen sits on one gutter.
//
// The section headings, the chart and the tables all keep 18px from the edge of
// the dialog. The three headline figures — Carrying now, Peak in the day, Up
// last 24 hours — sat directly in the body with none, so the one part of the
// screen that is pure reading ran edge to edge while everything around it was
// inset, and the numbers looked pressed into the corners.
func TestTheMetricsScreenKeepsOneGutter(t *testing.T) {
	loadPanel()

	css, err := fs.ReadFile(panelRoot, "css/screens/metrics.css")
	if err != nil {
		t.Fatalf("metrics.css: %v", err)
	}
	src := string(css)

	// The gutter every other block keeps, taken from the section heading rather
	// than written here — if that changes, this asks for the new one.
	sec := regexp.MustCompile(`\.dlg \.sec2\{[^}]*padding:\s*[\d.]+px\s+([\d.]+px)`).FindStringSubmatch(src)
	if sec == nil {
		t.Fatal("the section heading no longer declares a padding to line up with")
	}
	gutter := sec[1]

	rows := regexp.MustCompile(`\.dlg \.mrows\{([^}]*)\}`).FindStringSubmatch(src)
	if rows == nil {
		t.Fatal("the headline figures have no rule of their own")
	}
	if !strings.Contains(rows[1], "padding:0 "+gutter) {
		t.Errorf("the headline figures do not keep the screen's %s gutter, so they run "+
			"to the edge while every heading and chart around them is inset:\n  .mrows{%s}",
			gutter, rows[1])
	}

	// And the chart above them keeps it too, or the rows would line up with
	// nothing.
	if !regexp.MustCompile(`\.dlg \.chartbox\{[^}]*\s` + regexp.QuoteMeta(gutter)).MatchString(src) {
		t.Errorf("the chart does not keep the %s gutter", gutter)
	}
}

// Agent enrollment has no SSH address or password editor on a server card.
func TestManagedServerCardExposesOnlyAgentActions(t *testing.T) {
	loadPanel()
	js, err := fs.ReadFile(panelRoot, "js/views/servers.js")
	if err != nil {
		t.Fatalf("servers.js: %v", err)
	}
	src := string(js)
	for _, old := range []string{"editPanel(", "nodeCredentials(", "SSH port", "New password"} {
		if strings.Contains(src, old) {
			t.Errorf("legacy SSH editor remains in the fleet card: %s", old)
		}
	}
	for _, action := range []string{"nodeRefresh(", "nodeRevoke(", "nodeRemove("} {
		if !strings.Contains(src, action) {
			t.Errorf("Agent card lost %s", action)
		}
	}
}
