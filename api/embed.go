// Package api содержит спецификацию OpenAPI как встроенный ресурс (NF-M04).
package api

import _ "embed"

// OpenAPI — исходный текст api/openapi.yaml.
//
//go:embed openapi.yaml
var OpenAPI []byte
