// Package mailer sends plain-text emails over SMTP using credentials
// supplied via environment variables (see config.SMTPConfig) — never
// hardcode SMTP credentials in code.
package mailer

import (
	"fmt"
	"net/mail"
	"net/smtp"
	"strings"

	"github.com/miksea/bot_discord_go/internal/config"
)

// Mailer sends emails through a single configured SMTP account.
type Mailer struct {
	cfg config.SMTPConfig
}

// New creates a Mailer from SMTP settings loaded from the environment.
func New(cfg config.SMTPConfig) *Mailer {
	return &Mailer{cfg: cfg}
}

// Configured reports whether enough SMTP settings are present to send mail.
// Callers should check this before relying on Send succeeding.
func (m *Mailer) Configured() bool {
	return m.cfg.Host != "" && m.cfg.Username != "" && m.cfg.Password != "" && m.cfg.From != ""
}

// Send delivers a plain-text email to a single recipient.
func (m *Mailer) Send(to, subject, body string) error {
	if !m.Configured() {
		return fmt.Errorf("smtp is not configured (missing SMTP_HOST/SMTP_USERNAME/SMTP_PASSWORD/SMTP_FROM)")
	}

	// SMTP_FROM boleh berisi display name, mis. "Nama <alamat@domain.com>".
	// Header "From" boleh memuat itu apa adanya, tapi MAIL FROM di envelope
	// SMTP wajib berupa alamat polos — server (mis. Resend) menolak dengan
	// "Bad sender address syntax" kalau display name ikut terkirim di sana.
	envelopeFrom, err := bareAddress(m.cfg.From)
	if err != nil {
		return fmt.Errorf("parse SMTP_FROM %q: %w", m.cfg.From, err)
	}

	addr := fmt.Sprintf("%s:%s", m.cfg.Host, m.cfg.Port)
	auth := smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
	msg := buildMessage(m.cfg.From, to, subject, body)

	if err := smtp.SendMail(addr, auth, envelopeFrom, []string{to}, msg); err != nil {
		return fmt.Errorf("send email to %s: %w", to, err)
	}
	return nil
}

// bareAddress extracts the plain email address from a header-style value
// such as "Name <email@domain.com>", returning the input unchanged if it is
// already a plain address.
func bareAddress(headerValue string) (string, error) {
	addr, err := mail.ParseAddress(headerValue)
	if err != nil {
		return "", err
	}
	return addr.Address, nil
}

func buildMessage(from, to, subject, body string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return []byte(b.String())
}
