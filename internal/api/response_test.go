package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestOKPageEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	OKPage(c, []string{"a"}, 10, 2, 1)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var got Response
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || got.Error != nil {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	meta, ok := got.Meta.(map[string]interface{})
	if !ok {
		t.Fatalf("meta type = %T", got.Meta)
	}
	if meta["total"].(float64) != 10 || meta["page"].(float64) != 2 || meta["size"].(float64) != 1 {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestErrorEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	Conflict(c, "duplicate_doc_no", "doc exists", map[string]string{"doc_no": "BF-1"})

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
	}
	var got Response
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Success || got.Error == nil {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	if got.Error.Code != "duplicate_doc_no" || got.Error.Message != "doc exists" {
		t.Fatalf("error = %+v", got.Error)
	}
}
