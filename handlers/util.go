package handlers

import (
	"encoding/json"
	"net/http"
)

func decodeBody(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func marshalPayload(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func unmarshalPayload(s string, v any) error {
	return json.Unmarshal([]byte(s), v)
}
