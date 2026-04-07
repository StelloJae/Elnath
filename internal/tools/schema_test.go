package tools

import (
	"encoding/json"
	"testing"
)

func TestSchemaBuilder(t *testing.T) {
	t.Run("String property", func(t *testing.T) {
		p := String("a string field")
		if p.Type != "string" {
			t.Errorf("Type = %q, want %q", p.Type, "string")
		}
		if p.Description != "a string field" {
			t.Errorf("Description = %q, want %q", p.Description, "a string field")
		}
	})

	t.Run("Integer property", func(t *testing.T) {
		p := Int("an int field")
		if p.Type != "integer" {
			t.Errorf("Type = %q, want %q", p.Type, "integer")
		}
	})

	t.Run("Object produces valid JSON Schema", func(t *testing.T) {
		schema := Object(map[string]Property{
			"name": String("the name"),
			"age":  Int("the age"),
		}, []string{"name"})

		var parsed map[string]any
		if err := json.Unmarshal(schema, &parsed); err != nil {
			t.Fatalf("Object schema is not valid JSON: %v", err)
		}

		if parsed["type"] != "object" {
			t.Errorf("schema type = %v, want %q", parsed["type"], "object")
		}

		props, ok := parsed["properties"].(map[string]any)
		if !ok {
			t.Fatalf("schema missing 'properties' map")
		}
		if _, ok := props["name"]; !ok {
			t.Errorf("schema missing property 'name'")
		}
		if _, ok := props["age"]; !ok {
			t.Errorf("schema missing property 'age'")
		}

		required, ok := parsed["required"].([]any)
		if !ok || len(required) == 0 {
			t.Fatalf("schema missing or empty 'required' field")
		}
		if required[0] != "name" {
			t.Errorf("required[0] = %v, want %q", required[0], "name")
		}
	})

	t.Run("Object with no required fields omits required key", func(t *testing.T) {
		schema := Object(map[string]Property{
			"optional": String("optional field"),
		}, nil)

		var parsed map[string]any
		if err := json.Unmarshal(schema, &parsed); err != nil {
			t.Fatalf("Object schema is not valid JSON: %v", err)
		}
		if _, exists := parsed["required"]; exists {
			t.Errorf("schema should not have 'required' key when none specified")
		}
	})
}
