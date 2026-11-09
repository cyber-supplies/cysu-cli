package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
)

// engineTranscript keeps only the latest version of unfinished items, avoiding
// duplicate tool calls as started/updated/completed events arrive across pages.
type engineTranscript struct {
	dst     io.Writer
	pending map[string]json.RawMessage
}

// write renders completed items and errors, holding unfinished items until the
// snapshot ends. Protocol lifecycle events have no place in the transcript.
func (t *engineTranscript) write(raw json.RawMessage) error {
	var event struct {
		Type string `json:"type"`
		Item struct {
			ID string `json:"id"`
		} `json:"item"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return err
	}
	if strings.HasPrefix(event.Type, "item.") && event.Item.ID != "" {
		if event.Type == "item.started" || event.Type == "item.updated" {
			t.pending[event.Item.ID] = raw
			return nil
		}
		delete(t.pending, event.Item.ID)
	}
	text, err := renderEngineEvent(raw)
	if err != nil {
		return err
	}
	_, err = io.WriteString(t.dst, text)
	return err
}

// flush includes tool calls still in progress when an active run was fetched.
func (t *engineTranscript) flush() error {
	items := make([]json.RawMessage, 0, len(t.pending))
	for _, raw := range t.pending {
		items = append(items, raw)
	}
	sequence := func(raw json.RawMessage) int64 {
		var event struct {
			Seq int64 `json:"seq"`
		}
		_ = json.Unmarshal(raw, &event)
		return event.Seq
	}
	sort.Slice(items, func(i, j int) bool { return sequence(items[i]) < sequence(items[j]) })
	for _, raw := range items {
		text, err := renderEngineEvent(raw)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(t.dst, text); err != nil {
			return err
		}
	}
	return nil
}

// renderEngineEvent includes agent content and tool details, without event
// numbers, timestamps, IDs, lifecycle status, usage, or engine bookkeeping.
func renderEngineEvent(raw json.RawMessage) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var event map[string]any
	if err := decoder.Decode(&event); err != nil {
		return "", failf("engine returned an invalid transcript event")
	}
	kind, ok := event["type"].(string)
	if !ok || kind == "" {
		return "", failf("engine returned an event without a type")
	}
	var out strings.Builder
	item, isItem := event["item"].(map[string]any)
	if isItem && strings.HasPrefix(kind, "item.") {
		itemKind, _ := item["type"].(string)
		switch itemKind {
		case "agent_message", "reasoning":
			var content strings.Builder
			if text, ok := item["text"].(string); ok {
				content.WriteString(text)
				if !strings.HasSuffix(text, "\n") {
					content.WriteByte('\n')
				}
			}
			writeTranscriptFields(&content, item, "", "type", "id", "status", "text", "usage", "time", "seq")
			writeTranscriptQuote(&out, content.String())
		default:
			var input strings.Builder
			if itemKind != "command_execution" && itemKind != "mcp_tool_call" && itemKind != "" {
				fmt.Fprintf(&input, "%s\n", transcriptLabel(itemKind))
			}
			call, output := map[string]any{}, map[string]any{}
			for key, value := range item {
				switch key {
				case "aggregated_output", "result", "output", "stdout", "stderr", "response", "error":
					output[key] = value
				case "exit_code":
				default:
					call[key] = value
				}
			}
			writeTranscriptFields(&input, call, "", "type", "id", "status", "usage", "time", "seq", "call_id", "thread_id")
			var content strings.Builder
			content.WriteString(limitToolText(strings.TrimSuffix(input.String(), "\n")))
			if len(output) > 0 {
				var result strings.Builder
				if len(output) == 1 {
					for _, value := range output {
						if text, ok := value.(string); ok {
							result.WriteString(text)
						} else {
							writeTranscriptValue(&result, value, "")
						}
					}
				} else {
					writeTranscriptFields(&result, output, "")
				}
				content.WriteString("\n\n")
				content.WriteString(limitToolText(strings.TrimSuffix(result.String(), "\n")))
			}
			if code, ok := item["exit_code"]; ok {
				fmt.Fprintf(&content, "\nExit code: %v", code)
			}
			writeTranscriptBlock(&out, content.String())
		}
	} else if kind == "error" || kind == "turn.failed" {
		var content strings.Builder
		writeTranscriptFields(&content, event, "", "seq", "time", "type", "id", "thread_id", "usage")
		writeTranscriptBlock(&out, content.String())
	} else {
		return "", nil
	}
	return out.String(), nil
}

// writeTranscriptQuote matches the reference's bold, quoted messages. Escape
// backslashes before asterisks so message content cannot close the bold wrapper,
// and prefix every line to keep embedded content within the quote.
func writeTranscriptQuote(out *strings.Builder, content string) {
	content = strings.ReplaceAll(content, "`", "'")
	content = strings.ReplaceAll(content, "\\", "\\\\")
	content = strings.ReplaceAll(content, "*", "\\*")
	content = strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
	for _, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			out.WriteString(">\n")
		} else {
			out.WriteString("> **" + line + "**\n")
		}
	}
	out.WriteByte('\n')
}

