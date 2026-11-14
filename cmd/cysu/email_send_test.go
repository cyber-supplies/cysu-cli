package main

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmailSendLoadsAccountAndDeliversFromClient(t *testing.T) {
	var delivered emailAccount
	var to, subject, body string
	previous := smtpDeliver
	smtpDeliver = func(acct emailAccount, recipient, title, text string) error {
		delivered, to, subject, body = acct, recipient, title, text
		return nil
	}
	t.Cleanup(func() { smtpDeliver = previous })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/emails" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer user-token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]emailAccount{{ID: "work", Server: "smtp.example.com", Username: "me@example.com", Password: "secret", Port: 465}})
	}))
	defer server.Close()
	t.Setenv("CYSU_API_URL", server.URL)
	t.Setenv("CYSU_TOKEN", "user-token")
	output := captureStdout(t, func() error {
		return emailResource.run([]string{"send", "--account", "work", "--recipient", "you@example.com", "--subject", "Hi", "--body", "Hello"})
	})
	var result map[string]any
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	if result["success"] != true || result["account"] != "work" || delivered.Server != "smtp.example.com" || delivered.Port != 465 || delivered.Username != "me@example.com" || delivered.Password != "secret" || to != "you@example.com" || subject != "Hi" || body != "Hello" {
		t.Fatalf("output = %s, account = %#v, delivery = %q %q %q", output, delivered, to, subject, body)
	}
}

func TestSMTPDeliveryAccountUsesGmailSubmissionSettings(t *testing.T) {
	for _, server := range []string{"imap.gmail.com", "IMAP.GMAIL.COM", "imap.gmail.com:993"} {
		original := emailAccount{Server: server, Port: 993, Username: "me@gmail.com", Password: "secret"}
		got := smtpDeliveryAccount(original)
		if got.Server != "smtp.gmail.com" || got.Port != 587 || got.Username != original.Username || got.Password != original.Password {
			t.Fatalf("%q: delivery account = %#v", server, got)
		}
		if original.Server != server || original.Port != 993 {
			t.Fatalf("source account changed: %#v", original)
		}
	}
	other := emailAccount{Server: "imap.example.com", Port: 993}
	if got := smtpDeliveryAccount(other); got != other {
		t.Fatalf("non-Gmail account changed: %#v", got)
	}
}

func TestSMTPSendsThroughSTARTTLS(t *testing.T) {
	certServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cert := certServer.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(certServer.Certificate())
	certServer.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		write := func(text string) error { _, err := conn.Write([]byte(text)); return err }
		read := func(reader *bufio.Reader, prefix string) (string, error) {
			line, err := reader.ReadString('\n')
			if err != nil {
				return "", err
			}
			if !strings.HasPrefix(line, prefix) {
				return "", fmt.Errorf("expected %q, got %q", prefix, line)
			}
			return line, nil
		}
		reader := bufio.NewReader(conn)
		if err := write("220 localhost ESMTP\r\n"); err != nil {
			done <- err
			return
		}
		if _, err := read(reader, "EHLO "); err != nil {
			done <- err
			return
		}
		if err := write("250-localhost\r\n250 STARTTLS\r\n"); err != nil {
			done <- err
			return
		}
		if _, err := read(reader, "STARTTLS"); err != nil {
			done <- err
			return
		}
		if err := write("220 Ready\r\n"); err != nil {
			done <- err
			return
		}
		secure := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
		if err := secure.Handshake(); err != nil {
			done <- err
			return
		}
		conn = secure
		reader = bufio.NewReader(secure)
		if _, err := read(reader, "EHLO "); err != nil {
			done <- err
			return
		}
		if err := write("250-localhost\r\n250 AUTH PLAIN\r\n"); err != nil {
			done <- err
			return
		}
		line, err := read(reader, "AUTH PLAIN ")
		if err != nil {
			done <- err
			return
		}
		secret, err := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(line, "AUTH PLAIN ")))
		if err != nil || string(secret) != "\x00me@example.com\x00secret" {
			done <- fmt.Errorf("SMTP auth = %q, %v", secret, err)
			return
		}
		if err := write("235 Authenticated\r\n"); err != nil {
			done <- err
			return
		}
		for _, step := range []struct{ prefix, reply string }{
			{"MAIL FROM:<me@example.com>", "250 OK\r\n"},
			{"RCPT TO:<you@example.com>", "250 OK\r\n"},
			{"DATA", "354 Send data\r\n"},
		} {
			if _, err := read(reader, step.prefix); err != nil {
				done <- err
				return
			}
			if err := write(step.reply); err != nil {
				done <- err
				return
			}
		}
		var message strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				done <- err
				return
			}
			if line == ".\r\n" {
				break
			}
			message.WriteString(line)
		}
		if !strings.Contains(message.String(), "Subject: Hi\r\n") || !strings.Contains(message.String(), base64.StdEncoding.EncodeToString([]byte("Hello"))) {
			done <- fmt.Errorf("message = %q", message.String())
			return
		}
		if err := write("250 Accepted\r\n"); err != nil {
			done <- err
			return
		}
		if _, err := read(reader, "QUIT"); err != nil {
			done <- err
			return
		}
		done <- write("221 Bye\r\n")
	}()
	acct := emailAccount{Server: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Username: "me@example.com", Password: "secret"}
	if err := sendSMTPWithRoots(acct, "you@example.com", "Hi", "Hello", roots); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSMTPMessageEncodesBodyAndRejectsHeaderInjection(t *testing.T) {
	message, err := smtpMessage("me@example.com", "you@example.com", "Hi", "Hello\nthere")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(message), "\r\n\r\n", 2)
	if len(parts) != 2 || !strings.Contains(parts[0], "Subject: Hi\r\n") {
		t.Fatalf("message = %q", message)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
	if err != nil || string(decoded) != "Hello\nthere" {
		t.Fatalf("body = %q, %v", decoded, err)
	}
	if _, err := smtpMessage("me@example.com", "you@example.com", "Hi\r\nBcc: x@example.com", "Hello"); err == nil {
		t.Fatal("accepted injected subject")
	}
	if _, err := smtpMessage("me@example.com", "you@example.com\r\nBcc: x@example.com", "Hi", "Hello"); err == nil {
		t.Fatal("accepted injected recipient")
	}
}

func TestSMTPRefusesServerWithoutTLS(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.Write([]byte("220 localhost ESMTP\r\n"))
		buf := make([]byte, 1024)
		conn.Read(buf)
		conn.Write([]byte("250 localhost\r\n"))
	}()
	acct := emailAccount{Server: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Username: "me@example.com", Password: "secret"}
	err = sendSMTP(acct, "you@example.com", "Hi", "Hello")
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("send error = %v", err)
	}
	<-done
}
