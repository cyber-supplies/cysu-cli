package main

// trace.go implements the CLI's debug tracing. With it on, every exchange
// with the gateway is reported on stderr as it happens, so a command that
// fails can be diagnosed from the request that caused it rather than from the
// error alone. It is switched on by CYSU_DEBUG or by --debug.
//
// Records are JSON, one compact object per line, in the shape errors take
// (see errors.go). Nothing is written to stdout, so a traced command prints
// exactly what an untraced one prints.
//
// Credentials are redacted on the way out, and there is no switch to turn
// that off. Tokens, passwords and session cookies pass through this CLI
// constantly, and a trace tends to end up in an agent's context or pasted
// into a bug report, so field names are matched loosely and the API token is
// scrubbed from every finished line.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"
)

// maxTraceBody caps how much of a body is traced. An endpoint listing or a
// base64-encoded stored file runs to megabytes, which the caller would then
// pay for in context tokens.
const maxTraceBody = 4096

// redactedMarker stands in for a value that was withheld.
const redactedMarker = "****"

// sensitiveFields are the substrings that mark a field name as holding a
// credential. See isSensitive for how they are matched.
var sensitiveFields = []string{
	"password", "passphrase", "cookie", "token",
	"secret", "auth", "credential", "key",
}

var (
	// traceOut is where records are written; a variable so tests can read them.
	traceOut io.Writer = os.Stderr

	// tracingEnabled is set from CYSU_DEBUG or --debug before a command runs.
	tracingEnabled bool

	// traceSeq pairs a response with the request it answers. The CLI makes one
	// request at a time, so a counter is enough.
	traceSeq int
)

// debugFlag switches tracing on for a single invocation. Every command
// accepts it, including the ones that never reach the backend.
var debugFlag = flagSpec{
	name:    "debug",
	boolean: true,
	usage:   "trace gateway requests on stderr (or set CYSU_DEBUG=1)",
}

// setTracing turns tracing on or off.
func setTracing(on bool) { tracingEnabled = on }

// tracingFromEnv reports whether CYSU_DEBUG asks for tracing. It is read
// before any argument is parsed, so a malformed command line is traceable.
func tracingFromEnv() bool { return truthy(os.Getenv("CYSU_DEBUG")) }

// truthy reads an environment variable as a switch. Any value that is not
// plainly negative turns it on, so 1, true and yes all work.
func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Records
// ---------------------------------------------------------------------------

