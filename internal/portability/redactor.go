package portability

import (
	"strings"

	"gopkg.in/yaml.v3"
)

func HasSecretAPIKeys(yamlContent []byte) bool {
	var doc map[string]any
	if err := yaml.Unmarshal(yamlContent, &doc); err != nil {
		return false
	}
	return hasSecretAPIKeys(doc)
}

func hasSecretAPIKeys(v any) bool {
	switch typed := v.(type) {
	case map[string]any:
		for key, value := range typed {
			if key == "api_key" {
				if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
					return true
				}
			}
			if hasSecretAPIKeys(value) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if hasSecretAPIKeys(item) {
				return true
			}
		}
	}
	return false
}
