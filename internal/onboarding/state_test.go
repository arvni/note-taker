package onboarding

import "testing"

func TestLegalTransitions(t *testing.T) {
	ok := [][2]Status{
		{NotInvited, Invited},
		{Invited, Opened},
		{Opened, AuthorizationStarted},
		{AuthorizationStarted, Authorized},
		{Authorized, Revoked},
		{Revoked, AuthorizationStarted},
	}
	for _, tc := range ok {
		if _, err := Transition(tc[0], tc[1]); err != nil {
			t.Errorf("expected legal %s->%s: %v", tc[0], tc[1], err)
		}
	}
}

func TestIllegalTransitions(t *testing.T) {
	bad := [][2]Status{
		{NotInvited, Authorized},   // cannot skip consent
		{Authorized, Opened},       // cannot go backwards
		{Disabled, Authorized},     // must re-invite first
	}
	for _, tc := range bad {
		if _, err := Transition(tc[0], tc[1]); err == nil {
			t.Errorf("expected illegal %s->%s to fail", tc[0], tc[1])
		}
	}
}
