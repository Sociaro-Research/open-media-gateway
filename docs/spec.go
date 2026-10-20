// Package docs embeds the API contract used by the console reference and clients.
package docs

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v2"
)

//go:embed openapi.yaml
var OpenAPI []byte

// JSON converts the single YAML source to browser-readable OpenAPI JSON.
func JSON() ([]byte, error) {
	var value any
	if err := yaml.UnmarshalStrict(OpenAPI, &value); err != nil {
		return nil, err
	}
	value, err := jsonValue(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func jsonValue(value any) (any, error) {
	switch v := value.(type) {
	case map[any]any:
		result := make(map[string]any, len(v))
		for key, child := range v {
			name, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("non-string OpenAPI property: %v", key)
			}
			converted, err := jsonValue(child)
			if err != nil {
				return nil, err
			}
			result[name] = converted
		}
		return result, nil
	case []any:
		for i, child := range v {
			converted, err := jsonValue(child)
			if err != nil {
				return nil, err
			}
			v[i] = converted
		}
	}
	return value, nil
}
