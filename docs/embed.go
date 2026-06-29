package docs

import "embed"

// FS contains the OpenAPI source of truth and pinned Swagger UI assets.
//
//go:embed openapi.json swagger-ui.css swagger-ui-bundle.js
var FS embed.FS
