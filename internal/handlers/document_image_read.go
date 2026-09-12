package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/middleware"
)

const (
	// documentImagesListMaxRows caps how many rows a single listing returns. SML
	// documents realistically hold a few dozen images; anything beyond this points
	// at corrupt data rather than a legitimate document, and we refuse to stream it.
	documentImagesListMaxRows = 500
	// documentImagesReadTimeout bounds every read query so a slow or locked SML
	// database cannot pin a request goroutine indefinitely.
	documentImagesReadTimeout = 20 * time.Second
	// documentImageContentType matches what Replace accepts, so stored bytes are
	// always JPEG regardless of what a client claims on the way back out.
	documentImageContentType = "image/jpeg"
)

type documentImageListItem struct {
	PageNo int    `json:"page_no"`
	GUID   string `json:"guid_code"`
	Bytes  int    `json:"bytes"`
}

type listDocumentImagesResult struct {
	DocNo      string                  `json:"doc_no"`
	ImageCount int                     `json:"image_count"`
	TotalBytes int                     `json:"total_bytes"`
	Images     []documentImageListItem `json:"images"`
}

// List returns metadata for every image stored against a document, deliberately
// without the image_file bytes: a document's images routinely total several MB,
// and callers page through them one at a time via Download.
func (h *DocumentImageHandler) List(c *gin.Context) {
	docNo := strings.TrimSpace(c.Param("doc_no"))
	if err := validateDocumentImageDocNo(docNo); err != nil {
		var validation documentImageValidationError
		if errors.As(err, &validation) {
			api.Error(c, validation.status, validation.code, validation.message, validation.details)
			return
		}
		api.BadRequest(c, "validation_failed", err.Error(), nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), documentImagesReadTimeout)
	defer cancel()

	tenant := c.GetString(middleware.TenantKey)
	pool, err := h.dbm.Get(ctx, tenant)
	if err != nil {
		api.Internal(c, "db_pool_error", "could not get tenant database", err.Error())
		return
	}

	rows, err := pool.Query(ctx, `
SELECT image_order, COALESCE(guid_code, ''), COALESCE(octet_length(image_file), 0)
FROM public.sml_doc_images
WHERE image_id = $1
ORDER BY image_order
LIMIT $2
`, docNo, documentImagesListMaxRows)
	if err != nil {
		api.Internal(c, "document_images_list_failed", "could not list document images", err.Error())
		return
	}
	defer rows.Close()

	images := make([]documentImageListItem, 0, 8)
	totalBytes := 0
	for rows.Next() {
		var item documentImageListItem
		if err := rows.Scan(&item.PageNo, &item.GUID, &item.Bytes); err != nil {
			api.Internal(c, "document_images_list_failed", "could not read document images", err.Error())
			return
		}
		totalBytes += item.Bytes
		images = append(images, item)
	}
	if err := rows.Err(); err != nil {
		api.Internal(c, "document_images_list_failed", "could not read document images", err.Error())
		return
	}

	api.OK(c, listDocumentImagesResult{
		DocNo:      docNo,
		ImageCount: len(images),
		TotalBytes: totalBytes,
		Images:     images,
	})
}

// Download streams one image's bytes. The row is fetched by (image_id, image_order)
// so a caller can only ever reach an image that belongs to the document it named.
func (h *DocumentImageHandler) Download(c *gin.Context) {
	docNo := strings.TrimSpace(c.Param("doc_no"))
	if err := validateDocumentImageDocNo(docNo); err != nil {
		var validation documentImageValidationError
		if errors.As(err, &validation) {
			api.Error(c, validation.status, validation.code, validation.message, validation.details)
			return
		}
		api.BadRequest(c, "validation_failed", err.Error(), nil)
		return
	}

	pageNo, err := strconv.Atoi(strings.TrimSpace(c.Param("page_no")))
	if err != nil || pageNo < 1 {
		api.BadRequest(c, "document_image_page_invalid", "image pageNo must be 1 or greater", nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), documentImagesReadTimeout)
	defer cancel()

	tenant := c.GetString(middleware.TenantKey)
	pool, err := h.dbm.Get(ctx, tenant)
	if err != nil {
		api.Internal(c, "db_pool_error", "could not get tenant database", err.Error())
		return
	}

	// Read the size first so the response can be length-delimited and oversized
	// rows rejected before any bytes are pulled into memory.
	var size int
	err = pool.QueryRow(ctx, `
SELECT COALESCE(octet_length(image_file), 0)
FROM public.sml_doc_images
WHERE image_id = $1 AND image_order = $2
`, docNo, pageNo).Scan(&size)
	if errors.Is(err, pgx.ErrNoRows) {
		api.NotFound(c, "document_image_not_found", "no image found for this document page")
		return
	}
	if err != nil {
		api.Internal(c, "document_image_read_failed", "could not read document image", err.Error())
		return
	}
	if size <= 0 {
		api.NotFound(c, "document_image_not_found", "no image found for this document page")
		return
	}
	if size > documentImageMaxBytes {
		api.Error(c, http.StatusUnprocessableEntity, "document_image_too_large", "stored image exceeds the supported size", gin.H{
			"bytes": size,
			"max":   documentImageMaxBytes,
		})
		return
	}

	lo, err := pool.Query(ctx, `
SELECT image_file
FROM public.sml_doc_images
WHERE image_id = $1 AND image_order = $2
`, docNo, pageNo)
	if err != nil {
		api.Internal(c, "document_image_read_failed", "could not read document image", err.Error())
		return
	}
	defer lo.Close()
	if !lo.Next() {
		if err := lo.Err(); err != nil {
			api.Internal(c, "document_image_read_failed", "could not read document image", err.Error())
			return
		}
		api.NotFound(c, "document_image_not_found", "no image found for this document page")
		return
	}
	var payload []byte
	if err := lo.Scan(&payload); err != nil {
		api.Internal(c, "document_image_read_failed", "could not read document image", err.Error())
		return
	}
	if len(payload) == 0 {
		api.NotFound(c, "document_image_not_found", "no image found for this document page")
		return
	}

	// These are signed business documents: never let a browser or intermediary
	// retain a copy, matching how PaperLess serves its own document files.
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Type", documentImageContentType)
	c.Header("Content-Length", strconv.Itoa(len(payload)))
	c.Status(http.StatusOK)
	if _, err := c.Writer.Write(payload); err != nil {
		// Response headers are already committed; the client most likely went away.
		_ = c.Error(err)
	}
}

func validateDocumentImageDocNo(docNo string) error {
	if docNo == "" {
		return documentImageValidationError{status: http.StatusBadRequest, code: "doc_no_required", message: "doc_no is required"}
	}
	if len(docNo) > documentImagesMaxDocNoLength {
		return documentImageValidationError{status: http.StatusBadRequest, code: "doc_no_too_long", message: "doc_no exceeds the SML image_id length", details: gin.H{"max": documentImagesMaxDocNoLength}}
	}
	return nil
}
