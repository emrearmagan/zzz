package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseMessageResponseStructured(t *testing.T) {
	body := []byte(`{
		"info": {
			"structured": {
				"success": true,
				"summary": "done",
				"changes": ["c1"],
				"learnings": ["l1"]
			},
			"tokens": {"input": 10, "output": 20, "cache": {"read": 3, "write": 4}}
		},
		"parts": [{"type": "text", "text": "hello"}]
	}`)

	parsed, err := parseMessageResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.output.Success || parsed.output.Summary != "done" {
		t.Fatalf("unexpected parsed output: %+v", parsed.output)
	}
	if parsed.usage.InputTokens != 10 || parsed.usage.OutputTokens != 20 || parsed.usage.CacheReadTokens != 3 || parsed.usage.CacheWriteTokens != 4 {
		t.Fatalf("unexpected parsed usage: %+v", parsed.usage)
	}
}

func TestExtractUsageFromEventMessageUpdated(t *testing.T) {
	usage := map[string]UsageTotals{}
	event, err := parseOpenCodeEvent(`{
		"payload": {
			"type": "message.updated",
			"properties": {
				"sessionID": "s1",
				"info": {
					"id": "m1",
					"role": "assistant",
					"tokens": {"input": 11, "output": 7, "cache": {"read": 2, "write": 1}}
				}
			}
		}
	}`)
	if err != nil {
		t.Fatal(err)
	}

	changed, totals := extractUsageFromEvent(event, usage)
	if !changed {
		t.Fatal("expected usage to change")
	}
	if totals.InputTokens != 11 || totals.OutputTokens != 7 || totals.CacheReadTokens != 2 || totals.CacheWriteTokens != 1 {
		t.Fatalf("unexpected totals: %+v", totals)
	}
}

func TestBuildOpenCodePromptContainsSchemaInstructions(t *testing.T) {
	p := buildOpenCodePrompt("do work")
	if !strings.Contains(p, "reply with only valid JSON") {
		t.Fatalf("missing JSON-only instruction: %s", p)
	}
	if !strings.Contains(p, "\"changes\"") || !strings.Contains(p, "\"learnings\"") {
		t.Fatalf("missing schema fields in prompt: %s", p)
	}
}

func TestBuildOpenCodeAskPromptIncludesNotesContext(t *testing.T) {
	p := buildOpenCodeAskPrompt("what changed?", AskContext{Prompt: "ship feature", Notes: []string{"n1", "n2"}})
	if !strings.Contains(p, "Current objective:") || !strings.Contains(p, "ship feature") {
		t.Fatalf("expected objective context in ask prompt: %s", p)
	}
	if !strings.Contains(p, "n1") || !strings.Contains(p, "n2") {
		t.Fatalf("expected notes context in ask prompt: %s", p)
	}
}

func TestParseMessageResponseFallsBackToTextWithoutStructuredOutput(t *testing.T) {
	body := []byte(`{
		"info": {},
		"parts": [{"type":"text","text":"plain text only"}]
	}`)
	parsed, err := parseMessageResponse(body)
	if err != nil {
		t.Fatalf("expected text fallback, got error: %v", err)
	}
	if parsed.output.Summary == "" {
		t.Fatalf("expected fallback summary, got %+v", parsed.output)
	}
}

func TestParseMessageResponseFailsOnStructuredOutputError(t *testing.T) {
	body := []byte(`{
		"info": {
			"error": {
				"name": "StructuredOutputError",
				"data": {"message": "Model did not produce structured output"}
			}
		},
		"parts": []
	}`)
	_, err := parseMessageResponse(body)
	if err == nil {
		t.Fatal("expected structured output error")
	}
	if !strings.Contains(err.Error(), "structured output error") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildMessagePayloadIncludesModelWhenProvided(t *testing.T) {
	payload := buildMessagePayload("do work", "opencode/minimax-m2.5-free")
	v, ok := payload["model"]
	if !ok {
		t.Fatal("expected model field in payload")
	}
	modelObj, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected model object, got %T", v)
	}
	if modelObj["providerID"] != "opencode" || modelObj["modelID"] != "minimax-m2.5-free" {
		t.Fatalf("unexpected model selector: %#v", modelObj)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"json_schema\"") {
		t.Fatalf("expected schema format in payload: %s", string(data))
	}
}

func TestBuildModelSelectorForBareModelID(t *testing.T) {
	m := buildModelSelector("gpt-5.3-codex")
	if m["modelID"] != "gpt-5.3-codex" {
		t.Fatalf("unexpected selector: %#v", m)
	}
	if _, ok := m["providerID"]; ok {
		t.Fatalf("did not expect providerID for bare model: %#v", m)
	}
}

func TestBuildMessagePayloadOmitsModelWhenEmpty(t *testing.T) {
	payload := buildMessagePayload("do work", "")
	if _, ok := payload["model"]; ok {
		t.Fatal("did not expect model field for empty model")
	}
}
