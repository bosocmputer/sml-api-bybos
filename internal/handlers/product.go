package handlers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/middleware"
	"sml-api-bybos/internal/models"
)

type ProductHandler struct {
	dbm *db.Manager
}

type productImageMeta struct {
	ImageCount      int
	PrimaryRoworder int
	PrimaryGuid     string
	PrimaryBytes    int64
}

type productImageListItem struct {
	Roworder   int    `json:"roworder"`
	ImageOrder int    `json:"image_order"`
	Guid       string `json:"guid"`
	Bytes      int64  `json:"bytes"`
}

const productImageMetadataSQL = `WITH ranked AS (
	SELECT
		TRIM(image_id) AS item_code,
		roworder,
		COALESCE(guid_code, '') AS guid_code,
		COALESCE(octet_length(image_file), 0)::bigint AS image_bytes,
		COUNT(*) OVER (PARTITION BY TRIM(image_id))::int AS image_count,
		ROW_NUMBER() OVER (
			PARTITION BY TRIM(image_id)
			ORDER BY COALESCE(image_order, 0), roworder
		) AS rn
	FROM public.images
	WHERE TRIM(image_id) = ANY($1::text[])
	  AND image_file IS NOT NULL
)
SELECT item_code, image_count, roworder, guid_code, image_bytes
FROM ranked
WHERE rn = 1`

const productImageListSQL = `SELECT
	roworder,
	COALESCE(image_order, 0)::int AS image_order,
	COALESCE(guid_code, '') AS guid_code,
	COALESCE(octet_length(image_file), 0)::bigint AS image_bytes
FROM public.images
WHERE TRIM(image_id) = $1
  AND image_file IS NOT NULL
ORDER BY COALESCE(image_order, 0), roworder`

const unitListSQL = `SELECT
	code,
	COALESCE(name_1, '') AS name_1,
	COALESCE(name_2, '') AS name_2
FROM public.ic_unit
WHERE COALESCE(status, 0) = 0
  AND (
	@search = ''
	OR code ILIKE @search_like
	OR COALESCE(name_1, '') ILIKE @search_like
	OR COALESCE(name_2, '') ILIKE @search_like
  )
ORDER BY code
LIMIT @size`

const productUnitsSQL = `SELECT
	uu.code,
	COALESCE(NULLIF(unit.name_1, ''), uu.code) AS name_1,
	COALESCE(unit.name_2, '') AS name_2,
	COALESCE(uu.stand_value, 1)::float8 AS stand_value,
	COALESCE(uu.divide_value, 1)::float8 AS divide_value,
	uu.code = COALESCE(i.unit_standard, '') AS is_default
FROM public.ic_unit_use uu
JOIN public.ic_inventory i ON i.code = uu.ic_code
LEFT JOIN public.ic_unit unit ON unit.code = uu.code
WHERE uu.ic_code = $1
  AND COALESCE(uu.status, 0) = 0
ORDER BY COALESCE(uu.line_number, uu.row_order, 0), uu.code`

func NewProductHandler(dbm *db.Manager) *ProductHandler {
	return &ProductHandler{dbm: dbm}
}

func (h *ProductHandler) ListUnits(c *gin.Context) {
	search := strings.TrimSpace(c.Query("search"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "100"))
	if size < 1 || size > 500 {
		size = 100
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "connect to SML database failed", err.Error())
		return
	}

	rows, err := pool.Query(ctx, unitListSQL, pgx.NamedArgs{
		"search":      search,
		"search_like": "%" + search + "%",
		"size":        size,
	})
	if err != nil {
		api.Internal(c, "unit_list_failed", "list units failed", err.Error())
		return
	}
	defer rows.Close()

	units := []models.Unit{}
	for rows.Next() {
		var u models.Unit
		if err := rows.Scan(&u.Code, &u.Name1, &u.Name2); err != nil {
			api.Internal(c, "unit_scan_failed", "read unit row failed", err.Error())
			return
		}
		units = append(units, u)
	}
	if err := rows.Err(); err != nil {
		api.Internal(c, "unit_list_failed", "list units failed", err.Error())
		return
	}
	api.OK(c, gin.H{"units": units})
}

