package pkg

import "fmt"

// CommitState is an enum for represnting the state of a commit for posting via CommitStatus
type CommitState uint8

const (
	StateNone CommitState = iota
	StateSuccess
	StateRunning
	StateWarning
	StateFailure
	StateError
	StatePanic
)

// Emoji returns a string representation of this state for use in the request
func (s CommitState) Emoji() string {
	if emoji, ok := stateEmoji[s]; ok {
		return emoji
	} else {
		return defaultEmoji
	}
}

func (s CommitState) BareString() string {
	text, ok := stateString[s]
	if !ok {
		text = defaultString
	}
	return text
}

func (s CommitState) String() string {
	text, ok := stateString[s]
	if !ok {
		text = defaultString
	}

	if text == "" {
		return ""
	}

	return fmt.Sprintf("%s %s", text, s.Emoji())
}

var stateEmoji = map[CommitState]string{
	StateNone:    "",
	StateSuccess: ":white_check_mark:",
	StateRunning: ":running:",
	StateWarning: ":warning:",
	StateFailure: ":red_circle:",
	StateError:   ":heavy_exclamation_mark:",
	StatePanic:   ":skull:",
}

const defaultEmoji = ":interrobang:"

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
	if l2 > l1 {
		return l2
	}

	return l1
}
