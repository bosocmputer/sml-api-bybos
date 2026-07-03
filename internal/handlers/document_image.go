package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/middleware"
)

const (
	documentImagesMaxCount        = 8
	documentImagesMaxDocNoLength  = 50
	documentImagesMaxPayloadBytes = 24 << 20
	documentImageMaxBytes         = 4 << 20
	documentImagesSystemID        = "PAPERLESS"
)

type DocumentImageHandler struct {
	dbm *db.Manager
}

func NewDocumentImageHandler(dbm *db.Manager) *DocumentImageHandler {
	return &DocumentImageHandler{dbm: dbm}
}

type replaceDocumentImagesRequest struct {
	Images     []documentImageRequestItem `json:"images"`
	TotalPages int                        `json:"totalPages,omitempty"`
	Truncated  bool                       `json:"truncated,omitempty"`
}

type documentImageRequestItem struct {
	PageNo      int    `json:"pageNo"`
	ContentType string `json:"contentType,omitempty"`
	SHA256      string `json:"sha256"`
	Data        string `json:"data"`
}

type preparedDocumentImage struct {
	PageNo int
	GUID   string
	SHA256 string
	Bytes  []byte
}

type replaceDocumentImagesResult struct {
	DocNo        string                    `json:"doc_no"`
	ImageCount   int                       `json:"image_count"`
	TotalPages   int                       `json:"total_pages,omitempty"`
	Truncated    bool                      `json:"truncated"`
	TotalBytes   int                       `json:"total_bytes"`
	TargetTables []string                  `json:"target_tables"`
	Images       []documentImageResultItem `json:"images"`
}