func (h *ProductHandler) ListProductUnits(c *gin.Context) {
	code := strings.TrimSpace(c.Param("code"))
	if code == "" || strings.ContainsAny(code, "\x00\r\n") {
		api.BadRequest(c, "product_code_invalid", "product code is required", nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "connect to SML database failed", err.Error())
		return
	}

	var (
		standardCode string
		standardName string
		unitName1    string
		unitName2    string
	)
	err = pool.QueryRow(ctx, `SELECT
		COALESCE(i.unit_standard, ''),
		COALESCE(i.unit_standard_name, ''),
		COALESCE(unit.name_1, ''),
		COALESCE(unit.name_2, '')
	FROM public.ic_inventory i
	LEFT JOIN public.ic_unit unit ON unit.code = i.unit_standard
	WHERE i.code = $1
	  AND COALESCE(i.status, 0) = 0`, code).
		Scan(&standardCode, &standardName, &unitName1, &unitName2)
	if err != nil {
		if err == pgx.ErrNoRows {
			api.NotFound(c, "product_not_found", "product not found")
			return
		}
		api.Internal(c, "product_unit_standard_failed", "read product standard unit failed", err.Error())
		return
	}

	rows, err := pool.Query(ctx, productUnitsSQL, code)
	if err != nil {
		api.Internal(c, "product_unit_list_failed", "list product units failed", err.Error())
		return
	}
	defer rows.Close()

	units := []models.ProductUnit{}
	seen := map[string]int{}
	for rows.Next() {
		var u models.ProductUnit
		if err := rows.Scan(&u.Code, &u.Name1, &u.Name2, &u.StandValue, &u.DivideValue, &u.IsDefault); err != nil {
			api.Internal(c, "product_unit_scan_failed", "read product unit row failed", err.Error())
			return
		}
		if u.StandValue == 0 {
			u.StandValue = 1
		}
		if u.DivideValue == 0 {
			u.DivideValue = 1
		}
		seen[u.Code] = len(units)
		units = append(units, u)
	}
	if err := rows.Err(); err != nil {
		api.Internal(c, "product_unit_list_failed", "list product units failed", err.Error())
		return
	}

	standardCode = strings.TrimSpace(standardCode)
	if standardCode != "" {
		if idx, ok := seen[standardCode]; ok {
			units[idx].IsDefault = true
		} else {
			name1 := firstNonEmpty(unitName1, standardName, standardCode)
			units = append(units, models.ProductUnit{
				Code:        standardCode,
				Name1:       name1,
				Name2:       unitName2,
				StandValue:  1,
				DivideValue: 1,
				IsDefault:   true,
			})
		}
	}

	api.OK(c, gin.H{"units": units})
}

