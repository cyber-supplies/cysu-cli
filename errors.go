package main

// errors.go defines how the CLI reports failures. The primary user is an LLM
// agent, so every failure is exactly one line of JSON on stderr.
//
// A backend error response is echoed verbatim (compacted to one line): the
// backend's {"error": "<message>"} is the error. The CLI composes its own
// line in the same shape only when there is nothing to echo (a non-JSON
// reply, a connection failure) or when the failure happened before or
// outside a backend call (bad arguments, missing token, a file that cannot
// be read). Keep those messages short and complete in themselves: they are
// paid for in context tokens, and the message is all the caller gets.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Exit statuses.
const (
	exitFailure = 1 // the command ran and failed
	exitUsage   = 2 // the invocation itself was wrong
)

// A cliError is a failure ready to be shown as the error line, plus the
// exit status it should end the process with. When raw is set it is a
// backend error body and is printed as is; otherwise the line is built from
// message.
type cliError struct {
	message string
	raw     json.RawMessage
	exit    int
}

func (e cliError) Error() string { return e.message }

// echoError wraps a backend's JSON error body so it is printed verbatim.
func echoError(raw []byte) error {
	return cliError{message: string(raw), raw: raw, exit: exitFailure}
}

// usageErrorf reports a mistake in how the CLI was invoked. The correct
// invocation is appended so the caller can fix it without another round
// trip through help.
func usageErrorf(usage, format string, a ...any) error {
	return cliError{message: fmt.Sprintf(format, a...) + " (usage: " + usage + ")", exit: exitUsage}
}

// failf reports a failure that changing the arguments cannot fix.
func failf(format string, a ...any) error {
	return cliError{message: fmt.Sprintf(format, a...), exit: exitFailure}
}

// asCLIError converts any error into a cliError. Errors raised without an
// explicit status are ordinary failures.
func asCLIError(err error) cliError {
	var e cliError
	if errors.As(err, &e) {
		return e
	}
	return cliError{message: err.Error(), exit: exitFailure}
}

// print writes e as a single line of JSON: the backend's body untouched
// apart from whitespace, or {"error": message}.
func (e cliError) print(w io.Writer) {
	if len(e.raw) > 0 {
		var line bytes.Buffer
		if json.Compact(&line, e.raw) == nil {
			line.WriteByte('\n')
			w.Write(line.Bytes())
			return
		}
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // keep <id> placeholders readable
	enc.Encode(struct {
		Error string `json:"error"`
	}{e.message})
}
