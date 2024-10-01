package pkg

import (
	"fmt"
	"strings"
)

// CommitState is an enum for represnting the state of a commit for posting via CommitStatus.
type CommitState uint8

// must be in order of best to worst, in order for WorstState to work.
const (
	StateNone CommitState = iota
	StateSkip
	StateSuccess
	StateRunning
	StateWarning
	StateFailure
	StateError
	StatePanic
)

func (s CommitState) BareString() string {
	text, ok := stateString[s]
	if !ok {
		text = defaultString
	}
	return text
}

var stateString = map[CommitState]string{
	StateNone:    "",
	StateSuccess: "Passed",
	StateRunning: "Running",
	StateWarning: "Warning",
	StateFailure: "Failed",
	StateError:   "Error",
	StatePanic:   "Panic",
}

const defaultString = "Unknown"

func WorstState(l1, l2 CommitState) CommitState {
	return max(l1, l2)
}

func BestState(l1, l2 CommitState) CommitState {
	return min(l1, l2)
}

func ParseCommitState(s string) (CommitState, error) {
	switch strings.ToLower(s) {
	case "success":
		return StateSuccess, nil
	case "running":
		return StateRunning, nil
	case "warning":
		return StateWarning, nil
	case "failure":
		return StateFailure, nil
	case "error":
		return StateError, nil
	case "panic":
		return StatePanic, nil
	default:
		return StateNone, fmt.Errorf("unknown commit state: %s", s)
	}
}
