package web

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/net"
)

// parseSocketCount reads the kernel's aggregate socket count. This avoids
// building a process/connection list for each dashboard refresh on Linux.
func parseSocketCount(r io.Reader) (int, error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 3 && fields[0] == "sockets:" && fields[1] == "used" {
			count, err := strconv.Atoi(fields[2])
			if err != nil || count < 0 {
				return 0, fmt.Errorf("invalid socket count %q", fields[2])
			}
			return count, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("socket count not found")
}

func portableSocketCount() (int, error) {
	connections, err := net.Connections("all")
	if err != nil {
		return 0, err
	}
	return len(connections), nil
}
