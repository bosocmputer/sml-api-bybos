package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDocsUIServesLocalSwaggerAssets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	NewDocsHandler().UI(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/docs/swagger-ui.css") || !strings.Contains(body, "/docs/swagger-ui-bundle.js") {
		t.Fatalf("docs UI must reference local Swagger UI assets: %s", body)
	}
	if strings.Contains(body, "cdn.jsdelivr.net") {
		t.Fatal("docs UI must not depend on CDN assets")
	}
}

func TestDocsSpecServesOpenAPIJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	NewDocsHandler().Spec(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	var spec map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatalf("spec response must be valid JSON: %v", err)
	}
	if got := spec["openapi"]; got != "3.0.3" {
		t.Fatalf("openapi = %v, want 3.0.3", got)
	}
}

func TestDocsAssetOnlyServesPinnedSwaggerFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewDocsHandler()
	r.GET("/docs/:asset", h.Asset)

	for _, path := range []string{"/docs/swagger-ui.css", "/docs/swagger-ui-bundle.js"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", path, w.Code, http.StatusOK)
		}
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/docs/unknown.js", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown asset status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestReadyNormalizesTenantName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	c.Request.Header.Set("X-Tenant", " SML1_2026 ")

	dbName := readyTenantName(c, "")
	if dbName != "sml1_2026" {
		t.Fatalf("ready tenant = %q, want sml1_2026", dbName)
	}
}
