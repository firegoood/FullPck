//go:build !linux

package main

import (
	"bufio"
	"os"
)

func readEnrollmentCode() (string, error) {
	return bufio.NewReader(os.Stdin).ReadString('\n')
}
