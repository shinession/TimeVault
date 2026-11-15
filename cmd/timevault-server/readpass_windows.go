//go:build windows

package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"
)

const enableEchoInput = 0x0004

var setConsoleMode = syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")

func isTerminal() bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(os.Stdin.Fd()), &mode) == nil
}

// readSecret reads one line from the console without echoing it; when stdin is
// piped it simply reads the line.
func readSecret(prompt string) (string, error) {
	h := syscall.Handle(os.Stdin.Fd())
	var mode uint32
	if err := syscall.GetConsoleMode(h, &mode); err != nil {
		return readLine()
	}
	fmt.Fprint(os.Stderr, prompt)
	_, _, _ = setConsoleMode.Call(uintptr(h), uintptr(mode&^enableEchoInput))
	defer func() {
		_, _, _ = setConsoleMode.Call(uintptr(h), uintptr(mode))
		fmt.Fprintln(os.Stderr)
	}()
	return readLine()
}

func readLine() (string, error) {
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
