package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
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

// Send delivers an HTML message. TLS mode is chosen by port: 465 is implicit
// TLS (SMTPS) — the connection is wrapped in TLS immediately; 587/25 use
// STARTTLS. Using STARTTLS logic against a 465 server (or vice versa) makes the
// server drop the connection ("EOF"), so we branch on the port.
func (s *SMTPSender) Send(_ context.Context, m Message) error {
	from := m.From
	if from == "" {
		from = s.From
	}
	msg := buildMIME(from, m.To, m.Subject, m.HTMLBody)
	auth := smtp.PlainAuth("", s.User, s.Pass, s.Host)
	addr := s.Host + ":" + s.Port
	tlsCfg := &tls.Config{ServerName: s.Host}
	dialer := &net.Dialer{Timeout: 15 * time.Second}

	var conn net.Conn
	var err error
	if s.Port == "465" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg) // implicit TLS
	} else {
		conn, err = dialer.Dial("tcp", addr) // plaintext; upgrade via STARTTLS below
	}
	if err != nil {
		return fmt.Errorf("email: smtp dial %s: %w", addr, err)
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return fmt.Errorf("email: smtp client: %w", err)
	}
	defer c.Close()

	if s.Port != "465" {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("email: smtp starttls: %w", err)
			}
		}
	}
	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("email: smtp auth: %w", err)
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("email: smtp mail: %w", err)
	}
	if err := c.Rcpt(m.To); err != nil {
		return fmt.Errorf("email: smtp rcpt: %w", err)
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("email: smtp data: %w", err)
	}
	if _, err := wc.Write(msg); err != nil {
		return fmt.Errorf("email: smtp write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("email: smtp close: %w", err)
	}
	return c.Quit()
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
