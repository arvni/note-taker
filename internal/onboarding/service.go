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
	resolve  Resolver
	audit    *audit.Logger
	inviteTM *template.Template

	baseURL    string
	tokenBytes int
	ttl        time.Duration
}

// Brand is the per-org email/consent branding (spec §21-23).
type Brand struct {
	CompanyName string
	AppName     string
	SupportAddr string
	PrivacyURL  string
	FromAddr    string
	Permissions []string
}

// Resolver returns the email sender and branding for an org, so email/branding
// can be configured per-org in the app (with env fallback).
type Resolver func(ctx context.Context, org db.OrgID) (email.Sender, Brand)

func NewService(tokens TokenStore, emps EmployeeStore, resolve Resolver, auditLog *audit.Logger, inviteTemplate *template.Template, baseURL string, tokenBytes int, ttl time.Duration) *Service {
	if tokenBytes < MinTokenBytes {
		tokenBytes = 48
	}
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	return &Service{
		tokens: tokens, emps: emps, resolve: resolve, audit: auditLog, inviteTM: inviteTemplate,
		baseURL: strings.TrimRight(baseURL, "/"), tokenBytes: tokenBytes, ttl: ttl,
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

	sender, brand := s.resolve(ctx, org)
	consentURL := s.baseURL + "/connect/" + raw // raw token in path, never a secret in query (spec §22)
	msg, err := email.RenderInvite(s.inviteTM, brand.FromAddr, email.InviteData{
		CompanyName:    brand.CompanyName,
		AppName:        brand.AppName,
		EmployeeEmail:  empEmail,
		Permissions:    brand.Permissions,
		ConsentURL:     consentURL,
		ExpiresAt:      expiresAt.Format("2006-01-02 15:04 MST"),
		SupportContact: brand.SupportAddr,
		PrivacyURL:     brand.PrivacyURL,
	})
	if err != nil {
		return err
	}
	if err := sender.Send(ctx, msg); err != nil {
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