func (h *ProductHandler) List(c *gin.Context) {
	search := c.Query("search")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 20
	}
	offset := (page - 1) * size

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "connect to SML database failed", err.Error())
		return
	}

	baseWhere := "WHERE i.status = 0"
	args := pgx.NamedArgs{}
	if search != "" {
		baseWhere += " AND (i.code ILIKE @search OR i.name_1 ILIKE @search)"
		args["search"] = "%" + search + "%"
	}

	var total int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM ic_inventory i "+baseWhere, args).Scan(&total); err != nil {
		api.Internal(c, "product_count_failed", "count products failed", err.Error())
		return
	}

	query := `SELECT i.code, i.name_1, COALESCE(i.name_2,''), i.unit_standard, COALESCE(i.unit_standard_name,''),
		COALESCE(i.group_main,''), COALESCE(i.balance_qty,0), COALESCE(i.average_cost,0),
		COALESCE(NULLIF(regexp_replace(COALESCE(pf.price_0,''), '[^0-9\.-]', '', 'g'), ''), '0')::float8 AS price, i.item_status, i.status
		FROM ic_inventory i
		LEFT JOIN ic_inventory_price_formula pf
		  ON pf.ic_code = i.code AND pf.unit_code = i.unit_standard AND pf.sale_type = 0
		` + baseWhere + ` ORDER BY i.code LIMIT @size OFFSET @offset`
	args["size"] = size
	args["offset"] = offset

	rows, err := pool.Query(ctx, query, args)
	if err != nil {
		api.Internal(c, "product_list_failed", "list products failed", err.Error())
		return
	}
	defer rows.Close()

	var products []models.Product
	for rows.Next() {
		var p models.Product
		if err := rows.Scan(&p.Code, &p.Name1, &p.Name2, &p.UnitStd, &p.UnitStdName,
			&p.GroupCode, &p.BalanceQty, &p.AverageCost, &p.Price, &p.ItemStatus, &p.Status); err != nil {
			api.Internal(c, "product_scan_failed", "read product row failed", err.Error())
			return
		}
		products = append(products, p)
	}
	if products == nil {
		products = []models.Product{}
	}
	h.attachImageMetadata(ctx, c.GetString(middleware.TenantKey), products)

	api.OKPage(c, products, total, page, size)
}

func (h *ProductHandler) Get(c *gin.Context) {
	code := strings.TrimSpace(c.Param("code"))
	if code == "" {
		api.BadRequest(c, "product_code_invalid", "product code is required", nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "connect to SML database failed", err.Error())
		return
	}

	var p models.Product
	err = pool.QueryRow(ctx,
		`SELECT i.code, i.name_1, COALESCE(i.name_2,''), i.unit_standard, COALESCE(i.unit_standard_name,''),
		COALESCE(i.group_main,''), COALESCE(i.balance_qty,0), COALESCE(i.average_cost,0),
		COALESCE(NULLIF(regexp_replace(COALESCE(pf.price_0,''), '[^0-9\.-]', '', 'g'), ''), '0')::float8 AS price, i.item_status, i.status
		FROM ic_inventory i
		LEFT JOIN ic_inventory_price_formula pf
		  ON pf.ic_code = i.code AND pf.unit_code = i.unit_standard AND pf.sale_type = 0
		WHERE i.code = $1`, code).
		Scan(&p.Code, &p.Name1, &p.Name2, &p.UnitStd, &p.UnitStdName,
			&p.GroupCode, &p.BalanceQty, &p.AverageCost, &p.Price, &p.ItemStatus, &p.Status)
	if err != nil {
		api.NotFound(c, "product_not_found", "product not found")
		return
	}
	products := []models.Product{p}
	h.attachImageMetadata(ctx, c.GetString(middleware.TenantKey), products)
	p = products[0]
	api.OK(c, p)
}

