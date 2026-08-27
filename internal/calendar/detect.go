// Package calendar handles Zoho calendar discovery, event retrieval, and
// meeting-provider detection (spec §27, §29). Provider detection is pure logic
// over event fields so it can be unit-tested without live API calls.
package calendar

import (
	"regexp"
	"strings"
)

// Provider identifies a video-conferencing provider detected on an event.
type Provider string

const (
	ProviderNone   Provider = ""
	ProviderMeet   Provider = "google_meet"
	ProviderZoom   Provider = "zoom"
	ProviderTeams  Provider = "microsoft_teams"
)

var (
	reMeet  = regexp.MustCompile(`(?i)https?://meet\.google\.com/[a-z0-9\-]+`)
	reZoom  = regexp.MustCompile(`(?i)https?://[a-z0-9.\-]*zoom\.us/j/\d+[^\s"']*`)
	reTeams = regexp.MustCompile(`(?i)https?://teams\.microsoft\.com/l/meetup-join/[^\s"']+`)
)

// Detection is the result of scanning an event for a meeting.
type Detection struct {
	Provider   Provider
	MeetingURL string
}

// DetectMeeting scans an event's location, description, and conference-info
// fields for a supported meeting provider (spec §29). The first match wins in
// priority order Meet, Zoom, Teams. Returns ProviderNone if nothing matches.
func DetectMeeting(location, description, conferenceInfo string) Detection {
	haystack := strings.Join([]string{location, description, conferenceInfo}, "\n")

	if m := reMeet.FindString(haystack); m != "" {
		return Detection{ProviderMeet, m}
	}
	if m := reZoom.FindString(haystack); m != "" {
		return Detection{ProviderZoom, m}
	}
	if m := reTeams.FindString(haystack); m != "" {
		return Detection{ProviderTeams, m}
	}
	return Detection{ProviderNone, ""}
}

// HasMeeting reports whether the event qualifies as a detectable online meeting.
func (d Detection) HasMeeting() bool { return d.Provider != ProviderNone }
