package brain2

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nexus-chat/nexus/internal/brain"
)

// ValidationError contains structured error information that helps the model self-correct.
type ValidationError struct {
	Tool     string `json:"tool"`
	Error    string `json:"error"`
	Schema   any    `json:"schema,omitempty"`
	Received any    `json:"received,omitempty"`
	Hint     string `json:"hint"`
}

func (e *ValidationError) String() string {
	b, _ := json.Marshal(e)
	return string(b)
}

// ValidateToolCall checks a tool call's arguments against the tool's schema.
// Returns nil if valid, or a structured ValidationError for the model to self-correct.
func ValidateToolCall(call brain.ToolCall, tools []brain.ToolDef) *ValidationError {
	// Find the tool definition
	var toolDef *brain.ToolDef
	for i := range tools {
		if tools[i].Function.Name == call.Function.Name {
			toolDef = &tools[i]
			break
		}
	}

	if toolDef == nil {
		return &ValidationError{
			Tool:  call.Function.Name,
			Error: "unknown tool",
			Hint:  fmt.Sprintf("Tool '%s' does not exist. Available tools: %s", call.Function.Name, toolNames(tools)),
		}
	}

	// Parse the arguments
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return &ValidationError{
			Tool:     call.Function.Name,
			Error:    "invalid JSON arguments",
			Received: call.Function.Arguments,
			Hint:     "Arguments must be a valid JSON object. Check for syntax errors.",
		}
	}

	// Parse the parameter schema
	var schema struct {
		Type       string                       `json:"type"`
		Properties map[string]map[string]any     `json:"properties"`
		Required   []string                      `json:"required"`
	}
	if err := json.Unmarshal(toolDef.Function.Parameters, &schema); err != nil {
		return nil // can't parse schema, skip validation
	}

	// Check required arguments
	var missing []string
	for _, req := range schema.Required {
		if _, ok := args[req]; !ok {
			missing = append(missing, req)
		}
	}
	if len(missing) > 0 {
		return &ValidationError{
			Tool:     call.Function.Name,
			Error:    fmt.Sprintf("missing required arguments: %s", strings.Join(missing, ", ")),
			Schema:   schema,
			Received: args,
			Hint:     fmt.Sprintf("Please provide the following arguments: %s", strings.Join(missing, ", ")),
		}
	}

	// Type-check arguments against schema
	for argName, argVal := range args {
		propSchema, exists := schema.Properties[argName]
		if !exists {
			continue // extra args are ok
		}
		expectedType, _ := propSchema["type"].(string)
		if expectedType == "" {
			continue
		}
		if !typeMatches(argVal, expectedType) {
			return &ValidationError{
				Tool:     call.Function.Name,
				Error:    fmt.Sprintf("argument '%s' has wrong type: expected %s", argName, expectedType),
				Schema:   propSchema,
				Received: map[string]any{argName: argVal},
				Hint:     fmt.Sprintf("Argument '%s' should be of type '%s'.", argName, expectedType),
			}
		}

		// Check enum values
		if enumRaw, ok := propSchema["enum"]; ok {
			if enumSlice, ok := enumRaw.([]any); ok {
				strVal, isStr := argVal.(string)
				if isStr {
					found := false
					var allowed []string
					for _, e := range enumSlice {
						s, _ := e.(string)
						allowed = append(allowed, s)
						if s == strVal {
							found = true
						}
					}
					if !found {
						return &ValidationError{
							Tool:     call.Function.Name,
							Error:    fmt.Sprintf("argument '%s' has invalid value '%s'", argName, strVal),
							Schema:   propSchema,
							Received: map[string]any{argName: argVal},
							Hint:     fmt.Sprintf("Allowed values for '%s': %s", argName, strings.Join(allowed, ", ")),
						}
					}
				}
			}
		}
	}

	return nil // valid
}

func typeMatches(val any, expected string) bool {
	switch expected {
	case "string":
		_, ok := val.(string)
		return ok
	case "number", "integer":
		_, ok := val.(float64)
		return ok
	case "boolean":
		_, ok := val.(bool)
		return ok
	case "array":
		_, ok := val.([]any)
		return ok
	case "object":
		_, ok := val.(map[string]any)
		return ok
	}
	return true // unknown type, accept
}

func toolNames(tools []brain.ToolDef) string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Function.Name
	}
	return strings.Join(names, ", ")
}