// writeTranscriptBlock matches the reference's single fence per tool call.
// Backticks become apostrophes so embedded content cannot close the fence.
func writeTranscriptBlock(out *strings.Builder, content string) {
	out.WriteString("```text\n")
	content = strings.ReplaceAll(content, "`", "'")
	out.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		out.WriteByte('\n')
	}
	out.WriteString("```\n\n")
}

// limitToolText caps a whole rendered tool input or output at 200 Unicode
// characters, including the marker, rather than giving each nested field a limit.
func limitToolText(text string) string {
	const limit = 200
	const marker = "… [truncated]"
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit-len([]rune(marker))]) + marker
	}
	return text
}

// transcriptLabel makes protocol field names readable without changing the
// actual message, command, argument, or output values.
func transcriptLabel(name string) string {
	switch name {
	case "mcp_tool_call":
		return "MCP tool call"
	case "aggregated_output":
		return "Output"
	case "arguments":
		return "Input"
	}
	name = strings.NewReplacer("_", " ", ".", " ").Replace(name)
	runes := []rune(name)
	if len(runes) > 0 {
		runes[0] = unicode.ToUpper(runes[0])
	}
	return string(runes)
}

// writeTranscriptFields orders common tool fields naturally, followed by any
// unfamiliar fields in alphabetical order. Maps and lists use indented text
// rather than JSON, while numbers retain their original precision.
func writeTranscriptFields(out *strings.Builder, fields map[string]any, indent string, omit ...string) {
	seen := map[string]bool{}
	for _, key := range omit {
		seen[key] = true
	}
	keys := []string{}
	for _, key := range []string{"id", "kind", "model", "server", "tool", "arguments", "command", "text", "aggregated_output", "result", "error", "status", "exit_code"} {
		if _, exists := fields[key]; exists && !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	other := []string{}
	for key := range fields {
		if !seen[key] {
			other = append(other, key)
		}
	}
	sort.Strings(other)
	keys = append(keys, other...)
	for _, key := range keys {
		fmt.Fprintf(out, "%s%s:", indent, transcriptLabel(key))
		writeTranscriptValue(out, fields[key], indent)
	}
}

// writeTranscriptValue expands nested tool data without truncating strings.
// Multi-line strings keep their line breaks, including command output and diffs.
func writeTranscriptValue(out *strings.Builder, value any, indent string) {
	switch value := value.(type) {
	case map[string]any:
		out.WriteByte('\n')
		if len(value) == 0 {
			fmt.Fprintf(out, "%s  (empty)\n", indent)
		}
		writeTranscriptFields(out, value, indent+"  ")
	case []any:
		out.WriteByte('\n')
		if len(value) == 0 {
			fmt.Fprintf(out, "%s  (empty)\n", indent)
		}
		for i, item := range value {
			fmt.Fprintf(out, "%s  %d:", indent, i+1)
			writeTranscriptValue(out, item, indent+"  ")
		}
	case string:
		if strings.ContainsAny(value, "\r\n") {
			out.WriteByte('\n')
			out.WriteString(value)
			if !strings.HasSuffix(value, "\n") {
				out.WriteByte('\n')
			}
		} else {
			fmt.Fprintf(out, " %s\n", value)
		}
	case nil:
		out.WriteString(" (none)\n")
	default:
		fmt.Fprintf(out, " %v\n", value)
	}
}
