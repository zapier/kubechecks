package github_client

import "github.com/zapier/kubechecks/pkg"

var stateEmoji = map[pkg.CommitState]string{
	pkg.StateNone:    "",
	pkg.StateSuccess: ":white_check_mark:",
	pkg.StateRunning: ":runner:",
	pkg.StateWarning: ":warning:",
	pkg.StateFailure: ":red_circle:",
	pkg.StateError:   ":exclamation:",
	pkg.StatePanic:   ":skull:",
}

const defaultEmoji = ":interrobang:"

// ToEmoji returns a string representation of this state for use in the request
func (c *Client) ToEmoji(s pkg.CommitState) string {
	if emoji, ok := stateEmoji[s]; ok {
		return emoji
	} else {
		return defaultEmoji
	}
}
