package policy

import "testing"

func TestClassifyPersonal(t *testing.T) {
	cases := map[string]bool{
		"Work Calendar|":     false,
		"My Personal|":       true,
		"Holidays|holiday":   true,
		"Team Sales|company": false,
		"Birthdays|":         true,
	}
	for in, want := range cases {
		var name, typ string
		if i := indexByte(in, '|'); i >= 0 {
			name, typ = in[:i], in[i+1:]
		}
		if got := ClassifyPersonal(name, typ); got != want {
			t.Errorf("ClassifyPersonal(%q,%q)=%v want %v", name, typ, got, want)
		}
	}
}

func TestDefaultEnabled(t *testing.T) {
	if !DefaultEnabled(false) {
		t.Error("work calendar should default enabled")
	}
	if DefaultEnabled(true) {
		t.Error("personal calendar must default disabled (spec §28)")
	}
}

func TestRecordingEffective(t *testing.T) {
	if !( Recording{EmployeeEnabled: true, OrgDefault: false, OrgOverrides: false}).Effective() {
		t.Error("employee pref should win when org does not override")
	}
	if (Recording{EmployeeEnabled: true, OrgDefault: false, OrgOverrides: true}).Effective() {
		t.Error("org override should force off")
	}
	if !(Recording{EmployeeEnabled: false, OrgDefault: true, OrgOverrides: true}).Effective() {
		t.Error("org override should force on")
	}
}

func TestEvaluate(t *testing.T) {
	rec := Recording{EmployeeEnabled: true}
	work := Calendar{Enabled: true, Personal: false}

	if d := Evaluate(work, Event{HasMeeting: true}, rec); !d.Sync {
		t.Errorf("qualifying meeting should sync: %s", d.Reason)
	}
	if d := Evaluate(Calendar{Enabled: false}, Event{HasMeeting: true}, rec); d.Sync {
		t.Error("disabled calendar must not sync")
	}
	if d := Evaluate(Calendar{Enabled: true, Personal: true}, Event{HasMeeting: true}, rec); d.Sync {
		t.Error("personal calendar must not sync (spec §30)")
	}
	if d := Evaluate(work, Event{HasMeeting: true, IsPrivate: true}, rec); d.Sync {
		t.Error("private event must not sync (spec §30)")
	}
	if d := Evaluate(work, Event{HasMeeting: false}, rec); d.Sync {
		t.Error("non-meeting must not sync")
	}
	if d := Evaluate(work, Event{HasMeeting: true}, Recording{EmployeeEnabled: false}); d.Sync {
		t.Error("recording-disabled must not sync")
	}
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
