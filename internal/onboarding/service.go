package onboarding

import (
	"context"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/arvinizadi/fathom/internal/audit"
	"github.com/arvinizadi/fathom/internal/db"
	"github.com/arvinizadi/fathom/internal/email"
)

// EmployeeStore is the subset of the employee repo the onboarding service needs.
type EmployeeStore interface {
	GetByID(ctx context.Context, org db.OrgID, id int64) (email string, name string, err error)
	SetOnboardingStatus(ctx context.Context, org db.OrgID, id int64, status string) error
}

// TokenStore persists onboarding token hashes (spec §6). *Repo implements it.
type TokenStore interface {
	Create(ctx context.Context, employeeID int64, tokenHash []byte, expiresAt time.Time) error
}

// Service creates onboarding invitations: it mints a single-use token, stores
// only its hash, emails a consent link, transitions the employee to `invited`,
// and audits the action (spec §5-7, §22, §35). It implements directory.Inviter.
type Service struct {
	tokens   TokenStore
	emps     EmployeeStore
	sender   email.Sender
	audit    *audit.Logger
	inviteTM *template.Template

	baseURL     string
	companyName string
	appName     string
	permissions []string
	supportAddr string
	privacyURL  string
	fromAddr    string
	tokenBytes  int
	ttl         time.Duration
}

// Config configures the onboarding Service.
type Config struct {
	BaseURL     string // e.g. https://calendar-sync.company.com (verified TLS, spec §23)
	CompanyName string
	AppName     string
	Permissions []string // human-readable, mirrors requested scopes (spec §22)
	SupportAddr string
	PrivacyURL  string
	FromAddr    string // dedicated sender (spec §21)
	TokenBytes  int
	TTL         time.Duration
}

func NewService(tokens TokenStore, emps EmployeeStore, sender email.Sender, auditLog *audit.Logger, inviteTemplate *template.Template, cfg Config) *Service {
	if cfg.TokenBytes < MinTokenBytes {
		cfg.TokenBytes = 48
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 7 * 24 * time.Hour
	}
	return &Service{
		tokens: tokens, emps: emps, sender: sender, audit: auditLog, inviteTM: inviteTemplate,
		baseURL:     strings.TrimRight(cfg.BaseURL, "/"),
		companyName: cfg.CompanyName, appName: cfg.AppName, permissions: cfg.Permissions,
		supportAddr: cfg.SupportAddr, privacyURL: cfg.PrivacyURL, fromAddr: cfg.FromAddr,
		tokenBytes: cfg.TokenBytes, ttl: cfg.TTL,
	}
}

// Invite implements directory.Inviter (spec §44 steps 5-8).
func (s *Service) Invite(ctx context.Context, org db.OrgID, employeeID int64) error {
	empEmail, _, err := s.emps.GetByID(ctx, org, employeeID)
	if err != nil {
		return err
	}

	raw, hash, err := GenerateToken(s.tokenBytes)
	if err != nil {
		return err
	}
	expiresAt := time.Now().Add(s.ttl)
	if err := s.tokens.Create(ctx, employeeID, hash, expiresAt); err != nil {
		return err
	}

	consentURL := s.baseURL + "/connect/" + raw // raw token in path, never a secret in query (spec §22)
	msg, err := email.RenderInvite(s.inviteTM, s.fromAddr, email.InviteData{
		CompanyName:    s.companyName,
		AppName:        s.appName,
		EmployeeEmail:  empEmail,
		Permissions:    s.permissions,
		ConsentURL:     consentURL,
		ExpiresAt:      expiresAt.Format("2006-01-02 15:04 MST"),
		SupportContact: s.supportAddr,
		PrivacyURL:     s.privacyURL,
	})
	if err != nil {
		return err
	}
	if err := s.sender.Send(ctx, msg); err != nil {
		return fmt.Errorf("onboarding: send invite: %w", err)
	}

	if err := s.emps.SetOnboardingStatus(ctx, org, employeeID, string(Invited)); err != nil {
		return err
	}
	if s.audit != nil {
		_ = s.audit.Log(ctx, audit.Entry{
			OrganizationID: int64(org),
			EmployeeID:     employeeID,
			Action:         audit.InvitationSent,
			Metadata:       map[string]any{"email": empEmail, "expires_at": expiresAt},
		})
	}
	return nil
}

// Reconnect re-invites an employee whose credential became invalid, sending a
// fresh consent link (spec §18, §45). Reauthorization is a full repeat of the
// onboarding consent, so it delegates to Invite. Satisfies tokens.Reconnector.
func (s *Service) Reconnect(ctx context.Context, org db.OrgID, employeeID int64) error {
	return s.Invite(ctx, org, employeeID)
}