// A traceRecord is one line of trace output. One type covers every kind of
// event, so most fields are empty most of the time, and a struct rather than
// a map keeps the keys in a fixed order. The failure detail is called
// "detail" rather than "error" because an agent tests for that key to decide
// whether a command failed; a trace record must never look like one.
type traceRecord struct {
	Debug       string            `json:"debug"`
	N           int               `json:"n,omitempty"`
	Method      string            `json:"method,omitempty"`
	URL         string            `json:"url,omitempty"`
	Status      int               `json:"status,omitempty"`
	DurationMS  *int64            `json:"duration_ms,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Bytes       int               `json:"bytes,omitempty"`
	Truncated   bool              `json:"truncated,omitempty"`
	Body        json.RawMessage   `json:"body,omitempty"`
	Detail      string            `json:"detail,omitempty"`
	Fields      map[string]any    `json:"fields,omitempty"`
}

// emit writes one record as a single line of JSON. Scrubbing the token from
// the finished line catches one that reached a field the rules below do not
// cover.
func emit(rec traceRecord) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // a traced body should read as it was sent
	if enc.Encode(rec) != nil {
		return
	}
	fmt.Fprintln(traceOut, scrubToken(strings.TrimRight(buf.String(), "\n")))
}

// ms renders a duration for a record. It is a pointer so that a genuinely
// sub-millisecond exchange reports 0 rather than dropping the field, while a
// record that has no duration leaves it out.
func ms(elapsed time.Duration) *int64 {
	v := elapsed.Milliseconds()
	return &v
}

// nextSeq numbers the next request.
func nextSeq() int {
	traceSeq++
	return traceSeq
}

// ---------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------

// traceRequest reports an outgoing backend request and returns the number to
// pass to traceResponse or traceFailure. With tracing off it emits nothing
// and returns 0.
func traceRequest(method, url string, header http.Header, body []byte) int {
	if !tracingEnabled {
		return 0
	}
	n := nextSeq()

	rec := traceRecord{
		Debug:   "request",
		N:       n,
		Method:  method,
		URL:     url,
		Headers: redactHeaders(header),
	}
	rec.Body, rec.Bytes, rec.Truncated = traceBody(body)

	emit(rec)
	return n
}

// traceTransfer reports a request to one of the presigned storage URLs the
// backend hands out. The query string is dropped, since that is where the
// signature lives. The contents are not traced: they are encrypted, often
// large, and may be binary.
func traceTransfer(method, rawURL string, size int64, contentType string) int {
	if !tracingEnabled {
		return 0
	}
	n := nextSeq()

	emit(traceRecord{
		Debug:       "transfer",
		N:           n,
		Method:      method,
		URL:         redactURL(rawURL),
		Bytes:       int(size),
		ContentType: contentType,
	})
	return n
}

// traceResponse reports the reply to the request numbered n. A nil body is
// traced without one, for exchanges whose contents are not traceable.
func traceResponse(n, status int, elapsed time.Duration, body []byte) {
	if !tracingEnabled {
		return
	}

	rec := traceRecord{
		Debug:      "response",
		N:          n,
		Status:     status,
		DurationMS: ms(elapsed),
	}
	rec.Body, rec.Bytes, rec.Truncated = traceBody(body)

	emit(rec)
}

// traceTransferResponse reports the reply to a transfer. The contents are not
// traced, so the size is taken from the response rather than from a body, and
// is left out when the host does not declare one.
func traceTransferResponse(n, status int, elapsed time.Duration, size int64) {
	if !tracingEnabled {
		return
	}

	rec := traceRecord{
		Debug:      "response",
		N:          n,
		Status:     status,
		DurationMS: ms(elapsed),
	}
	if size > 0 {
		rec.Bytes = int(size)
	}

	emit(rec)
}

// traceFailure reports that the request numbered n never completed.
func traceFailure(n int, elapsed time.Duration, err error) {
	if !tracingEnabled {
		return
	}
	emit(traceRecord{
		Debug:      "failure",
		N:          n,
		DurationMS: ms(elapsed),
		Detail:     err.Error(),
	})
}

// traceEvent reports something worth knowing that is not an HTTP exchange,
// such as where the API token came from or which key file was loaded. Fields
// are redacted by the same rules as a request body.
func traceEvent(event string, fields map[string]any) {
	if !tracingEnabled {
		return
	}
	emit(traceRecord{Debug: event, Fields: redactFields(fields)})
}

// ---------------------------------------------------------------------------
// Redaction
// ---------------------------------------------------------------------------

// traceBody prepares a body for tracing. JSON is redacted and embedded as
// JSON so it reads naturally; anything else becomes a JSON string. A body too
// long to embed is redacted first and cut afterwards. It returns the value to
// embed, the true size of the body, and whether it was cut.
func traceBody(raw []byte) (json.RawMessage, int, bool) {
	if len(raw) == 0 {
		return nil, 0, false
	}
	size := len(raw)

	if redacted, ok := redactJSON(raw); ok {
		if len(redacted) <= maxTraceBody {
			return redacted, size, false
		}
		// Carry the redacted form as a cut string rather than the original,
		// which has not been through the rules below.
		return jsonString(cut(string(redacted))), size, true
	}

	text := scrubToken(string(raw))
	if len(text) > maxTraceBody {
		return jsonString(cut(text)), size, true
	}
	return jsonString(text), size, false
}

// cut shortens s to maxTraceBody, dropping a partial rune at the end so the
// result is still valid UTF-8.
func cut(s string) string {
	if len(s) <= maxTraceBody {
		return s
	}
	return strings.ToValidUTF8(s[:maxTraceBody], "")
}

// redactJSON parses raw as JSON and returns it with every sensitive value
// replaced. The boolean reports whether raw was JSON at all.
func redactJSON(raw []byte) (json.RawMessage, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // keep ids and other numbers as they were written

	var value any
	if dec.Decode(&value) != nil {
		return nil, false
	}
	encoded, err := marshalJSON(redactValue(value))
	if err != nil {
		return nil, false
	}
	return encoded, true
}

// redactValue walks a decoded JSON value and replaces anything held under a
// sensitive key. A matching key hides its whole value, object or not, so a
// credential cannot escape inside a nested structure.
func redactValue(v any) any {
	switch value := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, item := range value {
			if isSensitive(key) {
				out[key] = redactedMarker
				continue
			}
			out[key] = redactValue(item)
		}
		return out

	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = redactValue(item)
		}
		return out

	case string:
		return scrubToken(value)

	default:
		return v
	}
}

// redactFields applies the body redaction rules to a set of event fields.
func redactFields(fields map[string]any) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	out, _ := redactValue(fields).(map[string]any)
	return out
}

// isSensitive reports whether a field name looks like it holds a credential.
// The name is lowercased and stripped of separators before being matched
// against sensitiveFields, so "access_token", "apiKey" and "Set-Cookie" are
// all caught. The match is broad on purpose: a field hidden unnecessarily is
// harmless here.
func isSensitive(name string) bool {
	normalised := strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || r == ' ' {
			return -1
		}
		return unicode.ToLower(r)
	}, name)

	for _, field := range sensitiveFields {
		if strings.Contains(normalised, field) {
			return true
		}
	}
	return false
}

// redactHeaders renders request headers for tracing. Authorization keeps its
// scheme and the last few characters of the credential, which is enough to
// tell two tokens apart. Any other header whose name looks sensitive is
// hidden completely.
func redactHeaders(header http.Header) map[string]string {
	if len(header) == 0 {
		return nil
	}
	out := make(map[string]string, len(header))

	for name, values := range header {
		value := strings.Join(values, ", ")
		switch {
		case strings.EqualFold(name, "Authorization"):
			out[name] = redactAuthorization(value)
		case isSensitive(name):
			out[name] = redactedMarker
		default:
			out[name] = value
		}
	}
	return out
}

// redactAuthorization hides the credential in an Authorization header while
// keeping its scheme, so "Bearer sk-live-abcd1234" becomes "Bearer ****1234".
func redactAuthorization(value string) string {
	scheme, credential, found := strings.Cut(strings.TrimSpace(value), " ")
	if !found {
		return redactTail(value)
	}
	return scheme + " " + redactTail(credential)
}

// redactTail replaces all but the last few characters of a secret. A short
// value is hidden entirely.
func redactTail(secret string) string {
	runes := []rune(strings.TrimSpace(secret))
	if len(runes) <= 4 {
		return redactedMarker
	}
	return redactedMarker + string(runes[len(runes)-4:])
}

// scrubToken removes the API token of this invocation from s. Short values
// are left alone: they are not tokens, and replacing them would corrupt
// unrelated text.
func scrubToken(s string) string {
	if len(apiToken) < 8 {
		return s
	}
	return strings.ReplaceAll(s, apiToken, redactedMarker)
}

// jsonString encodes s as a JSON string, without HTML escaping so a traced
// body reads as it was sent.
func jsonString(s string) json.RawMessage {
	encoded, err := marshalJSON(s)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return encoded
}

// marshalJSON encodes values without HTML escaping so traced request bodies
// remain legible while retaining the redaction applied before this call.
func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
