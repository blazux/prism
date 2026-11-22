package ollama

import "encoding/json"

func (p ToolParameters) MarshalJSON() ([]byte, error) {
	if len(p.RawSchema) > 0 {
		return p.RawSchema, nil
	}
	type plain ToolParameters
	return json.Marshal(plain(p))
}
