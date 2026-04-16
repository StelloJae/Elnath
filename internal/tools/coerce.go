package tools

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// ArgTargetProvider is implemented by tools that want automatic argument coercion.
// The returned pointer should be a zero-valued instance of the tool's params struct.
type ArgTargetProvider interface {
	ArgsTarget() any
}

// CoerceToolArgs inspects the target struct's JSON tags and coerces common
// type mismatches in params before unmarshaling. Unsupported or invalid
// coercions leave the original field unchanged.
func CoerceToolArgs(params json.RawMessage, target any) json.RawMessage {
	if len(params) == 0 || target == nil {
		return params
	}

	typ := reflect.TypeOf(target)
	if typ.Kind() != reflect.Ptr || typ.Elem().Kind() != reflect.Struct {
		return params
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil {
		return params
	}

	changed := false
	for i := 0; i < typ.Elem().NumField(); i++ {
		field := typ.Elem().Field(i)
		if field.PkgPath != "" {
			continue
		}
		name, ok := jsonFieldName(field)
		if !ok {
			continue
		}
		raw, ok := fields[name]
		if !ok {
			continue
		}
		coerced, fieldChanged := coerceFieldRaw(raw, field.Type)
		if !fieldChanged {
			continue
		}
		fields[name] = coerced
		changed = true
	}

	if !changed {
		return params
	}

	encoded, err := json.Marshal(fields)
	if err != nil {
		return params
	}
	return encoded
}

func jsonFieldName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", false
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		name = field.Name
	}
	return name, true
}

func coerceFieldRaw(raw json.RawMessage, typ reflect.Type) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw, false
	}

	kind := typ.Kind()
	switch {
	case trimmed[0] == '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return raw, false
		}
		s = strings.TrimSpace(s)
		switch kind {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			value, err := strconv.ParseInt(s, 10, typ.Bits())
			if err != nil {
				return raw, false
			}
			return marshalInt(raw, typ, value)
		case reflect.Bool:
			value, err := strconv.ParseBool(s)
			if err != nil {
				return raw, false
			}
			return mustMarshalRaw(raw, value), true
		case reflect.Float32, reflect.Float64:
			value, err := strconv.ParseFloat(s, typ.Bits())
			if err != nil {
				return raw, false
			}
			return marshalFloat(raw, typ, value)
		}

	case isBoolLiteral(trimmed):
		if kind == reflect.String {
			return mustMarshalRaw(raw, string(trimmed)), true
		}

	case isNumberLiteral(trimmed):
		switch kind {
		case reflect.String:
			return mustMarshalRaw(raw, string(trimmed)), true
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if !bytes.ContainsAny(trimmed, ".eE") {
				return raw, false
			}
			value, err := strconv.ParseFloat(string(trimmed), 64)
			if err != nil || math.Trunc(value) != value {
				return raw, false
			}
			if !fitsSignedInt(typ, value) {
				return raw, false
			}
			return marshalInt(raw, typ, int64(value))
		}
	}

	return raw, false
}

func isBoolLiteral(raw []byte) bool {
	return bytes.Equal(raw, []byte("true")) || bytes.Equal(raw, []byte("false"))
}

func isNumberLiteral(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	first := raw[0]
	return first == '-' || (first >= '0' && first <= '9')
}

func fitsSignedInt(typ reflect.Type, value float64) bool {
	bits := typ.Bits()
	min := -math.Pow(2, float64(bits-1))
	max := math.Pow(2, float64(bits-1)) - 1
	return value >= min && value <= max
}

func marshalInt(fallback json.RawMessage, typ reflect.Type, value int64) (json.RawMessage, bool) {
	v := reflect.New(typ).Elem()
	v.SetInt(value)
	return mustMarshalRaw(fallback, v.Interface()), true
}

func marshalFloat(fallback json.RawMessage, typ reflect.Type, value float64) (json.RawMessage, bool) {
	v := reflect.New(typ).Elem()
	v.SetFloat(value)
	return mustMarshalRaw(fallback, v.Interface()), true
}

func mustMarshalRaw(fallback json.RawMessage, value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fallback
	}
	return encoded
}
