package handlers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/middleware"
	"sml-api-bybos/internal/models"
)

type supplierQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Begin(ctx context.Context) (supplierTx, error)
}

type supplierTx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

type supplierPool struct {
	*pgxpool.Pool
}

func (p supplierPool) Begin(ctx context.Context) (supplierTx, error) {
	return p.Pool.Begin(ctx)
}

type SupplierHandler struct {
	dbm     *db.Manager
	getPool func(context.Context, string) (supplierQuerier, error)
}

func NewSupplierHandler(dbm *db.Manager) *SupplierHandler {
	return &SupplierHandler{
		dbm: dbm,
		getPool: func(ctx context.Context, tenant string) (supplierQuerier, error) {
			p, err := dbm.Get(ctx, tenant)
			if err != nil {
				return nil, err
			}
			return supplierPool{Pool: p}, nil
		},
	}
}

type createSupplierRequest struct {
	Code       string `json:"code" binding:"required"`
	APStatus   *int   `json:"ap_status"`
	Firstname  string `json:"firstname"`
	Lastname   string `json:"lastname"`
	Name1      string `json:"name_1"`
	NameEng1   string `json:"name_eng_1"`
	Address    string `json:"address"`
	Remark     string `json:"remark"`
	TaxID      string `json:"tax_id"`
	BranchType *int   `json:"branch_type"`
	BranchCode string `json:"branch_code"`
	CardID     string `json:"card_id"`
}

type normalizedSupplierCreate struct {
	Code       string
	APStatus   int
	Firstname  string
	Lastname   string
	Name1      string
	NameEng1   string
	Address    string
	Remark     string
	TaxID      string
	BranchType int
	BranchCode string
	CardID     string
}

func (h *SupplierHandler) List(c *gin.Context) {
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

	pool, err := h.getPool(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "connect to SML database failed", err.Error())
		return
	}

	baseWhere, args := supplierSearchWhere(search)

	var total int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM ap_supplier s LEFT JOIN ap_supplier_detail d ON s.code = d.ap_code "+baseWhere, args).Scan(&total); err != nil {
		api.Internal(c, "supplier_count_failed", "count suppliers failed", err.Error())
		return
	}

	query := `SELECT COALESCE(s.code,''), COALESCE(s.name_1,''), COALESCE(s.name_2,''), COALESCE(s.name_eng_1,''),
		COALESCE(s.firstname,''), COALESCE(s.lastname,''), COALESCE(s.telephone,''), COALESCE(s.email,''),
		COALESCE(s.address,''), COALESCE(s.remark,''), COALESCE(d.tax_id,''), COALESCE(d.card_id,''),
		COALESCE(d.branch_type,0), COALESCE(d.branch_code,''), COALESCE(s.status,0), COALESCE(s.ap_status,0)
		FROM ap_supplier s
		LEFT JOIN ap_supplier_detail d ON s.code = d.ap_code
		` + baseWhere + ` ORDER BY s.code LIMIT @size OFFSET @offset`
	args["size"] = size
	args["offset"] = offset

	rows, err := pool.Query(ctx, query, args)
	if err != nil {
		api.Internal(c, "supplier_list_failed", "list suppliers failed", err.Error())
		return
	}
	defer rows.Close()

	var suppliers []models.Supplier
	for rows.Next() {
		var s models.Supplier
		if err := rows.Scan(&s.Code, &s.Name1, &s.Name2, &s.NameEng1,
			&s.Firstname, &s.Lastname, &s.Telephone, &s.Email,
			&s.Address, &s.Remark, &s.TaxID, &s.CardID,
			&s.BranchType, &s.BranchCode, &s.Status, &s.APStatus); err != nil {
			api.Internal(c, "supplier_scan_failed", "read supplier row failed", err.Error())
			return
		}
		normalizeSupplierModel(&s)
		suppliers = append(suppliers, s)
	}
	if suppliers == nil {
		suppliers = []models.Supplier{}
	}

	api.OKPage(c, suppliers, total, page, size)
}

