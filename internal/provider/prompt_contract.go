package provider

import (
	"encoding/json"
	"strings"
)

func buildPromptWithJSONSchema(prompt string, schema map[string]any) string {
	encoded, _ := json.Marshal(schema)
	return strings.Join([]string{
		prompt,
		"",
		"When you finish, reply with only valid JSON.",
		"Do not wrap the JSON in markdown fences.",
		"Do not include any prose before or after the JSON.",
		"The JSON must match this schema exactly: " + string(encoded),
	}, "\n")
}
