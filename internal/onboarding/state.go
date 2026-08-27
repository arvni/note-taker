package onboarding

import "fmt"

// Status is the employee onboarding lifecycle state (spec §4).
type Status string

const (
	NotInvited           Status = "not_invited"
	Invited              Status = "invited"
	Opened               Status = "opened"
	AuthorizationStarted Status = "authorization_started"
	Authorized           Status = "authorized"
	AuthorizationFailed  Status = "authorization_failed"
	Revoked              Status = "revoked"
	Disabled             Status = "disabled"
)

// transitions defines the legal onboarding state graph (spec §4).
var transitions = map[Status]map[Status]bool{
	NotInvited:           {Invited: true, Disabled: true},
	Invited:              {Opened: true, Invited: true, Disabled: true, AuthorizationFailed: true},
	Opened:               {AuthorizationStarted: true, Disabled: true, Invited: true},
	AuthorizationStarted: {Authorized: true, AuthorizationFailed: true, Disabled: true},
	AuthorizationFailed:  {AuthorizationStarted: true, Invited: true, Disabled: true},
	Authorized:           {Revoked: true, Disabled: true},
	Revoked:              {AuthorizationStarted: true, Invited: true, Disabled: true},
	Disabled:             {Invited: true}, // re-enable if directory reactivates
}

// CanTransition reports whether from→to is a permitted onboarding transition.
func CanTransition(from, to Status) bool {
	return transitions[from][to]
}

// Transition validates and returns the next state or an error (spec §4).
func Transition(from, to Status) (Status, error) {
	if !CanTransition(from, to) {
		return from, fmt.Errorf("onboarding: illegal transition %s -> %s", from, to)
	}
	return to, nil
}
