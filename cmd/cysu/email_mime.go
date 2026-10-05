package main

// email_mime.go decodes displayable MIME parts, preserving links in HTML mail
// and describing attachments without returning their payloads.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"regexp"
	"strings"
	"unicode"

	"github.com/emersion/go-imap"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const (
	emailMaxTextBytes      = 1 << 20
	emailMaxTotalTextBytes = 4 << 20
	emailMaxMIMEDepth      = 20
)

type emailAttachment struct {
	Filename string `json:"filename"`
	MIMEType string `json:"mime_type"`
	Size     int64  `json:"size"` // encoded MIME part bytes
	Part     string `json:"part"`
}

type parsedEmail struct {
	header      mail.Header
	text        string
	plain       bool
	attachments []emailAttachment
}

// parseEmailMessage bounds the raw input before traversing its MIME structure.
func parseEmailMessage(r io.Reader) (parsedEmail, error) {
	raw, err := io.ReadAll(io.LimitReader(r, emailMaxMessageBytes+1))
	if err != nil {
		return parsedEmail{}, err
	}
	if len(raw) > emailMaxMessageBytes {
		return parsedEmail{}, fmt.Errorf("message exceeds the 32 MiB limit")
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return parsedEmail{}, err
	}
	parsed, err := collectEmailPart(textproto.MIMEHeader(msg.Header), msg.Body, "1", 0)
	parsed.header = msg.Header
	return parsed, err
}

// collectEmailPart prefers plain text inside alternatives, while retaining
// separate text parts in mixed messages and forwarding notes.
func collectEmailPart(hdr textproto.MIMEHeader, r io.Reader, path string, depth int) (parsedEmail, error) {
	result := parsedEmail{attachments: []emailAttachment{}}
	if depth > emailMaxMIMEDepth {
		return result, fmt.Errorf("MIME nesting exceeds %d levels", emailMaxMIMEDepth)
	}
	mediaType, params := "text/plain", map[string]string{}
	if value := hdr.Get("Content-Type"); value != "" {
		var err error
		mediaType, params, err = mime.ParseMediaType(value)
		if err != nil {
			return result, fmt.Errorf("invalid Content-Type in part %s: %w", path, err)
		}
	}
	disposition, dispParams, _ := mime.ParseMediaType(hdr.Get("Content-Disposition"))
	filename := dispParams["filename"]
	if filename == "" {
		filename = params["name"]
	}
	if strings.EqualFold(disposition, "attachment") || filename != "" ||
		!strings.HasPrefix(mediaType, "text/") && !strings.HasPrefix(mediaType, "multipart/") && mediaType != "message/rfc822" {
		size, err := io.Copy(io.Discard, r)
		result.attachments = append(result.attachments, emailAttachment{
			Filename: decodeEmailHeader(filename), MIMEType: mediaType, Size: size, Part: path,
		})
		return result, err
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		if params["boundary"] == "" {
			return result, fmt.Errorf("multipart email part %s has no boundary", path)
		}
		parts := multipart.NewReader(r, params["boundary"])
		for index := 1; ; index++ {
			part, err := parts.NextRawPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return result, fmt.Errorf("reading MIME part %s: %w", path, err)
			}
			child, err := collectEmailPart(part.Header, part, fmt.Sprintf("%s.%d", path, index), depth+1)
			part.Close()
			if err != nil {
				return result, err
			}
			result.attachments = append(result.attachments, child.attachments...)
			if mediaType == "multipart/alternative" {
				if child.text != "" && (result.text == "" || child.plain && !result.plain) {
					result.text, result.plain = child.text, child.plain
				}
			} else if child.text != "" {
				if result.text != "" {
					result.text += "\n\n"
				}
				result.text += child.text
				result.plain = result.plain || child.plain
			}
			if len(result.text) > emailMaxTotalTextBytes {
				return result, fmt.Errorf("email text exceeds the 4 MiB limit")
			}
		}
		return result, nil
	}
	if mediaType == "message/rfc822" {
		inner, err := mail.ReadMessage(emailTransferReader(r, hdr.Get("Content-Transfer-Encoding")))
		if err != nil {
			return result, fmt.Errorf("reading forwarded email: %w", err)
		}
		return collectEmailPart(textproto.MIMEHeader(inner.Header), inner.Body, path+".rfc822", depth+1)
	}
	text, err := decodeEmailText(r, hdr.Get("Content-Transfer-Encoding"), params["charset"])
	if err != nil {
		return result, fmt.Errorf("decoding MIME part %s: %w", path, err)
	}
	if mediaType == "text/html" {
		text = emailHTMLText(text)
	}
	result.text = strings.TrimSpace(text)
	result.plain = mediaType == "text/plain"
	return result, nil
}

