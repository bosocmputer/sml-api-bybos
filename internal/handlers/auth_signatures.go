package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"sml-api-bybos/internal/api"
)

const (
	maxSMLSignatureEncodedBytes = 16 << 20
	maxSMLSignatureDecodedBytes = 12 << 20
	maxSMLSignaturePixels       = 60_000_000
	maxSMLSignatureDimension    = 12_000
)

var (
	errSMLSignatureMissing = errors.New("SML signature is missing")
	errSMLSignatureInvalid = errors.New("SML signature is invalid")
)

type smlSignatureRequest struct {
	Provider        string `json:"provider"`
	DataGroup       string `json:"dataGroup"`
	DatabaseName    string `json:"databaseName"`
	UserCode        string `json:"userCode"`
	ExpectedVersion string `json:"expectedVersion"`
}

type smlSignatureImage struct {
	Data        []byte
	ContentType string
	Version     string
	Width       int
	Height      int
}

func (h *AuthHandler) UserSignature(c *gin.Context) {
	var req smlSignatureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, "invalid_json", "request body must be valid JSON", nil)
		return
	}
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	dataGroup := strings.ToLower(strings.TrimSpace(req.DataGroup))
	databaseName := strings.TrimSpace(req.DatabaseName)
	userCode := strings.TrimSpace(req.UserCode)
	if provider == "" || dataGroup == "" || databaseName == "" || userCode == "" {
		api.BadRequest(c, "missing_signature_scope", "provider, dataGroup, databaseName, and userCode are required", nil)
		return
	}
	if provider != h.cfg.Auth.Provider || dataGroup != h.cfg.Auth.DataGroup {
		api.Forbidden(c, "auth_scope_invalid", "provider or dataGroup is not allowed", nil)
		return
	}

	registry, err := h.dbm.Get(c.Request.Context(), h.cfg.Auth.MainDatabase)
	if err != nil {
		api.Internal(c, "auth_db_pool_error", "connect to SML auth database failed", err.Error())
		return
	}
	database, err := h.lookupDatabase(c.Request.Context(), registry, dataGroup, databaseName)
	if err != nil {
		api.Forbidden(c, "database_not_allowed", "database is not allowed", nil)
		return
	}
	if len(h.cfg.DB.AllowedTenants) > 0 {
		if _, ok := h.cfg.DB.AllowedTenants[database.Tenant]; !ok {
			api.Forbidden(c, "tenant_not_allowed", "tenant is not allowed by this API", gin.H{"tenant": database.Tenant})
			return
		}
	}
	canonicalUserCode, allowed, err := h.isActiveSyncCandidateUser(c.Request.Context(), registry, database, userCode)
	if err != nil {
		api.Internal(c, "sml_user_signature_permission_failed", "verify SML signature permission failed", err.Error())
		return
	}
	if !allowed {
		api.Forbidden(c, "signature_user_not_allowed", "user is not allowed for this database", nil)
		return
	}
	userCode = canonicalUserCode

	imageValue, err := h.loadSMLSignatureValue(c.Request.Context(), database.Tenant, userCode)
	if errors.Is(err, errSMLSignatureMissing) {
		api.NotFound(c, "signature_not_found", "SML signature was not found")
		return
	}
	if err != nil {
		api.Internal(c, "sml_user_signature_load_failed", "load SML signature failed", err.Error())
		return
	}
	signature, err := decodeSMLSignature(imageValue)
	if err != nil {
		api.Error(c, http.StatusUnprocessableEntity, "signature_invalid", "SML signature is invalid", nil)
		return
	}
	if expected := strings.TrimSpace(req.ExpectedVersion); expected != "" && expected != signature.Version {
		api.Conflict(c, "signature_version_changed", "SML signature changed after preview", nil)
		return
	}

	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Signature-Version", signature.Version)
	c.Header("X-Signature-Width", fmt.Sprintf("%d", signature.Width))
	c.Header("X-Signature-Height", fmt.Sprintf("%d", signature.Height))
	c.Data(http.StatusOK, signature.ContentType, signature.Data)
}

func (h *AuthHandler) isActiveSyncCandidateUser(ctx context.Context, q pgxQuerier, database smlLoginDatabase, userCode string) (string, bool, error) {
	var canonical string
	err := q.QueryRow(ctx, `
WITH direct_allowed AS (
    SELECT trim(user_or_group_code) AS user_code
    FROM public.sml_database_list_user_and_group
    WHERE user_or_group_status = 0
      AND data_group = $1
      AND data_code = $2
),
group_allowed AS (
    SELECT trim(ug.user_code) AS user_code
    FROM public.sml_database_list_user_and_group m
    JOIN public.sml_user_and_group ug ON lower(trim(ug.group_code)) = lower(trim(m.user_or_group_code))
    WHERE m.user_or_group_status = 1
      AND m.data_group = $1
      AND m.data_code = $2
),
allowed AS (
    SELECT DISTINCT lower(trim(user_code)) AS user_code
    FROM (
        SELECT user_code FROM direct_allowed
        UNION
        SELECT user_code FROM group_allowed
    ) source
    WHERE COALESCE(user_code,'') <> ''
)
SELECT trim(ul.user_code)
FROM allowed a
JOIN public.sml_user_list ul ON lower(trim(ul.user_code)) = a.user_code
WHERE lower(trim(ul.user_code)) = lower(trim($3))
  AND COALESCE(ul.active_status,0) = 1
LIMIT 1
`, database.DataGroup, database.DataCode, userCode).Scan(&canonical)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(canonical), true, nil
}