func (h *SupplierHandler) Get(c *gin.Context) {
	code := c.Param("code")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	pool, err := h.getPool(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "connect to SML database failed", err.Error())
		return
	}

	var s models.Supplier
	err = pool.QueryRow(ctx,
		`SELECT COALESCE(s.code,''), COALESCE(s.name_1,''), COALESCE(s.name_2,''), COALESCE(s.name_eng_1,''),
		COALESCE(s.firstname,''), COALESCE(s.lastname,''), COALESCE(s.telephone,''), COALESCE(s.email,''),
		COALESCE(s.address,''), COALESCE(s.remark,''), COALESCE(d.tax_id,''), COALESCE(d.card_id,''),
		COALESCE(d.branch_type,0), COALESCE(d.branch_code,''), COALESCE(s.status,0), COALESCE(s.ap_status,0)
		FROM ap_supplier s
		LEFT JOIN ap_supplier_detail d ON s.code = d.ap_code
		WHERE s.code = $1`, code).
		Scan(&s.Code, &s.Name1, &s.Name2, &s.NameEng1,
			&s.Firstname, &s.Lastname, &s.Telephone, &s.Email,
			&s.Address, &s.Remark, &s.TaxID, &s.CardID,
			&s.BranchType, &s.BranchCode, &s.Status, &s.APStatus)
	if err != nil {
		api.NotFound(c, "supplier_not_found", "supplier not found")
		return
	}
	normalizeSupplierModel(&s)
	api.OK(c, s)
}