// emailTransferReader handles MIME transfer encodings without changing headers.
func emailTransferReader(r io.Reader, encoding string) io.Reader {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, r)
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	default:
		return r
	}
}

// decodeEmailText decodes the charset after transfer decoding, bounding UTF-8 size.
// It returns partial text alongside errors so a clipped preview remains useful.
func decodeEmailText(r io.Reader, encoding, charsetName string) (string, error) {
	r = emailTransferReader(r, encoding)
	if charsetName != "" && !strings.EqualFold(charsetName, "utf-8") && !strings.EqualFold(charsetName, "us-ascii") {
		var err error
		r, err = charset.NewReaderLabel(charsetName, r)
		if err != nil {
			return "", err
		}
	}
	raw, err := io.ReadAll(io.LimitReader(r, emailMaxTextBytes+1))
	if len(raw) > emailMaxTextBytes {
		return "", fmt.Errorf("text part exceeds the 1 MiB limit")
	}
	return strings.ToValidUTF8(string(raw), "\uFFFD"), err
}

// emailPartAttachment matches named or explicitly attached BODYSTRUCTURE parts.
func emailPartAttachment(part *imap.BodyStructure) bool {
	if strings.EqualFold(part.Disposition, "attachment") || part.DispositionParams["filename"] != "" || part.Params["name"] != "" {
		return true
	}
	return !strings.EqualFold(part.MIMEType, "text") && !strings.EqualFold(part.MIMEType, "multipart") &&
		!(strings.EqualFold(part.MIMEType, "message") && strings.EqualFold(part.MIMESubType, "rfc822"))
}

// decodeEmailHeader understands encoded words, including non-UTF-8 charsets.
func decodeEmailHeader(value string) string {
	decoder := mime.WordDecoder{CharsetReader: charset.NewReaderLabel}
	decoded, err := decoder.DecodeHeader(value)
	if err != nil {
		decoded = value
	}
	return emailOneLine(decoded)
}

// emailOneLine normalizes headers and previews while dropping control characters.
func emailOneLine(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

// emailAddresses renders IMAP envelope addresses in readable form.
func emailAddresses(addresses []*imap.Address) string {
	var values []string
	for _, addr := range addresses {
		if addr == nil || addr.HostName == "" || addr.MailboxName == "" {
			continue
		}
		value := addr.MailboxName + "@" + addr.HostName
		if name := decodeEmailHeader(addr.PersonalName); name != "" {
			value = name + " <" + value + ">"
		}
		values = append(values, value)
	}
	return emailOneLine(strings.Join(values, ", "))
}

// emailAddressesFromHeader renders To, Cc and Reply-To, tolerating bad addresses.
func emailAddressesFromHeader(hdr mail.Header, name string) string {
	value := decodeEmailHeader(hdr.Get(name))
	addresses, err := mail.ParseAddressList(value)
	if err != nil {
		return value
	}
	var values []string
	for _, addr := range addresses {
		value := addr.Address
		if addr.Name != "" {
			value = addr.Name + " <" + value + ">"
		}
		values = append(values, value)
	}
	return emailOneLine(strings.Join(values, ", "))
}

// emailHTMLText hides non-display elements and retains anchor destinations.
func emailHTMLText(value string) string {
	doc, err := html.Parse(strings.NewReader(value))
	if err != nil {
		return ""
	}
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		block := false
		if n.Type == html.ElementNode {
			switch n.Data {
			case "head", "script", "style", "noscript", "svg":
				return
			case "p", "div", "br", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre", "section":
				block = true
				text.WriteByte('\n')
			}
		} else if n.Type == html.TextNode {
			text.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key == "href" && strings.TrimSpace(attr.Val) != "" {
					text.WriteString(" (" + attr.Val + ")")
				}
			}
		}
		if block {
			text.WriteByte('\n')
		}
	}
	walk(doc)
	var lines []string
	for _, line := range strings.Split(text.String(), "\n") {
		line = emailOneLine(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

var emailReplyBanner = regexp.MustCompile(`(?i)^(on .{3,200}wrote:|-{2,}\s*original message\s*-{2,})$`)

// cleanEmailBody strips the signature and trailing quoted reply chain, matching
// checkemail's default focus on newly written text.
func cleanEmailBody(value string) string {
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(value, "\n")
	cut := len(lines)
	for i, line := range lines {
		if line == "-- " || line == "--" || emailReplyBanner.MatchString(strings.TrimSpace(line)) {
			cut = i
			break
		}
		if strings.HasPrefix(strings.TrimSpace(line), ">") &&
			(i == 0 || strings.TrimSpace(lines[i-1]) == "" || i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), ">")) {
			cut = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[:cut], "\n"))
}
