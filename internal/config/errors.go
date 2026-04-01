package config

import "errors"

var (
	ErrMissingName         = errors.New("definition: name is required")
	ErrMissingKind         = errors.New("definition: kind is required")
	ErrInvalidKind         = errors.New("definition: kind must be 'agent'")
	ErrMissingSystemPrompt = errors.New("definition: system_prompt is required")
)
