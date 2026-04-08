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

func TestSchemaStringEnum(t *testing.T) {
	p := StringEnum("pick one", "a", "b", "c")
	if p.Type != "string" {
		t.Errorf("Type = %q, want %q", p.Type, "string")
	}
	if p.Description != "pick one" {
		t.Errorf("Description = %q, want %q", p.Description, "pick one")
	}
	if len(p.Enum) != 3 {
		t.Fatalf("Enum length = %d, want 3", len(p.Enum))
	}
	want := []string{"a", "b", "c"}
	for i, v := range want {
		if p.Enum[i] != v {
			t.Errorf("Enum[%d] = %q, want %q", i, p.Enum[i], v)
		}
	}
}

func TestSchemaBool(t *testing.T) {
	p := Bool("a flag")
	if p.Type != "boolean" {
		t.Errorf("Type = %q, want %q", p.Type, "boolean")
	}
	if p.Description != "a flag" {
		t.Errorf("Description = %q, want %q", p.Description, "a flag")
	}
}

func TestSchemaArray(t *testing.T) {
	p := Array("list of names", "string")
	if p.Type != "array" {
		t.Errorf("Type = %q, want %q", p.Type, "array")
	}
	if p.Description != "list of names" {
		t.Errorf("Description = %q, want %q", p.Description, "list of names")
	}
	if p.Items == nil {
		t.Fatal("Items is nil")
	}
	if p.Items.Type != "string" {
		t.Errorf("Items.Type = %q, want %q", p.Items.Type, "string")
	}
}
