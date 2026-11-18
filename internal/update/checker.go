package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Checker examines a downloaded program before it takes the place of the running one.
type Checker interface {
	// Version is the version the program says it is.
	Version(ctx context.Context, program string) (string, error)
	// Config reports whether the program accepts the settings of this service: the same environment and the same
	// .env file, which it finds next to itself like the running program does.
	Config(ctx context.Context, program string) error
}

// ExecChecker runs the program with its version and check-config commands. The program is only run after its sha256
// was found to be the one of the manifest.
type ExecChecker struct {
	// Timeout limits each run; zero means 30 seconds. The first run of a new file can be slow while a virus scanner
	// looks at it.
	Timeout time.Duration
}

// Version runs "program version", which prints "timevault-server <version>".
func (c ExecChecker) Version(ctx context.Context, program string) (string, error) {
	out, err := c.run(ctx, program, "version")
	if err != nil {
		return "", fmt.Errorf("run the new program: %w", err)
	}
	fields := strings.Fields(out)
	if len(fields) != 2 || fields[0] != "timevault-server" {
		return "", fmt.Errorf("the new program does not say what version it is (it printed %q)", firstLine(out))
	}
	return NormalizeVersion(fields[1]), nil
}

// Config runs "program check-config", which succeeds when the program accepts the settings.
func (c ExecChecker) Config(ctx context.Context, program string) error {
	out, err := c.run(ctx, program, "check-config")
	if err == nil {
		return nil
	}
	reason := firstLine(out)
	if reason == "" {
		reason = err.Error()
	}
	return fmt.Errorf("the new program does not accept the settings of this service: %s", reason)
}

func (c ExecChecker) run(ctx context.Context, program, arg string) (string, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, arg)
	cmd.Env = os.Environ()
	cmd.WaitDelay = 2 * time.Second
	out := &capped{max: 8 << 10}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return out.String(), fmt.Errorf("it did not answer within %s", timeout)
		}
		return out.String(), err
	}
	return out.String(), nil
}

// capped keeps the first bytes of what a program prints and drops the rest, so that a program that does not stop
// printing cannot fill the memory.
type capped struct {
	buf bytes.Buffer
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (c *capped) String() string { return c.buf.String() }

// firstLine is the first line of some output that has text, without a leading "error:".
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "error:"))
		if line != "" {
			if len(line) > 300 {
				line = line[:300] + "..."
			}
			return line
		}
	}
	return ""
}
