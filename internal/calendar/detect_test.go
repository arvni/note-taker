package calendar

import "testing"

func TestDetectMeeting(t *testing.T) {
	cases := []struct {
		name    string
		loc     string
		desc    string
		conf    string
		want    Provider
		wantURL string
	}{
		{"google meet in location", "https://meet.google.com/abc-defg-hij", "", "", ProviderMeet, "https://meet.google.com/abc-defg-hij"},
		{"zoom in description", "", "Join: https://company.zoom.us/j/12345678?pwd=xyz", "", ProviderZoom, "https://company.zoom.us/j/12345678?pwd=xyz"},
		{"teams in conference info", "", "", "https://teams.microsoft.com/l/meetup-join/19%3ameeting", ProviderTeams, "https://teams.microsoft.com/l/meetup-join/19%3ameeting"},
		{"no meeting", "Conference Room B", "Quarterly review", "", ProviderNone, ""},
		{"teams live (personal) wrapped in <> by Outlook", "Microsoft Teams Meeting", "Click here to join the meeting<https://teams.live.com/meet/9355386776662?p=1kaSWKbzYPuLhQJfwA>\nMeeting ID: 935", "", ProviderTeams, "https://teams.live.com/meet/9355386776662?p=1kaSWKbzYPuLhQJfwA"},
		{"teams short meet link", "", "Join: https://teams.microsoft.com/meet/26839283?p=abc", "", ProviderTeams, "https://teams.microsoft.com/meet/26839283?p=abc"},
		{"teams download link is not a meeting", "", "Download Teams<https://www.microsoft.com/en-us/microsoft-teams/download-app>", "", ProviderNone, ""},
		{"meet wins priority", "https://meet.google.com/xyz-1234-abc", "also https://zoom.us/j/999", "", ProviderMeet, "https://meet.google.com/xyz-1234-abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectMeeting(tc.loc, tc.desc, tc.conf)
			if got.Provider != tc.want {
				t.Errorf("provider: got %q want %q", got.Provider, tc.want)
			}
			if got.MeetingURL != tc.wantURL {
				t.Errorf("url: got %q want %q", got.MeetingURL, tc.wantURL)
			}
		})
	}
}
