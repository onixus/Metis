// Package request owns transport-specific request decoding without coupling the
// domain to HTTP or modifying generated OpenAPI adapters.
package request

import (
	"encoding/json"
	"errors"
	"fmt"

	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ReleaseEOLInput distinguishes an explicit null, which clears the support date,
// from an omitted required field, which must not mutate an existing release.
type ReleaseEOLInput struct {
	Eol *openapi_types.Date `json:"eol"`
}

// UnmarshalJSON enforces the required nullable field in the OpenAPI contract.
func (in *ReleaseEOLInput) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("release EOL request: %w", err)
	}
	value, present := fields["eol"]
	if !present {
		return errors.New("release EOL request: eol is required; use null explicitly to clear it")
	}
	var eol *openapi_types.Date
	if err := json.Unmarshal(value, &eol); err != nil {
		return fmt.Errorf("release EOL date: %w", err)
	}
	in.Eol = eol
	return nil
}
