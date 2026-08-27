// Package policy decides which calendars to monitor and which events to
// synchronize, enforcing the privacy defaults (spec §28, §30, §31). All logic is
// pure so it can be unit-tested without any API calls.
package policy

import "strings"

// Calendar is the subset of calendar fields policy needs.
type Calendar struct {
	Name     string
	Type     string
	Enabled  bool
	Personal bool
}

// Event is the subset of event fields policy needs.
type Event struct {
	IsPrivate  bool
	HasMeeting bool
}

// Recording captures the org-vs-employee recording decision (spec §31).
type Recording struct {
	EmployeeEnabled bool
	OrgDefault      bool
	OrgOverrides    bool // when true, OrgDefault wins over EmployeeEnabled
}

// Effective resolves the recording preference: org policy overrides the employee
// preference only when explicitly configured to (spec §31).
func (r Recording) Effective() bool {
	if r.OrgOverrides {
		return r.OrgDefault
	}
	return r.EmployeeEnabled
}

// personalHints are calendar type/name markers that indicate a personal calendar
// which must stay disabled by default (spec §28).
var personalHints = []string{"personal", "private", "birthday", "holiday", "family"}

// ClassifyPersonal reports whether a calendar looks personal (spec §28). This is
// a conservative heuristic; admins/employees can override the enabled flag.
func ClassifyPersonal(name, calType string) bool {
	hay := strings.ToLower(name + " " + calType)
	for _, h := range personalHints {
		if strings.Contains(hay, h) {
			return true
		}
	}
	return false
}

// DefaultEnabled returns the initial monitoring state for a newly discovered
// calendar: work/company calendars on, personal calendars off (spec §28).
func DefaultEnabled(personal bool) bool { return !personal }

// Decision is the outcome of evaluating an event against policy.
type Decision struct {
	Sync   bool
	Reason string
}

// Evaluate applies calendar, meeting, employee, and organization policy to decide
// whether an event should be synchronized (spec §30). The order matters: the
// cheapest, most privacy-protective checks run first.
func Evaluate(cal Calendar, ev Event, rec Recording) Decision {
	if !cal.Enabled {
		return Decision{false, "calendar not enabled"}
	}
	if cal.Personal {
		return Decision{false, "personal calendar"}
	}
	if ev.IsPrivate {
		return Decision{false, "private event"}
	}
	if !ev.HasMeeting {
		return Decision{false, "no online meeting"}
	}
	if !rec.Effective() {
		return Decision{false, "recording disabled by policy"}
	}
	return Decision{true, "qualifying meeting"}
}