func (h *AuthHandler) attachSignatureMetadata(ctx context.Context, database smlLoginDatabase, users []smlSyncCandidateUser, summary *smlSyncCandidatesSummary) error {
	if len(users) == 0 {
		return nil
	}
	keys := make([]string, 0, len(users))
	indexes := make(map[string]int, len(users))
	for i := range users {
		key := strings.ToLower(strings.TrimSpace(users[i].UserCode))
		if key == "" {
			continue
		}
		keys = append(keys, key)
		indexes[key] = i
	}
	pool, err := h.dbm.Get(ctx, database.Tenant)
	if err != nil {
		return err
	}
	rows, err := pool.Query(ctx, `
SELECT DISTINCT ON (lower(trim(code))) lower(trim(code)), COALESCE(signature_1,'')
FROM public.erp_user
WHERE lower(trim(code)) = ANY($1::text[])
ORDER BY lower(trim(code)), length(COALESCE(signature_1,'')) DESC
`, keys)
	if err != nil {
		return err
	}
	defer rows.Close()
	found := make(map[string]bool, len(users))
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		index, ok := indexes[strings.ToLower(strings.TrimSpace(key))]
		if !ok {
			continue
		}
		found[key] = true
		signature, err := decodeSMLSignature(value)
		if errors.Is(err, errSMLSignatureMissing) {
			users[index].SignatureIssue = "signature_missing"
			continue
		}
		if err != nil {
			users[index].SignatureIssue = "signature_invalid"
			continue
		}
		users[index].SignatureAvailable = true
		users[index].SignatureVersion = signature.Version
		users[index].SignatureBytes = len(signature.Data)
		users[index].SignatureWidth = signature.Width
		users[index].SignatureHeight = signature.Height
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range users {
		key := strings.ToLower(strings.TrimSpace(users[i].UserCode))
		if !found[key] && users[i].SignatureIssue == "" {
			users[i].SignatureIssue = "signature_missing"
		}
		switch {
		case users[i].SignatureAvailable:
			summary.SignatureAvailable++
		case users[i].SignatureIssue == "signature_invalid":
			summary.SignatureInvalid++
		default:
			summary.SignatureMissing++
		}
	}
	return nil
}

func (h *AuthHandler) loadSMLSignatureValue(ctx context.Context, tenant, userCode string) (string, error) {
	pool, err := h.dbm.Get(ctx, tenant)
	if err != nil {
		return "", err
	}
	var value string
	err = pool.QueryRow(ctx, `
SELECT COALESCE(signature_1,'')
FROM public.erp_user
WHERE lower(trim(code)) = lower(trim($1))
ORDER BY length(COALESCE(signature_1,'')) DESC
LIMIT 1
`, userCode).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && strings.TrimSpace(value) == "") {
		return "", errSMLSignatureMissing
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

func decodeSMLSignature(value string) (smlSignatureImage, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return smlSignatureImage{}, errSMLSignatureMissing
	}
	if len(value) > maxSMLSignatureEncodedBytes {
		return smlSignatureImage{}, errSMLSignatureInvalid
	}
	contentType := ""
	if strings.HasPrefix(strings.ToLower(value), "data:") {
		comma := strings.IndexByte(value, ',')
		if comma <= 5 || !strings.Contains(strings.ToLower(value[:comma]), ";base64") {
			return smlSignatureImage{}, errSMLSignatureInvalid
		}
		contentType = strings.ToLower(strings.TrimSpace(strings.SplitN(value[5:comma], ";", 2)[0]))
		value = value[comma+1:]
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, value)
	if len(value) == 0 || len(value) > maxSMLSignatureEncodedBytes {
		return smlSignatureImage{}, errSMLSignatureInvalid
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil || len(data) == 0 || len(data) > maxSMLSignatureDecodedBytes {
		return smlSignatureImage{}, errSMLSignatureInvalid
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return smlSignatureImage{}, errSMLSignatureInvalid
	}
	if config.Width > maxSMLSignatureDimension || config.Height > maxSMLSignatureDimension || int64(config.Width)*int64(config.Height) > maxSMLSignaturePixels {
		return smlSignatureImage{}, errSMLSignatureInvalid
	}
	detectedType := ""
	switch strings.ToLower(format) {
	case "jpeg":
		detectedType = "image/jpeg"
	case "png":
		detectedType = "image/png"
	default:
		return smlSignatureImage{}, errSMLSignatureInvalid
	}
	if contentType != "" && contentType != detectedType && !(contentType == "image/jpg" && detectedType == "image/jpeg") {
		return smlSignatureImage{}, errSMLSignatureInvalid
	}
	sum := sha256.Sum256(data)
	return smlSignatureImage{
		Data:        data,
		ContentType: detectedType,
		Version:     hex.EncodeToString(sum[:]),
		Width:       config.Width,
		Height:      config.Height,
	}, nil
}