func (h *ProductHandler) ListImages(c *gin.Context) {
	code := strings.TrimSpace(c.Param("code"))
	if code == "" || strings.ContainsAny(code, "\x00\r\n") {
		api.BadRequest(c, "product_code_invalid", "product code is required", nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, productImageDatabaseName(c.GetString(middleware.TenantKey)))
	if err != nil {
		api.OK(c, gin.H{"images": []productImageListItem{}})
		return
	}

	rows, err := pool.Query(ctx, productImageListSQL, code)
	if err != nil {
		api.OK(c, gin.H{"images": []productImageListItem{}})
		return
	}
	defer rows.Close()

	images := []productImageListItem{}
	for rows.Next() {
		var img productImageListItem
		if err := rows.Scan(&img.Roworder, &img.ImageOrder, &img.Guid, &img.Bytes); err != nil {
			api.Internal(c, "product_image_list_scan_failed", "read product images failed", err.Error())
			return
		}
		images = append(images, img)
	}
	if err := rows.Err(); err != nil {
		api.Internal(c, "product_image_list_failed", "read product images failed", err.Error())
		return
	}
	api.OK(c, gin.H{"images": images})
}

func (h *ProductHandler) GetImage(c *gin.Context) {
	code := strings.TrimSpace(c.Param("code"))
	roworder, err := strconv.Atoi(c.Param("roworder"))
	if code == "" || err != nil || roworder < 1 {
		api.BadRequest(c, "product_image_param_invalid", "product code and image roworder are required", nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, productImageDatabaseName(c.GetString(middleware.TenantKey)))
	if err != nil {
		api.NotFound(c, "product_image_not_found", "product image not found")
		return
	}

	var (
		imageBytes []byte
		guid       string
	)
	err = pool.QueryRow(ctx, `SELECT image_file, COALESCE(guid_code, '')
		FROM public.images
		WHERE roworder = $1
		  AND TRIM(image_id) = $2
		  AND image_file IS NOT NULL
		LIMIT 1`, roworder, code).Scan(&imageBytes, &guid)
	if err != nil {
		if err == pgx.ErrNoRows {
			api.NotFound(c, "product_image_not_found", "product image not found")
			return
		}
		api.Internal(c, "product_image_read_failed", "read product image failed", err.Error())
		return
	}
	if len(imageBytes) == 0 {
		api.NotFound(c, "product_image_not_found", "product image not found")
		return
	}

	c.Header("Cache-Control", "private, max-age=3600")
	c.Header("ETag", fmt.Sprintf(`W/"%s-%d-%d"`, guid, roworder, len(imageBytes)))
	c.Data(http.StatusOK, sniffImageContentType(imageBytes), imageBytes)
}

func (h *ProductHandler) attachImageMetadata(ctx context.Context, tenant string, products []models.Product) {
	if len(products) == 0 {
		return
	}

	codes := make([]string, 0, len(products))
	seen := make(map[string]struct{}, len(products))
	for _, p := range products {
		code := strings.TrimSpace(p.Code)
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	if len(codes) == 0 {
		return
	}

	metaByCode, err := h.loadImageMetadata(ctx, tenant, codes)
	if err != nil {
		// The image DB is optional for read paths. Product sync/list must continue
		// even when sml1_2026_images is unavailable.
		return
	}
	for i := range products {
		meta, ok := metaByCode[strings.TrimSpace(products[i].Code)]
		if !ok {
			continue
		}
		roworder := meta.PrimaryRoworder
		products[i].ImageCount = meta.ImageCount
		products[i].PrimaryImageRoworder = &roworder
		products[i].PrimaryImageGuid = meta.PrimaryGuid
		products[i].PrimaryImageBytes = meta.PrimaryBytes
	}
}

func (h *ProductHandler) loadImageMetadata(ctx context.Context, tenant string, codes []string) (map[string]productImageMeta, error) {
	pool, err := h.dbm.Get(ctx, productImageDatabaseName(tenant))
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, productImageMetadataSQL, codes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	metaByCode := make(map[string]productImageMeta, len(codes))
	for rows.Next() {
		var (
			code string
			meta productImageMeta
		)
		if err := rows.Scan(&code, &meta.ImageCount, &meta.PrimaryRoworder, &meta.PrimaryGuid, &meta.PrimaryBytes); err != nil {
			return nil, err
		}
		metaByCode[code] = meta
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return metaByCode, nil
}

func productImageDatabaseName(tenant string) string {
	tenant = strings.ToLower(strings.TrimSpace(tenant))
	if tenant == "" {
		return "_images"
	}
	return tenant + "_images"
}

func sniffImageContentType(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		return "image/jpeg"
	case bytes.HasPrefix(data, []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}):
		return "image/png"
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif"
	case len(data) >= 12 && bytes.Equal(data[0:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}
