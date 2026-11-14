package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Native Claude stream-json events share the export's paging state. Tool IDs
// connect results to their calls even across pages and nested agent messages.
func (t *engineTranscript) writeClaude(raw json.RawMessage) error {
	var event struct {
		Type    string   `json:"type"`
		Session string   `json:"session_id"`
		Parent  string   `json:"parent_tool_use_id"`
		Result  string   `json:"result"`
		Subtype string   `json:"subtype"`
		IsError bool     `json:"is_error"`
		Errors  []string `json:"errors"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return failf("engine returned invalid Claude event")
	}
	if t.claudeTools == nil {
		t.claudeTools = map[string]string{}
	}
	if t.claudeText == nil {
		t.claudeText = map[string]string{}
	}
	context := event.Session + "/" + event.Parent
	prefix := ""
	if event.Parent != "" {
		prefix = "Nested agent (" + event.Parent + "): "
	}
	var out strings.Builder
	if event.Type == "result" {
		if event.IsError || strings.HasPrefix(event.Subtype, "error") {
			writeTranscriptBlock(&out, prefix+"Claude failure: "+event.Subtype+"\n"+event.Result+"\n"+strings.Join(event.Errors, "\n"))
		} else if event.Result != "" && strings.TrimSpace(event.Result) != strings.TrimSpace(t.claudeText[context]) {
			writeTranscriptQuote(&out, prefix+event.Result)
		}
	} else if len(event.Message.Content) > 0 {
		var blocks []map[string]any
		decoder := json.NewDecoder(bytes.NewReader(event.Message.Content))
		decoder.UseNumber()
		if err := decoder.Decode(&blocks); err != nil {
			var text string
			if json.Unmarshal(event.Message.Content, &text) != nil {
				return failf("engine returned invalid Claude content")
			}
			blocks = []map[string]any{{"type": "text", "text": text}}
		}
		var assistantText []string
		for _, block := range blocks {
			kind, _ := block["type"].(string)
			switch kind {
			case "text", "thinking":
				text, _ := block[kind].(string)
				if kind == "text" && event.Type == "assistant" {
					assistantText = append(assistantText, text)
				}
				if text != "" {
					writeTranscriptQuote(&out, prefix+text)
				}
			case "tool_use":
				id, _ := block["id"].(string)
				name, _ := block["name"].(string)
				t.claudeTools[event.Session+"/"+id] = name
				var input strings.Builder
				writeTranscriptValue(&input, block["input"], "")
				writeTranscriptBlock(&out, fmt.Sprintf("%sTool %s (%s)\n%s", prefix, name, id, limitToolText(input.String())))
			case "tool_result":
				id, _ := block["tool_use_id"].(string)
				key := event.Session + "/" + id
				name := t.claudeTools[key]
				if name == "" {
					name = "unknown"
				}
				delete(t.claudeTools, key)
				var result strings.Builder
				writeTranscriptValue(&result, block["content"], "")
				label := "result"
				if block["is_error"] == true {
					label = "error"
				}
				writeTranscriptBlock(&out, fmt.Sprintf("%sTool %s (%s) %s\n%s", prefix, name, id, label, limitToolText(result.String())))
			}
		}
		if len(assistantText) > 0 {
			t.claudeText[context] = strings.Join(assistantText, "\n")
		}
	}
	_, err := io.WriteString(t.dst, out.String())
	return err
}