func (h *SupplierHandler) Create(c *gin.Context) {
	var req createSupplierRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, "invalid_json", "invalid supplier payload", err.Error())
		return
	}
	payload, err := normalizeSupplierCreateRequest(req)
	if err != nil {
		api.BadRequest(c, "validation_failed", err.Error(), nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	pool, err := h.getPool(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "connect to SML database failed", err.Error())
		return
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		api.Internal(c, "supplier_tx_failed", "start supplier transaction failed", err.Error())
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx,
		`INSERT INTO ap_supplier (
			code, ap_status, firstname, lastname, name_eng_1, name_1,
			address, remark, status
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,0)`,
		payload.Code, payload.APStatus, payload.Firstname, payload.Lastname, payload.NameEng1, payload.Name1,
		payload.Address, payload.Remark,
	)
	if err != nil {
		if isPGUniqueViolation(err) {
			api.Conflict(c, "duplicate_supplier_code", fmt.Sprintf("supplier code '%s' already exists", payload.Code), gin.H{"code": payload.Code})
			return
		}
		api.Internal(c, "supplier_insert_failed", "insert supplier failed", err.Error())
		return
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO ap_supplier_detail (
			ap_code, tax_id, branch_type, branch_code, card_id
		) VALUES ($1,$2,$3,$4,$5)`,
		payload.Code, payload.TaxID, payload.BranchType, payload.BranchCode, payload.CardID,
	)
	if err != nil {
		if isPGUniqueViolation(err) {
			api.Conflict(c, "duplicate_supplier_detail", fmt.Sprintf("supplier detail for code '%s' already exists", payload.Code), gin.H{"code": payload.Code})
			return
		}
		api.Internal(c, "supplier_detail_insert_failed", "insert supplier detail failed", err.Error())
		return
	}

	if err := tx.Commit(ctx); err != nil {
		api.Internal(c, "supplier_commit_failed", "commit supplier failed", err.Error())
		return
	}

	s := models.Supplier{
		Code:       payload.Code,
		Name1:      payload.Name1,
		NameEng1:   payload.NameEng1,
		Firstname:  payload.Firstname,
		Lastname:   payload.Lastname,
		Address:    payload.Address,
		Remark:     payload.Remark,
		TaxID:      payload.TaxID,
		CardID:     payload.CardID,
		BranchType: payload.BranchType,
		BranchCode: payload.BranchCode,
		Status:     0,
		APStatus:   payload.APStatus,
	}
	normalizeSupplierModel(&s)
	api.Created(c, s)
}

func supplierSearchWhere(search string) (string, pgx.NamedArgs) {
	baseWhere := "WHERE COALESCE(s.status,0) = 0"
	args := pgx.NamedArgs{}
	search = strings.TrimSpace(search)
	if search != "" {
		baseWhere += ` AND (
			s.code ILIKE @search OR s.name_1 ILIKE @search OR s.name_eng_1 ILIKE @search OR
			s.firstname ILIKE @search OR s.lastname ILIKE @search OR s.telephone ILIKE @search OR
			d.tax_id ILIKE @search OR d.card_id ILIKE @search
		)`
		args["search"] = "%" + search + "%"
	}
	return baseWhere, args
}

func normalizeSupplierCreateRequest(req createSupplierRequest) (normalizedSupplierCreate, error) {
	req.Code = strings.ToUpper(req.Code)
	p := normalizedSupplierCreate{
		Code:       req.Code,
		Firstname:  strings.TrimSpace(req.Firstname),
		Lastname:   strings.TrimSpace(req.Lastname),
		Name1:      strings.TrimSpace(req.Name1),
		NameEng1:   strings.TrimSpace(req.NameEng1),
		Address:    strings.TrimSpace(req.Address),
		Remark:     strings.TrimSpace(req.Remark),
		TaxID:      strings.TrimSpace(req.TaxID),
		BranchCode: strings.TrimSpace(req.BranchCode),
		CardID:     strings.TrimSpace(req.CardID),
	}

	if req.Code != strings.TrimSpace(req.Code) {
		return p, fmt.Errorf("รหัสเจ้าหนี้ห้ามมีช่องว่างหน้า/หลัง")
	}
	p.Code = strings.TrimSpace(req.Code)
	if p.Code == "" {
		return p, fmt.Errorf("กรุณากรอกรหัสเจ้าหนี้")
	}
	if containsHiddenPartyCodeChar(p.Code) {
		return p, fmt.Errorf("รหัสเจ้าหนี้มีอักขระซ่อนหรืออักขระควบคุม")
	}

	legacyPayload := req.APStatus == nil && req.BranchType == nil &&
		p.Firstname == "" && p.Lastname == "" && p.NameEng1 == "" &&
		p.Address == "" && p.Remark == "" && p.TaxID == "" &&
		p.BranchCode == "" && p.CardID == ""

	if req.APStatus == nil {
		if !legacyPayload {
			return p, fmt.Errorf("กรุณาเลือกชนิดเจ้าหนี้")
		}
		p.APStatus = 1
	} else {
		p.APStatus = *req.APStatus
	}
	if p.APStatus != 0 && p.APStatus != 1 {
		return p, fmt.Errorf("ชนิดเจ้าหนี้ไม่ถูกต้อง")
	}

	if req.BranchType == nil {
		if !legacyPayload {
			return p, fmt.Errorf("กรุณาเลือกประเภทสาขา")
		}
		p.BranchType = 0
	} else {
		p.BranchType = *req.BranchType
	}
	if p.BranchType != 0 && p.BranchType != 1 {
		return p, fmt.Errorf("ประเภทสาขาไม่ถูกต้อง")
	}
	if p.BranchType == 0 {
		p.BranchCode = "00000"
	} else if p.BranchCode == "" {
		return p, fmt.Errorf("กรุณากรอกรหัสสาขา")
	}

	if p.APStatus == 0 {
		if p.Firstname == "" || p.Lastname == "" {
			return p, fmt.Errorf("กรุณากรอกชื่อและนามสกุล")
		}
		p.Name1 = ""
		p.NameEng1 = ""
	} else {
		if p.Name1 == "" {
			return p, fmt.Errorf("กรุณากรอกชื่อเจ้าหนี้")
		}
		p.Firstname = ""
		p.Lastname = ""
	}

	if err := validateSupplierCreateLengths(p); err != nil {
		return p, err
	}
	return p, nil
}

func validateSupplierCreateLengths(p normalizedSupplierCreate) error {
	limits := []struct {
		label string
		value string
		max   int
	}{
		{"รหัสเจ้าหนี้", p.Code, 25},
		{"ชื่อ", p.Name1, 100},
		{"ชื่อภาษาอังกฤษ", p.NameEng1, 100},
		{"ชื่อ", p.Firstname, 50},
		{"นามสกุล", p.Lastname, 70},
		{"ที่อยู่", p.Address, 255},
		{"หมายเหตุ", p.Remark, 255},
		{"เลขผู้เสียภาษี", p.TaxID, 50},
		{"เลขบัตรประชาชน", p.CardID, 50},
		{"รหัสสาขา", p.BranchCode, 25},
	}
	for _, item := range limits {
		if utf8.RuneCountInString(item.value) > item.max {
			return fmt.Errorf("%sยาวเกิน %d ตัวอักษร", item.label, item.max)
		}
	}
	return nil
}

func containsHiddenPartyCodeChar(value string) bool {
	for _, r := range value {
		switch r {
		case '\uFEFF', '\u200B', '\u200C', '\u200D', '\u2060':
			return true
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Mn, r) {
			return true
		}
	}
	return false
}

func normalizeSupplierModel(s *models.Supplier) {
	s.Code = strings.TrimSpace(s.Code)
	s.Name1 = strings.TrimSpace(s.Name1)
	s.NameEng1 = strings.TrimSpace(s.NameEng1)
	s.Firstname = strings.TrimSpace(s.Firstname)
	s.Lastname = strings.TrimSpace(s.Lastname)
	s.Name = strings.TrimSpace(s.Name1)
	if s.Name == "" {
		s.Name = strings.TrimSpace(strings.TrimSpace(s.Firstname + " " + s.Lastname))
	}
}
