package email

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"
)

// SMTPSender sends mail via an authenticated SMTP relay (spec §21). Use a
// dedicated transactional relay or the company's approved Zoho Mail SMTP; never
// employee credentials.
type SMTPSender struct {
	Host string // e.g. smtp.zoho.com
	Port string // e.g. 587
	User string
	Pass string
	From string
}

// NewSMTPSender builds an SMTP sender.
func NewSMTPSender(host, port, user, pass, from string) *SMTPSender {
	return &SMTPSender{Host: host, Port: port, User: user, Pass: pass, From: from}
}

// Send delivers an HTML message over STARTTLS (implicit via smtp.SendMail).
func (s *SMTPSender) Send(_ context.Context, m Message) error {
	from := m.From
	if from == "" {
		from = s.From
	}
	msg := buildMIME(from, m.To, m.Subject, m.HTMLBody)
	auth := smtp.PlainAuth("", s.User, s.Pass, s.Host)
	addr := s.Host + ":" + s.Port
	if err := smtp.SendMail(addr, auth, from, []string{m.To}, msg); err != nil {
		return fmt.Errorf("email: smtp send: %w", err)
	}
	return nil
}

func buildMIME(from, to, subject, htmlBody string) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(htmlBody)
	return []byte(b.String())
}
