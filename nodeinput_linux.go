//go:build linux

package main

import (
	"bufio"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// readEnrollmentCode suppresses echo for an interactive Linux terminal. A
// pipe remains available for automation without exposing the code in argv.
func readEnrollmentCode() (string, error) {
	fd := int(os.Stdin.Fd())
	state, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err == nil {
		noEcho := *state
		noEcho.Lflag &^= unix.ECHO
		if err := unix.IoctlSetTermios(fd, unix.TCSETS, &noEcho); err != nil {
			return "", err
		}
		defer func() {
			_ = unix.IoctlSetTermios(fd, unix.TCSETS, state)
			fmt.Println()
		}()
	}
	return bufio.NewReader(os.Stdin).ReadString('\n')
}
