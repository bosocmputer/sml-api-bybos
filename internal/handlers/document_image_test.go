package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

type recordedDocumentImageExec struct {
	calls  []documentImageExecCall
	failAt int
}

type documentImageExecCall struct {
	sql  string
	args []any
}

func (e *recordedDocumentImageExec) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	e.calls = append(e.calls, documentImageExecCall{sql: sql, args: append([]any(nil), args...)})
	if e.failAt > 0 && len(e.calls) == e.failAt {
		return pgconn.CommandTag{}, errors.New("boom")
	}
	return pgconn.CommandTag{}, nil
}

func documentImageRequest(pageNo int, data []byte) documentImageRequestItem {
	sum := sha256.Sum256(data)
	return documentImageRequestItem{
		PageNo:      pageNo,
		ContentType: "image/jpeg",
		SHA256:      hex.EncodeToString(sum[:]),
		Data:        base64.StdEncoding.EncodeToString(data),
	}
}

func TestValidateDocumentImagesRequest(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x01}

	tests := []struct {
		name    string
		docNo   string
		req     replaceDocumentImagesRequest
		wantErr string
	}{
		{
			name:  "valid jpeg",
			docNo: "PO26060001",
			req:   replaceDocumentImagesRequest{Images: []documentImageRequestItem{documentImageRequest(1, jpeg)}},
		},
		{
			name:  "accepts jpeg data url",
			docNo: "PO26060001",
			req: replaceDocumentImagesRequest{Images: []documentImageRequestItem{{
				PageNo:      1,
				ContentType: "image/jpeg",
				SHA256:      documentImageRequest(1, jpeg).SHA256,
				Data:        "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpeg),
			}}},
		},
		{
			name:    "doc number too long for SML image_id",
			docNo:   strings.Repeat("A", documentImagesMaxDocNoLength+1),
			req:     replaceDocumentImagesRequest{Images: []documentImageRequestItem{documentImageRequest(1, jpeg)}},
			wantErr: "doc_no_too_long",
		},
		{
			name:    "requires images",
			docNo:   "PO26060001",
			req:     replaceDocumentImagesRequest{},
			wantErr: "document_images_required",
		},
		{
			name:  "rejects more than eight images",
			docNo: "PO26060001",
			req: replaceDocumentImagesRequest{Images: []documentImageRequestItem{
				documentImageRequest(1, jpeg), documentImageRequest(2, jpeg), documentImageRequest(3, jpeg),
				documentImageRequest(4, jpeg), documentImageRequest(5, jpeg), documentImageRequest(6, jpeg),
				documentImageRequest(7, jpeg), documentImageRequest(8, jpeg), documentImageRequest(9, jpeg),
			}},
			wantErr: "document_images_too_many",
		},
		{
			name:  "rejects duplicate page numbers",
			docNo: "PO26060001",
			req: replaceDocumentImagesRequest{Images: []documentImageRequestItem{
				documentImageRequest(1, jpeg),
				documentImageRequest(1, jpeg),
			}},
			wantErr: "document_image_page_duplicate",
		},
		{
			name:  "rejects non jpeg bytes",
			docNo: "PO26060001",
			req: replaceDocumentImagesRequest{Images: []documentImageRequestItem{
				documentImageRequest(1, []byte("not jpeg")),
			}},
			wantErr: "document_image_not_jpeg",
		},
		{
			name:  "rejects sha mismatch",
			docNo: "PO26060001",
			req: replaceDocumentImagesRequest{Images: []documentImageRequestItem{{
				PageNo:      1,
				ContentType: "image/jpeg",
				SHA256:      strings.Repeat("0", 64),
				Data:        base64.StdEncoding.EncodeToString(jpeg),
			}}},
			wantErr: "document_image_sha256_mismatch",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			images, err := validateDocumentImagesRequest(tc.docNo, tc.req)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(images) != len(tc.req.Images) {
					t.Fatalf("prepared images = %d, want %d", len(images), len(tc.req.Images))
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

func TestReplaceDocumentImagesWritesDeleteThenInsert(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00}
	images := []preparedDocumentImage{{
		PageNo: 1,
		GUID:   "guid-1",
		SHA256: "sha-1",
		Bytes:  jpeg,
	}}
	exec := &recordedDocumentImageExec{}

	if err := replaceDocumentImages(context.Background(), exec, "PO26060001", images, true); err != nil {
		t.Fatalf("replace images failed: %v", err)
	}
	if len(exec.calls) != 3 {
		t.Fatalf("calls = %d, want advisory lock + delete + insert", len(exec.calls))
	}
	if !strings.Contains(exec.calls[0].sql, "pg_advisory_xact_lock") {
		t.Fatalf("first call should acquire advisory lock, got %s", exec.calls[0].sql)
	}
	if !strings.Contains(exec.calls[1].sql, "DELETE FROM public.sml_doc_images") {
		t.Fatalf("second call should delete existing rows, got %s", exec.calls[1].sql)
	}
	if !strings.Contains(exec.calls[2].sql, "INSERT INTO public.sml_doc_images") {
		t.Fatalf("third call should insert image row, got %s", exec.calls[2].sql)
	}
	if got, ok := exec.calls[2].args[1].([]byte); !ok || string(got) != string(jpeg) {
		t.Fatalf("binary insert arg = %#v, want jpeg bytes", exec.calls[2].args[1])
	}
	if exec.calls[2].args[2] != documentImagesSystemID {
		t.Fatalf("system id = %v, want %s", exec.calls[2].args[2], documentImagesSystemID)
	}
	if exec.calls[2].args[4] != 1 {
		t.Fatalf("image_order = %v, want page number 1", exec.calls[2].args[4])
	}
}

func TestReplaceDocumentImagesMetadataOmitsBinaryPayload(t *testing.T) {
	exec := &recordedDocumentImageExec{}
	images := []preparedDocumentImage{{PageNo: 1, GUID: "guid-1", Bytes: []byte{0xff, 0xd8, 0xff}}}

	if err := replaceDocumentImages(context.Background(), exec, "PO26060001", images, false); err != nil {
		t.Fatalf("replace metadata failed: %v", err)
	}
	if len(exec.calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(exec.calls))
	}
	if exec.calls[2].args[1] != nil {
		t.Fatalf("metadata image_file arg = %#v, want nil", exec.calls[2].args[1])
	}
}

func TestReplaceDocumentImagesPropagatesWriteError(t *testing.T) {
	exec := &recordedDocumentImageExec{failAt: 2}
	err := replaceDocumentImages(context.Background(), exec, "PO26060001", []preparedDocumentImage{{PageNo: 1}}, true)
	if err == nil {
		t.Fatal("expected delete failure to propagate")
	}
}