type documentImageResultItem struct {
	PageNo int    `json:"page_no"`
	GUID   string `json:"guid_code"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

type documentImageValidationError struct {
	status  int
	code    string
	message string
	details any
}

func (e documentImageValidationError) Error() string {
	return e.message
}

type documentImageExec interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func (h *DocumentImageHandler) Replace(c *gin.Context) {
	docNo := strings.TrimSpace(c.Param("doc_no"))

	var req replaceDocumentImagesRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, documentImagesMaxPayloadBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		api.BadRequest(c, "invalid_json", "request body must be valid JSON", nil)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		api.BadRequest(c, "invalid_json", "request body must contain one JSON object", nil)
		return
	}

	images, err := validateDocumentImagesRequest(docNo, req)
	if err != nil {
		var validation documentImageValidationError
		if errors.As(err, &validation) {
			api.Error(c, validation.status, validation.code, validation.message, validation.details)
			return
		}
		api.BadRequest(c, "validation_failed", err.Error(), nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	tenant := c.GetString(middleware.TenantKey)
	mainPool, err := h.dbm.Get(ctx, tenant)
	if err != nil {
		api.Internal(c, "db_pool_error", "could not get tenant database", err.Error())
		return
	}

	mainTx, err := mainPool.Begin(ctx)
	if err != nil {
		api.Internal(c, "db_tx_error", "could not start tenant transaction", err.Error())
		return
	}
	defer func() { _ = mainTx.Rollback(ctx) }()
	if err := acquireDocumentImageLock(ctx, mainTx, docNo); err != nil {
		api.Internal(c, "document_images_lock_failed", "could not lock document images", err.Error())
		return
	}
	if _, _, _, found, err := locateDocForLock(ctx, mainTx, docNo); err != nil {
		api.Internal(c, "document_lookup_failed", "could not look up document", err.Error())
		return
	} else if !found {
		api.NotFound(c, "document_not_found", "no active document found for doc_no: "+docNo)
		return
	}

	imagePool, err := h.dbm.Get(ctx, productImageDatabaseName(tenant))
	if err != nil {
		api.Internal(c, "image_db_pool_error", "could not get tenant image database", err.Error())
		return
	}

	if err := replaceDocumentImagesInPool(ctx, imagePool, docNo, images, true); err != nil {
		api.Internal(c, "document_images_replace_failed", "could not replace document images", err.Error())
		return
	}
	if err := replaceDocumentImages(ctx, mainTx, docNo, images, false); err != nil {
		api.Internal(c, "document_images_metadata_replace_failed", "could not replace document image metadata", err.Error())
		return
	}
	if err := mainTx.Commit(ctx); err != nil {
		api.Internal(c, "document_images_metadata_commit_failed", "could not commit document image metadata", err.Error())
		return
	}

	api.OK(c, buildDocumentImagesResult(docNo, req.TotalPages, req.Truncated, images))
}

type documentImageTx interface {
	documentImageExec
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

func replaceDocumentImagesInPool(ctx context.Context, pool *pgxpool.Pool, docNo string, images []preparedDocumentImage, includeBinary bool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := replaceDocumentImages(ctx, tx, docNo, images, includeBinary); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func acquireDocumentImageLock(ctx context.Context, tx documentImageExec, docNo string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "sml_doc_images:"+docNo)
	return err
}

func validateDocumentImagesRequest(docNo string, req replaceDocumentImagesRequest) ([]preparedDocumentImage, error) {
	docNo = strings.TrimSpace(docNo)
	if docNo == "" || strings.ContainsAny(docNo, "\x00\r\n") {
		return nil, documentImageValidationError{status: http.StatusBadRequest, code: "doc_no_invalid", message: "doc_no is required"}
	}
	if len(docNo) > documentImagesMaxDocNoLength {
		return nil, documentImageValidationError{status: http.StatusBadRequest, code: "doc_no_too_long", message: "doc_no is too long", details: gin.H{"max": documentImagesMaxDocNoLength}}
	}
	if len(req.Images) == 0 {
		return nil, documentImageValidationError{status: http.StatusBadRequest, code: "document_images_required", message: "at least one image is required"}
	}
	if len(req.Images) > documentImagesMaxCount {
		return nil, documentImageValidationError{status: http.StatusBadRequest, code: "document_images_too_many", message: "document images exceed the SML limit", details: gin.H{"max": documentImagesMaxCount}}
	}

	prepared := make([]preparedDocumentImage, 0, len(req.Images))
	seenPages := make(map[int]struct{}, len(req.Images))
	totalBytes := 0
	for i, item := range req.Images {
		pageNo := item.PageNo
		if pageNo < 1 || pageNo > documentImagesMaxCount {
			return nil, documentImageValidationError{status: http.StatusBadRequest, code: "document_image_page_invalid", message: "image pageNo must be between 1 and 8", details: gin.H{"index": i}}
		}
		if _, ok := seenPages[pageNo]; ok {
			return nil, documentImageValidationError{status: http.StatusBadRequest, code: "document_image_page_duplicate", message: "image pageNo must be unique", details: gin.H{"pageNo": pageNo}}
		}
		seenPages[pageNo] = struct{}{}
		if ct := strings.ToLower(strings.TrimSpace(item.ContentType)); ct != "" && ct != "image/jpeg" && ct != "image/jpg" {
			return nil, documentImageValidationError{status: http.StatusBadRequest, code: "document_image_content_type_invalid", message: "document images must be JPEG", details: gin.H{"pageNo": pageNo}}
		}
		data, err := decodeDocumentImageData(item.Data)
		if err != nil {
			return nil, documentImageValidationError{status: http.StatusBadRequest, code: "document_image_base64_invalid", message: "image data must be base64 JPEG", details: gin.H{"pageNo": pageNo}}
		}
		if len(data) == 0 || len(data) > documentImageMaxBytes {
			return nil, documentImageValidationError{status: http.StatusRequestEntityTooLarge, code: "document_image_too_large", message: "one or more images are too large", details: gin.H{"pageNo": pageNo, "maxBytes": documentImageMaxBytes}}
		}
		totalBytes += len(data)
		if totalBytes > documentImagesMaxPayloadBytes {
			return nil, documentImageValidationError{status: http.StatusRequestEntityTooLarge, code: "document_images_payload_too_large", message: "document images payload is too large", details: gin.H{"maxBytes": documentImagesMaxPayloadBytes}}
		}
		if !isJPEGBytes(data) {
			return nil, documentImageValidationError{status: http.StatusBadRequest, code: "document_image_not_jpeg", message: "document images must be JPEG", details: gin.H{"pageNo": pageNo}}
		}
		sum := sha256.Sum256(data)
		sha := hex.EncodeToString(sum[:])
		if strings.TrimSpace(item.SHA256) == "" || !strings.EqualFold(strings.TrimSpace(item.SHA256), sha) {
			return nil, documentImageValidationError{status: http.StatusBadRequest, code: "document_image_sha256_mismatch", message: "image sha256 does not match payload", details: gin.H{"pageNo": pageNo}}
		}
		prepared = append(prepared, preparedDocumentImage{
			PageNo: pageNo,
			GUID:   randomUUIDString(),
			SHA256: sha,
			Bytes:  data,
		})
	}
	return prepared, nil
}

func decodeDocumentImageData(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if i := strings.Index(value, ","); strings.HasPrefix(strings.ToLower(value), "data:image/") && i >= 0 {
		value = value[i+1:]
	}
	return base64.StdEncoding.DecodeString(value)
}

func isJPEGBytes(data []byte) bool {
	return len(data) >= 3 && bytes.Equal(data[:3], []byte{0xff, 0xd8, 0xff})
}

func replaceDocumentImages(ctx context.Context, tx documentImageExec, docNo string, images []preparedDocumentImage, includeBinary bool) error {
	if err := acquireDocumentImageLock(ctx, tx, docNo); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM public.sml_doc_images WHERE TRIM(image_id) = $1`, docNo); err != nil {
		return err
	}
	for _, image := range images {
		var imageFile any
		if includeBinary {
			imageFile = image.Bytes
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO public.sml_doc_images (
    image_id, image_file, system_id, guid_code, image_order, image_url
) VALUES ($1, $2, $3, $4, $5, '')
`, docNo, imageFile, documentImagesSystemID, image.GUID, image.PageNo); err != nil {
			return err
		}
	}
	return nil
}

func buildDocumentImagesResult(docNo string, totalPages int, truncated bool, images []preparedDocumentImage) replaceDocumentImagesResult {
	items := make([]documentImageResultItem, 0, len(images))
	totalBytes := 0
	for _, image := range images {
		totalBytes += len(image.Bytes)
		items = append(items, documentImageResultItem{
			PageNo: image.PageNo,
			GUID:   image.GUID,
			SHA256: image.SHA256,
			Bytes:  len(image.Bytes),
		})
	}
	return replaceDocumentImagesResult{
		DocNo:        docNo,
		ImageCount:   len(images),
		TotalPages:   totalPages,
		Truncated:    truncated,
		TotalBytes:   totalBytes,
		TargetTables: []string{"sml_doc_images@tenant_images", "sml_doc_images@tenant"},
		Images:       items,
	}
}

func randomUUIDString() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
		copy(b[:], sum[:16])
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
