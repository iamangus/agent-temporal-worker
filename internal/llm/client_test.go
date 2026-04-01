package llm

import (
	"encoding/json"
	"testing"
)

func TestStripCodeFences(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "bare JSON object",
			input: `{"tasks":[]}`,
			want:  `{"tasks":[]}`,
		},
		{
			name:  "bare JSON array",
			input: `[1,2,3]`,
			want:  `[1,2,3]`,
		},
		{
			name:  "json code fence",
			input: "```json\n{\"tasks\":[]}\n```",
			want:  `{"tasks":[]}`,
		},
		{
			name:  "bare code fence without language",
			input: "```\n{\"tasks\":[]}\n```",
			want:  `{"tasks":[]}`,
		},
		{
			name:  "preamble text before JSON",
			input: "Here are my findings: {\"tasks\":[]}",
			want:  `{"tasks":[]}`,
		},
		{
			name:  "preamble with JSON keyword",
			input: "Here are my findings: JSON{\"tasks\":[]}",
			want:  `{"tasks":[]}`,
		},
		{
			name:  "preamble with code fence",
			input: "Here are my findings:\n```json\n{\"tasks\":[]}\n```",
			want:  `{"tasks":[]}`,
		},
		{
			name:  "leading whitespace",
			input: "  \n  {\"tasks\":[]}  \n  ",
			want:  `{"tasks":[]}`,
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace only",
			input: "   \n\t  ",
			want:  "",
		},
		{
			name:  "preamble with array",
			input: "The result is: [1, 2, 3]",
			want:  "[1, 2, 3]",
		},
		{
			name:  "nested braces in preamble",
			input: "Here is some {nested} and then {\"tasks\":[]}",
			want:  "{nested}",
		},
		{
			name:  "string with braces inside JSON",
			input: `{"reason": "used {curly braces}", "outcome": "ok"}`,
			want:  `{"reason": "used {curly braces}", "outcome": "ok"}`,
		},
		{
			name:  "escaped quotes inside JSON",
			input: `{"reason": "he said \"hello\"", "outcome": "ok"}`,
			want:  `{"reason": "he said \"hello\"", "outcome": "ok"}`,
		},
		{
			name:  "preamble with escaped quotes inside JSON",
			input: `Here is the result: {"reason": "he said \"hello\"", "outcome": "ok"}`,
			want:  `{"reason": "he said \"hello\"", "outcome": "ok"}`,
		},
		{
			name:  "trailing content after JSON object",
			input: `{"tasks":[]}<system-reminder>you are in build mode</system-reminder>`,
			want:  `{"tasks":[]}`,
		},
		{
			name:  "trailing content after JSON array",
			input: `[1,2,3]some trailing garbage`,
			want:  `[1,2,3]`,
		},
		{
			name:  "trailing content after code fence",
			input: "```json\n{\"tasks\":[]}\n```\n<system-reminder>noise</system-reminder>",
			want:  `{"tasks":[]}`,
		},
		{
			name:  "bare JSON with trailing newline and text",
			input: "{\"tasks\":[]}\n\nI hope this helps!",
			want:  `{"tasks":[]}`,
		},
		{
			name:  "plain text no JSON",
			input: "just some text with no json at all",
			want:  "just some text with no json at all",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripCodeFences(tt.input)
			if got != tt.want {
				t.Errorf("StripCodeFences() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsValidJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "valid object", input: `{"key": "value"}`, wantErr: false},
		{name: "valid array", input: `[1, 2, 3]`, wantErr: false},
		{name: "valid string", input: `"hello"`, wantErr: false},
		{name: "valid number", input: `42`, wantErr: false},
		{name: "empty object", input: `{}`, wantErr: false},
		{name: "invalid JSON", input: `{not json}`, wantErr: true},
		{name: "empty string", input: ``, wantErr: true},
		{name: "plain text", input: `hello world`, wantErr: true},
		{name: "trailing comma", input: `{"a": 1,}`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := IsValidJSON(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("IsValidJSON(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestValidateAgainstSchema(t *testing.T) {
	simpleSchema := json.RawMessage(`{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type": "object",
		"properties": {
			"reason": {"type": "string"},
			"outcome": {"type": "string"}
		},
		"required": ["reason", "outcome"],
		"additionalProperties": false
	}`)

	tasksSchema := json.RawMessage(`{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type": "object",
		"properties": {
			"tasks": {
				"type": "array",
				"items": {"type": "string"}
			}
		},
		"required": ["tasks"]
	}`)

	tests := []struct {
		name    string
		content string
		schema  json.RawMessage
		wantErr bool
	}{
		{
			name:    "valid against simple schema",
			content: `{"reason": "test", "outcome": "ok"}`,
			schema:  simpleSchema,
			wantErr: false,
		},
		{
			name:    "missing required field",
			content: `{"reason": "test"}`,
			schema:  simpleSchema,
			wantErr: true,
		},
		{
			name:    "wrong type for field",
			content: `{"reason": 123, "outcome": "ok"}`,
			schema:  simpleSchema,
			wantErr: true,
		},
		{
			name:    "extra properties rejected",
			content: `{"reason": "test", "outcome": "ok", "extra": true}`,
			schema:  simpleSchema,
			wantErr: true,
		},
		{
			name:    "valid tasks array with strings",
			content: `{"tasks": ["a", "b"]}`,
			schema:  tasksSchema,
			wantErr: false,
		},
		{
			name:    "tasks as objects instead of strings",
			content: `{"tasks": [{"id": "TASK-001", "title": "test"}]}`,
			schema:  tasksSchema,
			wantErr: true,
		},
		{
			name:    "invalid JSON content",
			content: `{not json}`,
			schema:  tasksSchema,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAgainstSchema(tt.content, tt.schema)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAgainstSchema() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
