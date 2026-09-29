//go:build linux

package web

import "os"

func socketCount() (int, error) {
	f, err := os.Open("/proc/net/sockstat")
	if err == nil {
		defer f.Close()
		if count, parseErr := parseSocketCount(f); parseErr == nil {
			return count, nil
		}
	}
	return portableSocketCount()
}
