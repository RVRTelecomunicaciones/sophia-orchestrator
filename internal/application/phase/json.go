package phase

import "encoding/json"

// jsonMarshal is a thin alias around encoding/json.Marshal; isolated so
// audit-payload serialization can be swapped without touching the service.
func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v) //nolint:wrapcheck
}
