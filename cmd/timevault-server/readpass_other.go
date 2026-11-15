//go:build !windows

package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func isTerminal() bool {
	st, err := os.Stdin.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// readSecret reads one line from stdin. Echo is not suppressed on this
// platform; pipe the password in for non-interactive use.
func readSecret(prompt string) (string, error) {
	if isTerminal() {
		fmt.Fprint(os.Stderr, prompt)
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
