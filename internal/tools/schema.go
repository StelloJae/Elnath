package tools

import "encoding/json"

// Property describes a single JSON Schema property.
type Property struct {
	Type        string      `json:"type"`
	Description string      `json:"description,omitempty"`
	Enum        []string    `json:"enum,omitempty"`
	Default     interface{} `json:"default,omitempty"`
	Items       *itemsDef   `json:"items,omitempty"`
}

type itemsDef struct {
	Type string `json:"type"`
}

// String creates a string property.
func String(desc string) Property {
	return Property{Type: "string", Description: desc}
}

// StringEnum creates a string property restricted to the given values.
func StringEnum(desc string, values ...string) Property {
	return Property{Type: "string", Description: desc, Enum: values}
}

// Int creates an integer property.
func Int(desc string) Property {
	return Property{Type: "integer", Description: desc}
}

// Bool creates a boolean property.
func Bool(desc string) Property {
	return Property{Type: "boolean", Description: desc}
}

// Array creates an array property whose items have the given type.
func Array(desc string, itemType string) Property {
	return Property{
		Type:        "array",
		Description: desc,
		Items:       &itemsDef{Type: itemType},
	}
}

// Object builds a JSON Schema "object" type from the given properties and
// required field list. Returns a json.RawMessage ready for Tool.Schema().
func Object(properties map[string]Property, required []string) json.RawMessage {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	raw, _ := json.Marshal(schema)
	return raw
}
