package handlers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
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

type customerQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Begin(ctx context.Context) (customerTx, error)
}

type customerTx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

type customerPool struct {
	*pgxpool.Pool
}

func (p customerPool) Begin(ctx context.Context) (customerTx, error) {
	return p.Pool.Begin(ctx)
}

type CustomerHandler struct {
	dbm     *db.Manager
	getPool func(context.Context, string) (customerQuerier, error)
}

func NewCustomerHandler(dbm *db.Manager) *CustomerHandler {
	return &CustomerHandler{
		dbm: dbm,
		getPool: func(ctx context.Context, tenant string) (customerQuerier, error) {
			p, err := dbm.Get(ctx, tenant)
			if err != nil {
				return nil, err
			}
			return customerPool{Pool: p}, nil
		},
	}
}

type createCustomerRequest struct {
	Code       string `json:"code" binding:"required"`
	ARStatus   *int   `json:"ar_status"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	Name1      string `json:"name_1"`
	NameEng1   string `json:"name_eng_1"`
	Address    string `json:"address"`
	Remark     string `json:"remark"`
	TaxID      string `json:"tax_id"`
	BranchType *int   `json:"branch_type"`
	BranchCode string `json:"branch_code"`
	CardID     string `json:"card_id"`
}

type normalizedCustomerCreate struct {
	Code       string
	ARStatus   int
	FirstName  string
	LastName   string
	Name1      string
	NameEng1   string
	Address    string
	Remark     string
	TaxID      string
	BranchType int
	BranchCode string
	CardID     string
}

func (h *CustomerHandler) List(c *gin.Context) {
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

	baseWhere, args := customerSearchWhere(search)

	var total int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM ar_customer c LEFT JOIN ar_customer_detail d ON c.code = d.ar_code "+baseWhere, args).Scan(&total); err != nil {
		api.Internal(c, "customer_count_failed", "count customers failed", err.Error())
		return
	}

	query := `SELECT COALESCE(c.code,''), COALESCE(c.name_1,''), COALESCE(c.name_2,''), COALESCE(c.name_eng_1,''),
		COALESCE(c.first_name,''), COALESCE(c.last_name,''), COALESCE(c.telephone,''), COALESCE(c.email,''),
		COALESCE(c.address,''), COALESCE(c.remark,''), COALESCE(d.tax_id,''), COALESCE(d.card_id,''),
		COALESCE(d.branch_type,0), COALESCE(d.branch_code,''), COALESCE(c.status,0), COALESCE(c.ar_status,0)
		FROM ar_customer c
		LEFT JOIN ar_customer_detail d ON c.code = d.ar_code
		` + baseWhere + ` ORDER BY c.code LIMIT @size OFFSET @offset`
	args["size"] = size
	args["offset"] = offset

	rows, err := pool.Query(ctx, query, args)
	if err != nil {
		api.Internal(c, "customer_list_failed", "list customers failed", err.Error())
		return
	}
	defer rows.Close()

	var customers []models.Customer
	for rows.Next() {
		var cu models.Customer
		if err := rows.Scan(&cu.Code, &cu.Name1, &cu.Name2, &cu.NameEng1,
			&cu.FirstName, &cu.LastName, &cu.Telephone, &cu.Email,
			&cu.Address, &cu.Remark, &cu.TaxID, &cu.CardID,
			&cu.BranchType, &cu.BranchCode, &cu.Status, &cu.ARStatus); err != nil {
			api.Internal(c, "customer_scan_failed", "read customer row failed", err.Error())
			return
		}
		normalizeCustomerModel(&cu)
		customers = append(customers, cu)
	}
	if customers == nil {
		customers = []models.Customer{}
	}

	api.OKPage(c, customers, total, page, size)
}

func (h *CustomerHandler) Get(c *gin.Context) {
	code := c.Param("code")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	pool, err := h.getPool(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "connect to SML database failed", err.Error())
		return
	}

	var cu models.Customer
	err = pool.QueryRow(ctx,
		`SELECT COALESCE(c.code,''), COALESCE(c.name_1,''), COALESCE(c.name_2,''), COALESCE(c.name_eng_1,''),
		COALESCE(c.first_name,''), COALESCE(c.last_name,''), COALESCE(c.telephone,''), COALESCE(c.email,''),
		COALESCE(c.address,''), COALESCE(c.remark,''), COALESCE(d.tax_id,''), COALESCE(d.card_id,''),
		COALESCE(d.branch_type,0), COALESCE(d.branch_code,''), COALESCE(c.status,0), COALESCE(c.ar_status,0)
		FROM ar_customer c
		LEFT JOIN ar_customer_detail d ON c.code = d.ar_code
		WHERE c.code = $1`, code).
		Scan(&cu.Code, &cu.Name1, &cu.Name2, &cu.NameEng1,
			&cu.FirstName, &cu.LastName, &cu.Telephone, &cu.Email,
			&cu.Address, &cu.Remark, &cu.TaxID, &cu.CardID,
			&cu.BranchType, &cu.BranchCode, &cu.Status, &cu.ARStatus)
	if err != nil {
		api.NotFound(c, "customer_not_found", "customer not found")
		return
	}
	normalizeCustomerModel(&cu)
	api.OK(c, cu)
}

func (h *CustomerHandler) Create(c *gin.Context) {
	var req createCustomerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, "invalid_json", "invalid customer payload", err.Error())
		return
	}
	payload, err := normalizeCustomerCreateRequest(req)
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
		api.Internal(c, "customer_tx_failed", "start customer transaction failed", err.Error())
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx,
		`INSERT INTO ar_customer (
			code, ar_status, first_name, last_name, name_eng_1, name_1,
			address, remark, status
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,0)`,
		payload.Code, payload.ARStatus, payload.FirstName, payload.LastName, payload.NameEng1, payload.Name1,
		payload.Address, payload.Remark,
	)
	if err != nil {
		if isPGUniqueViolation(err) {
			api.Conflict(c, "duplicate_customer_code", fmt.Sprintf("customer code '%s' already exists", payload.Code), gin.H{"code": payload.Code})
			return
		}
		api.Internal(c, "customer_insert_failed", "insert customer failed", err.Error())
		return
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO ar_customer_detail (
			ar_code, tax_id, branch_type, branch_code, card_id
		) VALUES ($1,$2,$3,$4,$5)`,
		payload.Code, payload.TaxID, payload.BranchType, payload.BranchCode, payload.CardID,
	)
	if err != nil {
		if isPGUniqueViolation(err) {
			api.Conflict(c, "duplicate_customer_detail", fmt.Sprintf("customer detail for code '%s' already exists", payload.Code), gin.H{"code": payload.Code})
			return
		}
		api.Internal(c, "customer_detail_insert_failed", "insert customer detail failed", err.Error())
		return
	}

	if err := tx.Commit(ctx); err != nil {
		api.Internal(c, "customer_commit_failed", "commit customer failed", err.Error())
		return
	}

	cu := models.Customer{
		Code:       payload.Code,
		Name1:      payload.Name1,
		NameEng1:   payload.NameEng1,
		FirstName:  payload.FirstName,
		LastName:   payload.LastName,
		Address:    payload.Address,
		Remark:     payload.Remark,
		TaxID:      payload.TaxID,
		CardID:     payload.CardID,
		BranchType: payload.BranchType,
		BranchCode: payload.BranchCode,
		Status:     0,
		ARStatus:   payload.ARStatus,
	}
	normalizeCustomerModel(&cu)
	api.Created(c, cu)
}

