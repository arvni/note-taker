package config

import (
	"os"
	"strings"
	"testing"
)

func TestLoad_RejectsMailScope(t *testing.T) {
	os.Setenv("ZOHO_SCOPES", "ZohoCalendar.event.READ,ZohoMail.messages.READ")
	defer os.Unsetenv("ZOHO_SCOPES")
	if _, err := Load(); err == nil {
		t.Fatal("expected Load to reject a Zoho Mail scope (spec §33)")
	}
}

func TestLoad_RejectsAllScope(t *testing.T) {
	os.Setenv("ZOHO_SCOPES", "ZohoCalendar.event.ALL")
	defer os.Unsetenv("ZOHO_SCOPES")
	if _, err := Load(); err == nil {
		t.Fatal("expected Load to reject the over-broad .ALL scope (spec §11)")
	}
}

func TestDefaultScopes_NoMail(t *testing.T) {
	os.Unsetenv("ZOHO_SCOPES")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range c.Zoho.Scopes {
		if strings.Contains(strings.ToLower(s), "zohomail") {
			t.Fatalf("default scopes contain a Mail scope: %q", s)
		}
	}
}
