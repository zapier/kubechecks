package pkg

// CommitState is an enum for represnting the state of a commit for posting via CommitStatus
type CommitState uint8

// must be in order of best to worst, in order for WorstState to work
const (
	StateNone CommitState = iota
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
