package manage

import (
	"reflect"
	"testing"
)

func TestEditVisiblePorts(t *testing.T) {
	current := []string{"443", "8080=127.0.0.1:80"}
	for _, tc := range []struct {
		action, index int
		entries       []string
		want          []string
	}{
		{0, -1, []string{"8443"}, []string{"443", "8080=127.0.0.1:80", "8443"}},
		{1, 0, []string{"444"}, []string{"444", "8080=127.0.0.1:80"}},
		{2, 1, nil, []string{"443"}},
		{3, -1, []string{"9000"}, []string{"9000"}},
	} {
		got, err := editVisiblePorts(current, tc.action, tc.index, tc.entries)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("action %d: got %v, err %v, want %v", tc.action, got, err, tc.want)
		}
		if !reflect.DeepEqual(current, []string{"443", "8080=127.0.0.1:80"}) {
			t.Fatal("input mutated")
		}
	}
	if _, err := editVisiblePorts([]string{"443"}, 2, 0, nil); err == nil {
		t.Fatal("removed final port")
	}
	if _, err := editVisiblePorts(current, 1, 0, []string{"bad"}); err == nil {
		t.Fatal("accepted invalid port")
	}
}
