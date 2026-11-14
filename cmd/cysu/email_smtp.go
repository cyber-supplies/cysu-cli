package main

// SMTP delivery runs in the CLI process. The API supplies the selected
// account's credentials; the email body never goes through the gateway.

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

var smtpDeliver = sendSMTP

// Gmail stores its incoming IMAP endpoint in the existing account fields.
// Use Gmail's submission endpoint for this send without changing that record.
func smtpDeliveryAccount(acct emailAccount) emailAccount {
	addr, err := emailServerAddress(acct)
	if err != nil {
		return acct
	}
	host, _, _ := net.SplitHostPort(addr)
	if strings.EqualFold(strings.TrimSuffix(host, "."), "imap.gmail.com") {
		acct.Server = "smtp.gmail.com"
		acct.Port = 587
	}
	return acct
}

func smtpAddress(value string) (string, error) {
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value || strings.ContainsAny(value, "\r\n") {
		return "", cliError{message: "email send: --recipient and account username must be plain email addresses", exit: exitUsage}
	}
	for _, r := range value {
		if r > 127 {
			return "", cliError{message: "email send: SMTPUTF8 addresses are not supported", exit: exitUsage}
		}
	}
	return parsed.Address, nil
}

func smtpMessage(from, to, subject, body string) ([]byte, error) {
	if _, err := smtpAddress(from); err != nil {
		return nil, err
	}
	if _, err := smtpAddress(to); err != nil {
		return nil, err
	}
	if strings.TrimSpace(subject) == "" || strings.ContainsAny(subject, "\r\n\x00") {
		return nil, cliError{message: "email send: subject must be nonempty and contain no line breaks", exit: exitUsage}
	}
	if strings.TrimSpace(body) == "" || len(body) > 1<<20 {
		return nil, cliError{message: "email send: body must be nonempty and at most 1 MiB", exit: exitUsage}
	}
	if len(subject) > 998 {
		return nil, cliError{message: "email send: subject is too long", exit: exitUsage}
	}
	encodedSubject := subject
	for _, r := range subject {
		if r > 127 {
			encodedSubject = mime.QEncoding.Encode("utf-8", subject)
			break
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "From: <%s>\r\nTo: <%s>\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n", from, to, encodedSubject)
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	for len(encoded) > 76 {
		out.WriteString(encoded[:76])
		out.WriteString("\r\n")
		encoded = encoded[76:]
	}
	out.WriteString(encoded)
	out.WriteString("\r\n")
	return []byte(out.String()), nil
}

func sendSMTP(acct emailAccount, recipient, subject, body string) error {
	return sendSMTPWithRoots(acct, recipient, subject, body, nil)
}

func sendSMTPWithRoots(acct emailAccount, recipient, subject, body string, roots *x509.CertPool) error {
	message, err := smtpMessage(acct.Username, recipient, subject, body)
	if err != nil {
		return err
	}
	addr, err := emailServerAddress(acct)
	if err != nil {
		return err
	}
	host, portText, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portText)
	tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: roots}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	if port == 465 {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connecting to SMTP %s: %w", addr, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(45 * time.Second)); err != nil {
		return err
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("SMTP greeting: %w", err)
	}
	defer client.Close()
	if port != 465 {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return failf("SMTP server %s does not support STARTTLS", addr)
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("SMTP STARTTLS: %w", err)
		}
	}
	if err := client.Auth(smtp.PlainAuth("", acct.Username, acct.Password, host)); err != nil {
		return fmt.Errorf("SMTP authentication: %w", err)
	}
	if err := client.Mail(acct.Username); err != nil {
		return fmt.Errorf("SMTP sender: %w", err)
	}
	if err := client.Rcpt(recipient); err != nil {
		return fmt.Errorf("SMTP recipient: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err := w.Write(message); err != nil {
		return fmt.Errorf("writing SMTP message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("sending SMTP message: %w", err)
	}
	_ = client.Quit()
	return nil
}
