package handlers

import (
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"

	apidocs "sml-api-bybos/docs"
)

// swaggerUI is a small bootstrap page for official, locally embedded Swagger UI assets.
const swaggerUI = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>sml-api-bybos API Docs</title>
  <link rel="stylesheet" href="/docs/swagger-ui.css">
  <style>
    body { margin: 0; }
    #swagger-ui .topbar { background-color: #1a1a2e; }
  </style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="/docs/swagger-ui-bundle.js"></script>
  <script>
    window.ui = SwaggerUIBundle({
      url: "/docs/openapi.json",
      dom_id: "#swagger-ui",
      presets: [SwaggerUIBundle.presets.apis],
      layout: "BaseLayout",
      deepLinking: true,
      defaultModelsExpandDepth: 1,
      defaultModelExpandDepth: 1,
      validatorUrl: null
    });
  </script>
</body>
</html>`

// DocsHandler serves Swagger UI and the OpenAPI spec.
type DocsHandler struct{}

func NewDocsHandler() *DocsHandler { return &DocsHandler{} }

// UI serves the Swagger UI HTML page.
func (h *DocsHandler) UI(c *gin.Context) {
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(swaggerUI))
}

// Spec serves the OpenAPI JSON spec.
func (h *DocsHandler) Spec(c *gin.Context) {
	b, err := apidocs.FS.ReadFile("openapi.json")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": gin.H{"code": "openapi_spec_unavailable", "message": err.Error()}})
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", b)
}

// Asset serves pinned Swagger UI assets embedded from docs/.
func (h *DocsHandler) Asset(c *gin.Context) {
	name := filepath.Base(c.Param("asset"))
	if name != "swagger-ui.css" && name != "swagger-ui-bundle.js" {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": gin.H{"code": "asset_not_found", "message": "documentation asset not found"}})
		return
	}

	b, err := apidocs.FS.ReadFile(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": gin.H{"code": "asset_not_found", "message": "documentation asset not found"}})
		return
	}

	contentType := "application/octet-stream"
	switch filepath.Ext(name) {
	case ".css":
		contentType = "text/css; charset=utf-8"
	case ".js":
		contentType = "application/javascript; charset=utf-8"
	}
	c.Data(http.StatusOK, contentType, b)
}
