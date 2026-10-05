package main

import (
	"cmp"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

const (
	emailTimeout         = 30 * time.Second
	emailListLimit       = 20
	emailMaxMessageBytes = 32 << 20
)

type emailConnection struct{ client *client.Client }

type emailRef struct {
	mailbox string
	uid     uint32
}

type emailSummary struct {
	ID             string    `json:"id"`
	From           string    `json:"from"`
	Subject        string    `json:"subject"`
	Date           time.Time `json:"date"`
	Flags          []string  `json:"flags"`
	Unread         bool      `json:"unread"`
	Size           uint32    `json:"size"`
	Preview        string    `json:"preview"`
	HasAttachments bool      `json:"has_attachments"`
}

type emailListResult struct {
	Account  string         `json:"account"`
	Folder   string         `json:"folder"`
	Total    uint32         `json:"total"`
	Messages []emailSummary `json:"messages"`
}

type emailReadResult struct {
	emailSummary
	Account     string            `json:"account"`
	To          string            `json:"to"`
	Cc          string            `json:"cc"`
	ReplyTo     string            `json:"reply_to"`
	MessageID   string            `json:"message_id"`
	Body        string            `json:"body"`
	Attachments []emailAttachment `json:"attachments"`
}

// emailServerAddress accepts DNS names and IPv4/IPv6, with an optional port.
// An explicit server port takes precedence over the configuration's port field.
func emailServerAddress(acct emailAccount) (string, error) {
	server := strings.TrimSpace(acct.Server)
	host, port := server, acct.Port
	switch {
	case strings.HasPrefix(server, "[") && strings.HasSuffix(server, "]"):
		host = strings.TrimSuffix(strings.TrimPrefix(server, "["), "]")
		if net.ParseIP(host) == nil {
			return "", fmt.Errorf("invalid bracketed IP address %q", server)
		}
	case net.ParseIP(server) != nil:
		// A bare IPv6 address carries no port.
	case strings.Contains(server, ":"):
		var portText string
		var err error
		host, portText, err = net.SplitHostPort(server)
		if err != nil {
			return "", fmt.Errorf("invalid IMAP server address %q", server)
		}
		port, err = strconv.Atoi(portText)
		if err != nil || port == 0 {
			return "", fmt.Errorf("invalid IMAP server port in %q", server)
		}
	}
	if host == "" || strings.ContainsAny(host, " /\\\t\r\n[]") {
		return "", fmt.Errorf("invalid IMAP server host %q", host)
	}
	if port == 0 {
		port = 993
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("IMAP server port must be between 1 and 65535")
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// dialEmailAccount verifies TLS and authenticates with bounded command timeouts.
func dialEmailAccount(acct emailAccount) (*emailConnection, error) {
	addr, err := emailServerAddress(acct)
	if err != nil {
		return nil, err
	}
	host, _, _ := net.SplitHostPort(addr)
	cli, err := client.DialWithDialerTLS(&net.Dialer{Timeout: emailTimeout}, addr, &tls.Config{
		ServerName: host, MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		return nil, fmt.Errorf("connecting to IMAP %s: %w", addr, err)
	}
	cli.Timeout = emailTimeout
	// Protocol diagnostics must not bypass the CLI's JSON errors or leak mail.
	cli.ErrorLog = log.New(io.Discard, "", 0)
	if err := cli.Login(acct.Username, acct.Password); err != nil {
		cli.Terminate()
		return nil, fmt.Errorf("IMAP login on %s failed: %w", addr, err)
	}
	return &emailConnection{client: cli}, nil
}

// close logs out without allowing a broken connection to delay process exit.
func (c *emailConnection) close() {
	c.client.Timeout = 2 * time.Second
	c.client.Logout()
	c.client.Terminate()
}

// parseEmailRef accepts folder:uid and the INBOX shorthand uid.
func parseEmailRef(value string) (emailRef, error) {
	value = strings.TrimSpace(value)
	folder, uidText := imap.InboxName, value
	if i := strings.LastIndexByte(value, ':'); i >= 0 {
		folder, uidText = strings.TrimSpace(value[:i]), strings.TrimSpace(value[i+1:])
	}
	uid, err := strconv.ParseUint(uidText, 10, 32)
	if err != nil || uid == 0 || folder == "" || strings.ContainsAny(folder, "\x00\r\n") {
		return emailRef{}, fmt.Errorf("invalid email ID %q: expected folder:uid or a positive INBOX uid", value)
	}
	return emailRef{mailbox: imap.CanonicalMailboxName(folder), uid: uint32(uid)}, nil
}

// String renders the ID returned by list and accepted by read.
func (r emailRef) String() string {
	return r.mailbox + ":" + strconv.FormatUint(uint64(r.uid), 10)
}

// fetch drains the message channel even on failure, avoiding blocked goroutines.
func (c *emailConnection) fetch(set *imap.SeqSet, items []imap.FetchItem) ([]*imap.Message, error) {
	messages := make(chan *imap.Message)
	done := make(chan error, 1)
	go func() { done <- c.client.UidFetch(set, items, messages) }()
	var result []*imap.Message
	for msg := range messages {
		result = append(result, msg)
	}
	return result, <-done
}

// emailMessageSummary translates IMAP metadata to the JSON response schema.
func emailMessageSummary(folder string, msg *imap.Message) emailSummary {
	sum := emailSummary{
		ID:   (emailRef{mailbox: folder, uid: msg.Uid}).String(),
		Date: msg.InternalDate, Size: msg.Size,
		Flags: append([]string{}, msg.Flags...), Unread: true,
	}
	for _, flag := range msg.Flags {
		if strings.EqualFold(flag, imap.SeenFlag) {
			sum.Unread = false
		}
	}
	if env := msg.Envelope; env != nil {
		sum.From = emailAddresses(env.From)
		sum.Subject = decodeEmailHeader(env.Subject)
		if !env.Date.IsZero() {
			sum.Date = env.Date
		}
	}
	if msg.BodyStructure != nil {
		msg.BodyStructure.Walk(func(_ []int, part *imap.BodyStructure) bool {
			if emailPartAttachment(part) {
				sum.HasAttachments = true
			}
			return true
		})
	}
	return sum
}

// list searches UIDs rather than sequence numbers, which shift during expunges.
func (c *emailConnection) list(account string) (emailListResult, error) {
	result := emailListResult{Account: account, Folder: imap.InboxName, Messages: []emailSummary{}}
	status, err := c.client.Select(imap.InboxName, true)
	if err != nil {
		return result, fmt.Errorf("opening INBOX: %w", err)
	}
	result.Total = status.Messages
	if status.Messages == 0 {
		return result, nil
	}
	uids, err := c.client.UidSearch(imap.NewSearchCriteria())
	if err != nil {
		return result, fmt.Errorf("searching INBOX: %w", err)
	}
	slices.Sort(uids)
	if len(uids) > emailListLimit {
		uids = uids[len(uids)-emailListLimit:]
	}
	if len(uids) == 0 {
		return result, nil
	}
	set := new(imap.SeqSet)
	set.AddNum(uids...)
	messages, err := c.fetch(set, []imap.FetchItem{
		imap.FetchUid, imap.FetchEnvelope, imap.FetchFlags,
		imap.FetchInternalDate, imap.FetchRFC822Size, imap.FetchBodyStructure,
	})
	if err != nil {
		return result, fmt.Errorf("listing email: %w", err)
	}
	slices.SortFunc(messages, func(a, b *imap.Message) int {
		return cmp.Compare(b.Uid, a.Uid)
	})
	for _, msg := range messages {
		sum := emailMessageSummary(imap.InboxName, msg)
		sum.Preview, err = c.preview(msg)
		if err != nil {
			return result, err
		}
		result.Messages = append(result.Messages, sum)
	}
	return result, nil
}

// preview fetches a bounded fragment of the preferred text part using PEEK.
func (c *emailConnection) preview(msg *imap.Message) (string, error) {
	var chosen *imap.BodyStructure
	var path []int
	if msg.BodyStructure == nil {
		return "", nil
	}
	msg.BodyStructure.Walk(func(p []int, part *imap.BodyStructure) bool {
		if emailPartAttachment(part) {
			return false
		}
		if strings.EqualFold(part.MIMEType, "text") &&
			(strings.EqualFold(part.MIMESubType, "plain") || strings.EqualFold(part.MIMESubType, "html")) {
			if chosen == nil || strings.EqualFold(part.MIMESubType, "plain") && !strings.EqualFold(chosen.MIMESubType, "plain") {
				chosen, path = part, append([]int{}, p...)
			}
		}
		return true
	})
	if chosen == nil {
		return "", nil
	}
	section := &imap.BodySectionName{Peek: true, Partial: []int{0, 4096}}
	section.Path = path
	if len(path) == 0 {
		section.Specifier = imap.TextSpecifier
	}
	set := new(imap.SeqSet)
	set.AddNum(msg.Uid)
	messages, err := c.fetch(set, []imap.FetchItem{section.FetchItem()})
	if err != nil {
		return "", fmt.Errorf("fetching email preview: %w", err)
	}
	for _, found := range messages {
		if body := found.GetBody(section); body != nil {
			// A partial encoded body may end inside a character or base64 group.
			text, _ := decodeEmailText(body, chosen.Encoding, chosen.Params["charset"])
			if strings.EqualFold(chosen.MIMESubType, "html") {
				text = emailHTMLText(text)
			}
			runes := []rune(emailOneLine(cleanEmailBody(text)))
			if len(runes) > 160 {
				return string(runes[:160]) + "...", nil
			}
			return string(runes), nil
		}
	}
	return "", nil
}

// read retrieves the entire message without PEEK, which sets the Seen flag.
func (c *emailConnection) read(account string, ref emailRef) (emailReadResult, error) {
	var result emailReadResult
	status, err := c.client.Select(ref.mailbox, false)
	if err != nil {
		return result, fmt.Errorf("opening email folder %q: %w", ref.mailbox, err)
	}
	if status.ReadOnly {
		return result, fmt.Errorf("email folder %q is read-only", ref.mailbox)
	}
	section := &imap.BodySectionName{}
	set := new(imap.SeqSet)
	set.AddNum(ref.uid)
	messages, err := c.fetch(set, []imap.FetchItem{
		imap.FetchUid, imap.FetchEnvelope, imap.FetchFlags,
		imap.FetchInternalDate, imap.FetchRFC822Size, section.FetchItem(),
	})
	if err != nil {
		return result, fmt.Errorf("reading email %s: %w", ref, err)
	}
	if len(messages) == 0 {
		return result, fmt.Errorf("email %s not found", ref)
	}
	msg := messages[0]
	body := msg.GetBody(section)
	if body == nil {
		return result, fmt.Errorf("email %s has no RFC822 body", ref)
	}
	if body.Len() > emailMaxMessageBytes {
		return result, fmt.Errorf("email %s exceeds the 32 MiB message limit", ref)
	}
	parsed, err := parseEmailMessage(body)
	if err != nil {
		return result, fmt.Errorf("decoding email %s: %w", ref, err)
	}
	result = emailReadResult{
		emailSummary: emailMessageSummary(ref.mailbox, msg), Account: account,
		To:        emailAddressesFromHeader(parsed.header, "To"),
		Cc:        emailAddressesFromHeader(parsed.header, "Cc"),
		ReplyTo:   emailAddressesFromHeader(parsed.header, "Reply-To"),
		MessageID: emailOneLine(parsed.header.Get("Message-ID")),
		Body:      cleanEmailBody(parsed.text), Attachments: parsed.attachments,
	}
	result.Unread = false
	if !slices.ContainsFunc(result.Flags, func(f string) bool { return strings.EqualFold(f, imap.SeenFlag) }) {
		result.Flags = append(result.Flags, imap.SeenFlag)
	}
	result.HasAttachments = len(parsed.attachments) > 0
	return result, nil
}