func customerSearchWhere(search string) (string, pgx.NamedArgs) {
	baseWhere := "WHERE COALESCE(c.status,0) = 0"
	args := pgx.NamedArgs{}
	search = strings.TrimSpace(search)
	if search != "" {
		baseWhere += ` AND (
			c.code ILIKE @search OR c.name_1 ILIKE @search OR c.name_eng_1 ILIKE @search OR
			c.first_name ILIKE @search OR c.last_name ILIKE @search OR c.telephone ILIKE @search OR
			d.tax_id ILIKE @search OR d.card_id ILIKE @search
		)`
		args["search"] = "%" + search + "%"
	}
	return baseWhere, args
}

func normalizeCustomerCreateRequest(req createCustomerRequest) (normalizedCustomerCreate, error) {
	req.Code = strings.ToUpper(req.Code)
	p := normalizedCustomerCreate{
		Code:       req.Code,
		FirstName:  strings.TrimSpace(req.FirstName),
		LastName:   strings.TrimSpace(req.LastName),
		Name1:      strings.TrimSpace(req.Name1),
		NameEng1:   strings.TrimSpace(req.NameEng1),
		Address:    strings.TrimSpace(req.Address),
		Remark:     strings.TrimSpace(req.Remark),
		TaxID:      strings.TrimSpace(req.TaxID),
		BranchCode: strings.TrimSpace(req.BranchCode),
		CardID:     strings.TrimSpace(req.CardID),
	}

	if req.Code != strings.TrimSpace(req.Code) {
		return p, fmt.Errorf("รหัสลูกหนี้ห้ามมีช่องว่างหน้า/หลัง")
	}
	p.Code = strings.TrimSpace(req.Code)
	if p.Code == "" {
		return p, fmt.Errorf("กรุณากรอกรหัสลูกหนี้")
	}
	if containsHiddenPartyCodeChar(p.Code) {
		return p, fmt.Errorf("รหัสลูกหนี้มีอักขระซ่อนหรืออักขระควบคุม")
	}

	legacyPayload := req.ARStatus == nil && req.BranchType == nil &&
		p.FirstName == "" && p.LastName == "" && p.NameEng1 == "" &&
		p.Address == "" && p.Remark == "" && p.TaxID == "" &&
		p.BranchCode == "" && p.CardID == ""

	if req.ARStatus == nil {
		if !legacyPayload {
			return p, fmt.Errorf("กรุณาเลือกชนิดลูกหนี้")
		}
		p.ARStatus = 1
	} else {
		p.ARStatus = *req.ARStatus
	}
	if p.ARStatus != 0 && p.ARStatus != 1 {
		return p, fmt.Errorf("ชนิดลูกหนี้ไม่ถูกต้อง")
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

	if p.ARStatus == 0 {
		if p.FirstName == "" || p.LastName == "" {
			return p, fmt.Errorf("กรุณากรอกชื่อและนามสกุล")
		}
		p.Name1 = ""
		p.NameEng1 = ""
	} else {
		if p.Name1 == "" {
			return p, fmt.Errorf("กรุณากรอกชื่อลูกหนี้")
		}
		p.FirstName = ""
		p.LastName = ""
	}

	if err := validateCustomerCreateLengths(p); err != nil {
		return p, err
	}
	return p, nil
}

func validateCustomerCreateLengths(p normalizedCustomerCreate) error {
	limits := []struct {
		label string
		value string
		max   int
	}{
		{"รหัสลูกหนี้", p.Code, 25},
		{"ชื่อ", p.Name1, 255},
		{"ชื่อภาษาอังกฤษ", p.NameEng1, 255},
		{"ชื่อ", p.FirstName, 50},
		{"นามสกุล", p.LastName, 70},
		{"ที่อยู่", p.Address, 255},
		{"หมายเหตุ", p.Remark, 255},
		{"เลขผู้เสียภาษี", p.TaxID, 50},
		{"เลขบัตรประชาชน", p.CardID, 100},
		{"รหัสสาขา", p.BranchCode, 25},
	}
	for _, item := range limits {
		if utf8.RuneCountInString(item.value) > item.max {
			return fmt.Errorf("%sยาวเกิน %d ตัวอักษร", item.label, item.max)
		}
	}
	return nil
}

func normalizeCustomerModel(c *models.Customer) {
	c.Code = strings.TrimSpace(c.Code)
	c.Name1 = strings.TrimSpace(c.Name1)
	c.NameEng1 = strings.TrimSpace(c.NameEng1)
	c.FirstName = strings.TrimSpace(c.FirstName)
	c.LastName = strings.TrimSpace(c.LastName)
	c.Name = strings.TrimSpace(c.Name1)
	if c.Name == "" {
		c.Name = strings.TrimSpace(strings.TrimSpace(c.FirstName + " " + c.LastName))
	}
}

func isPGUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key") || strings.Contains(msg, "unique constraint")
}
