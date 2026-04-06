package provider

import "strings"

func buildMessagePayload(prompt, model string) map[string]any {
	payload := map[string]any{
		"role":  "user",
		"parts": []map[string]string{{"type": "text", "text": prompt}},
		"format": map[string]any{
			"type":       "json_schema",
			"retryCount": 1,
			"schema":     opencodeOutputSchema(),
		},
	}
	if strings.TrimSpace(model) != "" {
		payload["model"] = buildModelSelector(model)
	}
	return payload
}

func buildModelSelector(model string) map[string]any {
	clean := strings.TrimSpace(model)
	parts := strings.SplitN(clean, "/", 2)
	if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" {
		return map[string]any{
			"providerID": strings.TrimSpace(parts[0]),
			"modelID":    strings.TrimSpace(parts[1]),
		}
	}
	return map[string]any{"modelID": clean}
}

func buildOpenCodePrompt(prompt string) string {
	return buildPromptWithJSONSchema(prompt, opencodeOutputSchema())
}

func opencodeOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"success", "summary", "changes", "learnings"},
		"properties": map[string]any{
			"success":   map[string]any{"type": "boolean"},
			"summary":   map[string]any{"type": "string"},
			"changes":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"learnings": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
}
