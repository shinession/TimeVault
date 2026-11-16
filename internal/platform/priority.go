package platform

import "fmt"

// Priority is how much the operating system favours this process over others.
type Priority string

const (
	// PriorityNormal leaves the process as it was started.
	PriorityNormal Priority = "normal"
	// PriorityBelowNormal lowers the CPU priority, so the process yields to the services it shares the machine with.
	PriorityBelowNormal Priority = "below_normal"
	// PriorityBackground also lowers the disk and memory priority of the process (Windows background mode).
	PriorityBackground Priority = "background"
)

// ParsePriority reads the name of a priority.
func ParsePriority(s string) (Priority, error) {
	switch p := Priority(s); p {
	case PriorityNormal, PriorityBelowNormal, PriorityBackground:
		return p, nil
	}
	return "", fmt.Errorf("platform: unknown priority %q", s)
}
