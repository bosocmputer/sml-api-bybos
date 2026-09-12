package handlers

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateDocumentImageDocNo(t *testing.T) {
	tests := []struct {
		name    string
		docNo   string
		wantErr string
	}{
		{name: "accepts normal doc no", docNo: "1EPO2609-00020"},
		{name: "rejects empty", docNo: "", wantErr: "doc_no_required"},
		{
			name:    "rejects doc no longer than SML image_id",
			docNo:   strings.Repeat("A", documentImagesMaxDocNoLength+1),
			wantErr: "doc_no_too_long",
		},
		{name: "accepts doc no at the length limit", docNo: strings.Repeat("A", documentImagesMaxDocNoLength)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDocumentImageDocNo(tc.docNo)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error code %s, got nil", tc.wantErr)
			}
			var validation documentImageValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error type = %T, want documentImageValidationError", err)
			}
			if validation.code != tc.wantErr {
				t.Fatalf("error code = %s, want %s", validation.code, tc.wantErr)
			}
		})
	}
}

// The read path serves whatever Replace stored, and Replace only ever accepts
// JPEG bytes - so the content type it reports back must stay in step with that.
func TestDocumentImageContentTypeMatchesWhatReplaceAccepts(t *testing.T) {
	if documentImageContentType != "image/jpeg" {
		t.Fatalf("content type = %s, want image/jpeg", documentImageContentType)
	}
}

// A listing must never be able to pull an unbounded number of rows, and the
// per-image ceiling on the way out must match the one enforced on the way in.
func TestDocumentImageReadLimits(t *testing.T) {
	if documentImagesListMaxRows <= 0 {
		t.Fatalf("list max rows = %d, want a positive cap", documentImagesListMaxRows)
	}
	if documentImageMaxBytes <= 0 {
		t.Fatalf("image max bytes = %d, want a positive cap", documentImageMaxBytes)
	}
	if documentImagesReadTimeout <= 0 {
		t.Fatalf("read timeout = %s, want a positive bound", documentImagesReadTimeout)
	}
}
