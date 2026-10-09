package cli

import "fmt"

// waitlistedMessage is what the CLI prints when the api answers
// `waitlisted` (DECISIONS I-269, I-290): the api's own sentence, which
// names the place in the queue and where the email goes, printed as it
// is. Only an api that sent no message (a proxy that stripped it) gets a
// sentence built here from the detail.
func waitlistedMessage(e *APIError) string {
	if e.Message != "" {
		return e.Message
	}
	pos, _ := e.Detail["position"].(float64)
	if pos < 1 {
		return "repose is full right now. You're on the waitlist; we'll email you when there's a seat."
	}
	email, _ := e.Detail["email"].(string)
	if email == "" {
		return fmt.Sprintf("repose is full right now. You're number %d on the waitlist; %s shows your place.", int(pos), billingURL)
	}
	return fmt.Sprintf("repose is full right now. You're number %d on the waitlist; we'll email %s when there's a seat.", int(pos), email)
}
