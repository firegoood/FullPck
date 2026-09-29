package web

import (
	"strings"
	"testing"
)

func TestParseSocketCount(t *testing.T) {
	got, err := parseSocketCount(strings.NewReader("sockets: used 123\nTCP: inuse 11 orphan 0\n"))
	if err != nil || got != 123 {
		t.Fatalf("count = %d, err = %v", got, err)
	}
	if _, err := parseSocketCount(strings.NewReader("TCP: inuse 1\n")); err == nil {
		t.Fatal("missing count accepted")
	}
	if _, err := parseSocketCount(strings.NewReader("sockets: used nope\n")); err == nil {
		t.Fatal("invalid count accepted")
	}
}
