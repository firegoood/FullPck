package spec

import "testing"

func TestDistinctForwardedPortBindings(t *testing.T) {
	for _, ports := range [][]string{
		{"443", "443"},
		{"440-450", "443"},
		{"0.0.0.0:443=127.0.0.1:443", "127.0.0.1:443=127.0.0.1:8443"},
		{"127.0.0.1:443=127.0.0.1:80", "127.0.0.1:443=127.0.0.1:81"},
	} {
		if err := ValidatePortSpecs(ports); err == nil {
			t.Errorf("accepted overlapping listeners: %v", ports)
		}
	}
	if err := ValidatePortSpecs([]string{"127.0.0.1:443=127.0.0.1:80", "127.0.0.2:443=127.0.0.1:81"}); err != nil {
		t.Fatalf("distinct listeners rejected: %v", err)
	}
}
