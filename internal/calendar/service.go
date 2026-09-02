package calendar

import (
	"context"
	"log"
	"time"

	"github.com/arvinizadi/fathom/internal/audit"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/policy"
)

// TokenProvider yields a valid access token for an employee (implemented by
// *tokens.Manager), refreshing under a lock as needed (spec §15-16).
type TokenProvider interface {
	AccessToken(ctx context.Context, org db.OrgID, employeeID int64) (string, error)
}

// PolicyProvider yields the recording decision inputs for an employee (spec §31).
type PolicyProvider interface {
	RecordingSettings(ctx context.Context, org db.OrgID, employeeID int64) (empEnabled, orgDefault, orgOverrides bool, err error)
}

// Service discovers calendars and scans enabled calendars for qualifying
// meetings, storing only minimized event data (spec §27-32).
// BaseFor resolves the org's Zoho Calendar API base URL (data-center specific).
type BaseFor func(ctx context.Context, org db.OrgID) (string, error)

type Service struct {
	tokens  TokenProvider
	baseFor BaseFor
	repo    *Repo
	policy  PolicyProvider
	audit   *audit.Logger
	window  time.Duration // how far ahead to scan (<= 31 days per Zoho)
	syncAll bool          // when true, sync every event, not only ones with a video link
}

func NewService(tp TokenProvider, baseFor BaseFor, repo *Repo, pp PolicyProvider, auditLog *audit.Logger, syncAll bool) *Service {
	return &Service{tokens: tp, baseFor: baseFor, repo: repo, policy: pp, audit: auditLog, window: 30 * 24 * time.Hour, syncAll: syncAll}
}

func (s *Service) api(ctx context.Context, org db.OrgID) (*APIClient, error) {
	base, err := s.baseFor(ctx, org)
	if err != nil {
		return nil, err
	}
	return NewAPIClient(base), nil
}

// Discover lists the employee's calendars and stores them, applying the default
// policy: work calendars enabled, personal calendars disabled (spec §27-28).
func (s *Service) Discover(ctx context.Context, org db.OrgID, employeeID int64) (int, error) {
	token, err := s.tokens.AccessToken(ctx, org, employeeID)
	if err != nil {
		return 0, err
	}
	api, err := s.api(ctx, org)
	if err != nil {
		return 0, err
	}
	cals, err := api.ListCalendars(ctx, token)
	if err != nil {
		return 0, err
	}
	for _, c := range cals {
		personal := policy.ClassifyPersonal(c.Name, c.Type)
		if err := s.repo.UpsertCalendar(ctx, StoredCalendar{
			EmployeeID: employeeID, UID: c.UID, Name: c.Name, Type: c.Type,
			Timezone: c.Timezone, Owner: c.Owner,
			Enabled:    policy.DefaultEnabled(personal),
			IsPersonal: personal,
		}); err != nil {
			return 0, err
		}
	}
	s.log(ctx, org, employeeID, audit.CalendarConnected, map[string]any{"count": len(cals)})
	return len(cals), nil
}

// Scan reads events from the employee's enabled calendars, detects meetings,
// applies privacy policy, and stores minimized qualifying events (spec §29-32).
// Returns the number of qualifying meetings stored.
func (s *Service) Scan(ctx context.Context, org db.OrgID, employeeID int64) (int, error) {
	token, err := s.tokens.AccessToken(ctx, org, employeeID)
	if err != nil {
		return 0, err
	}
	empEnabled, orgDefault, orgOverrides, err := s.policy.RecordingSettings(ctx, org, employeeID)
	if err != nil {
		return 0, err
	}
	rec := policy.Recording{EmployeeEnabled: empEnabled, OrgDefault: orgDefault, OrgOverrides: orgOverrides}

	api, err := s.api(ctx, org)
	if err != nil {
		return 0, err
	}
	cals, err := s.repo.ListEnabledCalendars(ctx, employeeID)
	if err != nil {
		return 0, err
	}
	from := time.Now()
	to := from.Add(s.window)

	stored := 0
	for _, cal := range cals {
		events, err := api.ListEvents(ctx, token, cal.UID, from, to)
		if err != nil {
			log.Printf("scan: employee=%d calendar=%s: %v", employeeID, cal.UID, err)
			continue // one bad calendar shouldn't abort the whole scan
		}
		calStored, noMeeting, other := 0, 0, 0
		for _, ev := range events {
			det := DetectMeeting(ev.Location, ev.Description, "")
			// When syncAll is set, every (non-personal, non-private) event syncs,
			// not just ones with a detected video link.
			hasMeeting := det.HasMeeting() || s.syncAll
			decision := policy.Evaluate(
				policy.Calendar{Name: cal.Name, Type: cal.Type, Enabled: true, Personal: cal.IsPersonal},
				policy.Event{IsPrivate: ev.IsPrivate, HasMeeting: hasMeeting},
				rec,
			)
			if !decision.Sync {
				if !det.HasMeeting() {
					noMeeting++
				} else {
					other++
				}
				continue
			}
			calStored++
			if err := s.repo.UpsertMapping(ctx, MinimizedEvent{
				EmployeeID:      employeeID,
				CalendarUID:     cal.UID,
				SourceEventID:   ev.UID,
				Title:           ev.Title,
				StartsAt:        parseZohoTime(ev.Start),
				EndsAt:          parseZohoTime(ev.End),
				MeetingProvider: string(det.Provider),
				MeetingURL:      det.MeetingURL,
				Description:     ev.Description,
				Location:        ev.Location,
				SourceUpdatedAt: parseZohoTime(ev.UpdatedAt),
			}); err != nil {
				return stored, err
			}
			stored++
		}
		log.Printf("scan: employee=%d calendar=%q fetched=%d stored=%d skipped_no_meeting=%d skipped_other=%d",
			employeeID, cal.Name, len(events), calStored, noMeeting, other)
	}
	return stored, nil
}

func (s *Service) log(ctx context.Context, org db.OrgID, empID int64, action audit.Action, md map[string]any) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Log(ctx, audit.Entry{OrganizationID: int64(org), EmployeeID: empID, Action: action, Metadata: md})
}

// parseZohoTime parses Zoho's yyyyMMdd'T'HHmmss'Z' (and date-only) formats.
func parseZohoTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	for _, layout := range []string{"20060102T150405Z", "20060102T150405Z0700", "20060102"} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}
