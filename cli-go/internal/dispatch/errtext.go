package dispatch

import "strings"

// PrefixedMessage returns err's text with exactly one leading "dispatch: ".
// Errors raised in this package already begin with it, and a transport that adds
// the prefix to every error (the REST handler, the CLI) printed it twice for
// those: "dispatch: dispatch: runtime codex was requested explicitly ...". An
// error from elsewhere (an adapter, the OS) gets the prefix.
func PrefixedMessage(err error) string {
	msg := err.Error()
	if strings.HasPrefix(msg, "dispatch:") {
		return msg
	}
	return "dispatch: " + msg
}
