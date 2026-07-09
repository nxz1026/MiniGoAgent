package protocol

import "encoding/json"

// CanonicalizeSchema ensures parameters is a valid JSON Schema object.
// An empty or nil parameters gets a default "type: object, properties: {}" schema.
func CanonicalizeSchema(params json.RawMessage) json.RawMessage {
	if len(params) == 0 {
		return defaultSchema()
	}
	var v any
	if err := json.Unmarshal(params, &v); err != nil {
		return defaultSchema()
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return defaultSchema()
	}
	if obj["type"] == nil {
		obj["type"] = "object"
	}
	if obj["properties"] == nil {
		obj["properties"] = map[string]any{}
	}
	fixed, _ := json.Marshal(obj)
	return fixed
}

func defaultSchema() json.RawMessage {
	b, _ := json.Marshal(map[string]any{"type": "object", "properties": map[string]any{}})
	return b
}
