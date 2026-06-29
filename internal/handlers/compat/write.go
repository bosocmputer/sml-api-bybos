package compat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/middleware"
	"sml-api-bybos/internal/models"
)

type WriteHandler struct {
	dbm        *db.Manager
	log        *zap.Logger
	logCacheMu sync.Mutex
	logCache   map[string]erpLogCacheEntry
}

func NewWriteHandler(dbm *db.Manager, log *zap.Logger) *WriteHandler {
	return &WriteHandler{
		dbm:      dbm,
		log:      log,
		logCache: make(map[string]erpLogCacheEntry),
	}
}

type docRoute struct {
	name      string
	transFlag int
	transType int
	itemKey   string
	partyKind string
	menuName  string
}

var (
	routeSaleOrder = docRoute{
		name: "saleorder", transFlag: models.TransFlagSaleOrder,
		transType: models.TransTypeSale, itemKey: "items", partyKind: "customer",
		menuName: "BillFlow - ใบสั่งขาย",
	}
	routeSaleInvoice = docRoute{
		name: "saleinvoice", transFlag: models.TransFlagSaleInvoice,
		transType: models.TransTypeSale, itemKey: "details", partyKind: "customer",
		menuName: "BillFlow - ขายสินค้าและบริการ",
	}
	routeCreditNote = docRoute{
		name: "creditnote", transFlag: models.TransFlagCreditNote,
		transType: models.TransTypeSale, itemKey: "details", partyKind: "customer",
		menuName: "Nexflow - ยกเลิกขายสินค้าและบริการ",
	}
	routePurchaseOrder = docRoute{
		name: "purchaseorder", transFlag: models.TransFlagPurchaseOrder,
		transType: models.TransTypePurchase, itemKey: "items", partyKind: "supplier",
		menuName: "BillFlow - ใบสั่งซื้อ",
	}
)

type erpLogCacheEntry struct {
	checkedAt time.Time
	available bool
	message   string
}

type erpLogResult struct {
	Status  string `json:"log_status"`
	Warning string `json:"log_warning,omitempty"`
}

type docPayload struct {
	DocNo          string    `json:"doc_no"`
	DocDate        string    `json:"doc_date"`
	DocTime        string    `json:"doc_time"`
	DocRef         string    `json:"doc_ref"`
	DocRefDate     string    `json:"doc_ref_date"`
	DocFormatCode  string    `json:"doc_format_code"`
	CustCode       string    `json:"cust_code"`
	SupplierName   string    `json:"supplier_name"`
	SaleCode       string    `json:"sale_code"`
	BranchCode     string    `json:"branch_code"`
	WHCode         string    `json:"wh_code"`
	ShelfCode      string    `json:"shelf_code"`
	WHFrom         string    `json:"wh_from"`
	LocationFrom   string    `json:"location_from"`
	SaleType       int       `json:"sale_type"`
	BuyType        int       `json:"buy_type"`
	VATType        int       `json:"vat_type"`
	VATRate        float64   `json:"vat_rate"`
	TotalValue     float64   `json:"total_value"`
	TotalDiscount  float64   `json:"total_discount"`
	TotalBeforeVAT float64   `json:"total_before_vat"`
	TotalVATValue  float64   `json:"total_vat_value"`
	TotalExceptVAT float64   `json:"total_except_vat"`
	TotalAfterVAT  float64   `json:"total_after_vat"`
	TotalAmount    float64   `json:"total_amount"`
	InquiryType    int       `json:"inquiry_type"`
	Remark         string    `json:"remark"`
	Remark2        string    `json:"remark_2"`
	Remark5        string    `json:"remark_5"`
	UserRequest    string    `json:"user_request"`
	Items          []docItem `json:"items"`
	Details        []docItem `json:"details"`
}

type updateCreditorPayload struct {
	CustCode            string `json:"cust_code"`
	SupplierName        string `json:"supplier_name"`
	ExpectedOldCustCode string `json:"expected_old_cust_code"`
}

type updateCreditorResult struct {
	DocNo             string `json:"doc_no"`
	OldCustCode       string `json:"old_cust_code"`
	NewCustCode       string `json:"new_cust_code"`
	SupplierName      string `json:"supplier_name,omitempty"`
	UpdatedDetailRows int64  `json:"updated_detail_rows"`
	Changed           bool   `json:"changed"`
	LogStatus         string `json:"log_status,omitempty"`
	LogWarning        string `json:"log_warning,omitempty"`
}

type updateDocRefPayload struct {
	DocRef            string `json:"doc_ref"`
	ExpectedOldDocRef string `json:"expected_old_doc_ref"`
	ExpectedRemark5   string `json:"expected_remark_5"`
	DryRun            bool   `json:"dry_run"`
}

type updateDocRefResult struct {
	DocNo             string `json:"doc_no"`
	OldDocRef         string `json:"old_doc_ref"`
	NewDocRef         string `json:"new_doc_ref"`
	OldRemark5        string `json:"old_remark_5,omitempty"`
	UpdatedDetailRows int64  `json:"updated_detail_rows"`
	Changed           bool   `json:"changed"`
	DryRun            bool   `json:"dry_run"`
	LogStatus         string `json:"log_status,omitempty"`
	LogWarning        string `json:"log_warning,omitempty"`
}

type docItem struct {
	DocRef           string  `json:"doc_ref"`
	ItemCode         string  `json:"item_code"`
	ItemName         string  `json:"item_name"`
	LineNumber       int     `json:"line_number"`
	IsPremium        int     `json:"is_permium"`
	IsGetPrice       int     `json:"is_get_price"`
	UnitCode         string  `json:"unit_code"`
	WHCode           string  `json:"wh_code"`
	ShelfCode        string  `json:"shelf_code"`
	WHCode2          string  `json:"wh_code_2"`
	ShelfCode2       string  `json:"shelf_code_2"`
	Qty              float64 `json:"qty"`
	Price            float64 `json:"price"`
	PriceExcludeVAT  float64 `json:"price_exclude_vat"`
	DiscountAmount   float64 `json:"discount_amount"`
	SumAmount        float64 `json:"sum_amount"`
	VATAmount        float64 `json:"vat_amount"`
	TotalVATValue    float64 `json:"total_vat_value"`
	TaxType          int     `json:"tax_type"`
	VATType          int     `json:"vat_type"`
	SumAmountExclVAT float64 `json:"sum_amount_exclude_vat"`
}

type createProductUnit struct {
	UnitCode    string  `json:"unit_code"`
	UnitName    string  `json:"unit_name"`
	StandValue  float64 `json:"stand_value"`
	DivideValue float64 `json:"divide_value"`
}

type createProductPriceFormula struct {
	UnitCode      string `json:"unit_code"`
	SaleType      int    `json:"sale_type"`
	Price0        string `json:"price_0"`
	TaxType       int    `json:"tax_type"`
	PriceCurrency int    `json:"price_currency"`
}

type createProductPayload struct {
	Code          string                      `json:"code"`
	Name          string                      `json:"name"`
	NameEng       string                      `json:"name_eng"`
	NameEng2      string                      `json:"name_eng_2"`
	TaxType       int                         `json:"tax_type"`
	ItemType      int                         `json:"item_type"`
	UnitType      int                         `json:"unit_type"`
	UnitCost      string                      `json:"unit_cost"`
	UnitStandard  string                      `json:"unit_standard"`
	ItemCategory  string                      `json:"item_category"`
	CategoryName  string                      `json:"category_name"`
	GroupMain     string                      `json:"group_main"`
	GroupMainName string                      `json:"group_main_name"`
	GroupSub      string                      `json:"group_sub"`
	PurchasePoint int                         `json:"purchase_point"`
	Units         []createProductUnit         `json:"units"`
	PriceFormulas []createProductPriceFormula `json:"price_formulas"`
}

func (h *WriteHandler) CreateSaleOrder(c *gin.Context) {
	h.createDocument(c, routeSaleOrder)
}

func (h *WriteHandler) CreateSaleInvoice(c *gin.Context) {
	h.createDocument(c, routeSaleInvoice)
}

func (h *WriteHandler) CreatePurchaseOrder(c *gin.Context) {
	h.createDocument(c, routePurchaseOrder)
}

func (h *WriteHandler) UpdatePurchaseOrderCreditor(c *gin.Context) {
	start := time.Now()
	docNo := strings.TrimSpace(c.Param("doc_no"))
	var p updateCreditorPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		api.BadRequest(c, "invalid_json", "invalid creditor update payload", err.Error())
		h.logWrite(c, routePurchaseOrder, docNo, 0, start, "invalid_json")
		return
	}
	p.CustCode = strings.TrimSpace(p.CustCode)
	p.SupplierName = strings.TrimSpace(p.SupplierName)
	p.ExpectedOldCustCode = strings.TrimSpace(p.ExpectedOldCustCode)
	if docNo == "" {
		api.BadRequest(c, "validation_failed", "doc_no is required", nil)
		h.logWrite(c, routePurchaseOrder, docNo, 0, start, "validation_failed")
		return
	}
	if p.CustCode == "" {
		api.BadRequest(c, "validation_failed", "supplier code (cust_code) is required", nil)
		h.logWrite(c, routePurchaseOrder, docNo, 0, start, "validation_failed")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	pool := getPool(c, h.dbm)
	if pool == nil {
		h.logWrite(c, routePurchaseOrder, docNo, 0, start, "db_pool_error")
		return
	}

	result, err := updatePurchaseOrderCreditor(ctx, pool, docNo, p)
	if err != nil {
		var ae *appError
		if errors.As(err, &ae) {
			writeAppError(c, ae)
			h.logWrite(c, routePurchaseOrder, docNo, 0, start, ae.Code)
			return
		}
		api.Internal(c, "purchase_order_creditor_update_failed", "update purchase order creditor failed", err.Error())
		h.logWrite(c, routePurchaseOrder, docNo, 0, start, "purchase_order_creditor_update_failed")
		return
	}

	logResult := h.updateERPLogCreditor(c, docNo, routePurchaseOrder, p.CustCode, result.Changed)
	result.LogStatus = logResult.Status
	result.LogWarning = logResult.Warning
	api.OK(c, result)
	h.logWrite(c, routePurchaseOrder, docNo, int(result.UpdatedDetailRows)+1, start, "")
}

func (h *WriteHandler) createDocument(c *gin.Context, route docRoute) {
	start := time.Now()
	var p docPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		api.BadRequest(c, "invalid_json", "invalid request body", err.Error())
		h.logWrite(c, route, p.DocNo, 0, start, "invalid_json")
		return
	}
	items := p.Items
	if route.itemKey == "details" {
		items = p.Details
	}
	if err := normalizeAndValidate(&p, items, route); err != nil {
		api.BadRequest(c, "validation_failed", err.Error(), nil)
		h.logWrite(c, route, p.DocNo, 0, start, "validation_failed")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	pool := getPool(c, h.dbm)
	if pool == nil {
		h.logWrite(c, route, p.DocNo, 0, start, "db_pool_error")
		return
	}

	rows, existing, err := h.insertDocument(ctx, pool, p, items, route)
	if err != nil {
		var ae *appError
		if errors.As(err, &ae) {
			writeAppError(c, ae)
			h.logWrite(c, route, p.DocNo, rows, start, ae.Code)
			return
		}
		api.Internal(c, "document_write_failed", "write SML document failed", err.Error())
		h.logWrite(c, route, p.DocNo, rows, start, "document_write_failed")
		return
	}
	if existing {
		api.OK(c, gin.H{"doc_no": p.DocNo, "status": "already_exists", "log_status": "skipped"})
		h.logWrite(c, route, p.DocNo, rows, start, "already_exists")
		return
	}
	logResult := h.writeERPLog(c, p, route)
	api.Created(c, gin.H{
		"doc_no":       p.DocNo,
		"status":       "created",
		"rows_written": rows,
		"log_status":   logResult.Status,
		"log_warning":  logResult.Warning,
	})
	h.logWrite(c, route, p.DocNo, rows, start, "")
}

func normalizeAndValidate(p *docPayload, items []docItem, route docRoute) error {
	p.DocNo = strings.TrimSpace(p.DocNo)
	p.DocDate = strings.TrimSpace(p.DocDate)
	p.DocTime = strings.TrimSpace(p.DocTime)
	p.DocFormatCode = strings.TrimSpace(p.DocFormatCode)
	p.CustCode = strings.TrimSpace(p.CustCode)
	p.BranchCode = strings.TrimSpace(p.BranchCode)
	p.WHCode = firstNonEmpty(p.WHCode, p.WHFrom)
	p.ShelfCode = firstNonEmpty(p.ShelfCode, p.LocationFrom)
	if p.DocNo == "" {
		return fmt.Errorf("doc_no is required")
	}
	if _, err := time.Parse("2006-01-02", p.DocDate); err != nil {
		return fmt.Errorf("doc_date format must be YYYY-MM-DD")
	}
	if p.DocTime == "" {
		return fmt.Errorf("doc_time is required")
	}
	if len(p.DocTime) > 5 {
		return fmt.Errorf("doc_time format must be HH:MM")
	}
	if p.DocFormatCode == "" {
		return fmt.Errorf("doc_format_code is required")
	}
	if p.CustCode == "" {
		if route.partyKind == "supplier" {
			return fmt.Errorf("supplier code (cust_code) is required")
		}
		return fmt.Errorf("customer code (cust_code) is required")
	}
	if p.VATRate < 0 {
		return fmt.Errorf("vat_rate must be >= 0")
	}
	if p.VATType < 0 || p.VATType > 2 {
		return fmt.Errorf("vat_type must be 0, 1, or 2")
	}
	if len(items) == 0 {
		return fmt.Errorf("%s must contain at least one item", route.itemKey)
	}
	for i := range items {
		if strings.TrimSpace(items[i].ItemCode) == "" {
			return fmt.Errorf("item %d item_code is required", i)
		}
		if strings.TrimSpace(items[i].UnitCode) == "" {
			return fmt.Errorf("item %d unit_code is required", i)
		}
		if items[i].Qty <= 0 {
			return fmt.Errorf("item %d qty must be > 0", i)
		}
		if items[i].Price < 0 {
			return fmt.Errorf("item %d price must be >= 0", i)
		}
	}
	return nil
}

type erpLogPool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (h *WriteHandler) writeERPLog(c *gin.Context, p docPayload, route docRoute) erpLogResult {
	tenant := strings.TrimSpace(c.GetString(middleware.TenantKey))
	if tenant == "" {
		return erpLogResult{Status: "warning", Warning: "บันทึก SML erp_logs ไม่สำเร็จ: ไม่พบ tenant ของร้าน"}
	}
	logDB := tenant + "_logs"
	if cached, ok := h.cachedERPLogAvailability(logDB); ok && !cached.available {
		return erpLogResult{Status: "warning", Warning: cached.message}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	pool, err := h.dbm.Get(ctx, logDB)
	if err != nil {
		msg := erpLogWarningMessage(logDB, err)
		h.setERPLogAvailability(logDB, false, msg)
		if h.log != nil {
			h.log.Warn("sml_erp_log_pool_unavailable",
				zap.String("tenant", tenant),
				zap.String("logs_db", logDB),
				zap.Error(err),
			)
		}
		return erpLogResult{Status: "warning", Warning: msg}
	}
	status, err := insertERPLog(ctx, pool, p, route)
	if err != nil {
		msg := erpLogWarningMessage(logDB, err)
		h.setERPLogAvailability(logDB, false, msg)
		if h.log != nil {
			h.log.Warn("sml_erp_log_write_failed",
				zap.String("tenant", tenant),
				zap.String("logs_db", logDB),
				zap.String("doc_no", p.DocNo),
				zap.Int("trans_flag", route.transFlag),
				zap.Error(err),
			)
		}
		return erpLogResult{Status: "warning", Warning: msg}
	}
	h.setERPLogAvailability(logDB, true, "")
	return erpLogResult{Status: status}
}

func insertERPLog(ctx context.Context, pool erpLogPool, p docPayload, route docRoute) (string, error) {
	docDate, err := time.Parse("2006-01-02", p.DocDate)
	if err != nil {
		return "warning", fmt.Errorf("parse doc_date for erp_logs: %w", err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM erp_logs
			WHERE doc_no=$1 AND trans_flag=$2 AND function_code=1
			  AND COALESCE(menu_name,'') LIKE 'BillFlow%'
			LIMIT 1
		)`, p.DocNo, route.transFlag).Scan(&exists); err != nil {
		return "warning", fmt.Errorf("check duplicate erp_logs: %w", err)
	}
	if exists {
		return "skipped", nil
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO erp_logs (
			doc_no, doc_date, doc_time, cust_code, user_code, date_time,
			trans_flag, trans_type, computer_name, function_code, menu_name,
			doc_amount
		) VALUES (
			$1,$2,$3,$4,$5,NOW(),
			$6,$7,$8,$9,$10,
			$11
		)`,
		p.DocNo, docDate, p.DocTime, p.CustCode, "BILLFLOW",
		route.transFlag, route.transType, "BILLFLOW", 1, route.menuName,
		p.TotalAmount,
	)
	if err != nil {
		return "warning", fmt.Errorf("insert erp_logs: %w", err)
	}
	return "created", nil
}

func (h *WriteHandler) cachedERPLogAvailability(logDB string) (erpLogCacheEntry, bool) {
	h.logCacheMu.Lock()
	defer h.logCacheMu.Unlock()
	entry, ok := h.logCache[logDB]
	if !ok || time.Since(entry.checkedAt) > time.Minute {
		return erpLogCacheEntry{}, false
	}
	return entry, true
}

func (h *WriteHandler) setERPLogAvailability(logDB string, available bool, message string) {
	h.logCacheMu.Lock()
	defer h.logCacheMu.Unlock()
	h.logCache[logDB] = erpLogCacheEntry{
		checkedAt: time.Now(),
		available: available,
		message:   message,
	}
}

func erpLogWarningMessage(logDB string, err error) string {
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "does not exist"):
		return fmt.Sprintf("บันทึก SML erp_logs ไม่สำเร็จ: ไม่พบฐานข้อมูล %s", logDB)
	case strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "timeout"):
		return "บันทึก SML erp_logs ไม่สำเร็จ: ฐานข้อมูล logs ตอบช้าเกินกำหนด"
	case strings.Contains(lower, "connection refused") || strings.Contains(lower, "no route to host"):
		return "บันทึก SML erp_logs ไม่สำเร็จ: เชื่อมต่อฐานข้อมูล logs ไม่ได้"
	default:
		return "บันทึก SML erp_logs ไม่สำเร็จ: ตรวจฐานข้อมูล logs ของร้านนี้"
	}
}

func (h *WriteHandler) insertDocument(ctx context.Context, pool txBeginner, p docPayload, items []docItem, route docRoute) (int, bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	exists, err := docExists(ctx, tx, p.DocNo, route.transFlag)
	if err != nil {
		return 0, false, err
	}
	if exists {
		return 0, true, nil
	}
	if err := validateRefs(ctx, tx, p, items, route); err != nil {
		return 0, false, err
	}

	docDate, _ := time.Parse("2006-01-02", p.DocDate)
	var docRefDate interface{}
	if strings.TrimSpace(p.DocRefDate) != "" {
		d, err := time.Parse("2006-01-02", p.DocRefDate)
		if err != nil {
			return 0, false, newAppError(http.StatusBadRequest, "invalid_doc_ref_date", "doc_ref_date format must be YYYY-MM-DD", nil)
		}
		docRefDate = d
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO ic_trans (
			trans_type, trans_flag, doc_date, doc_no, doc_time, doc_format_code,
			cust_code, branch_code, sale_code,
			wh_from, location_from,
			vat_type, vat_rate,
			total_value, total_vat_value, total_after_vat, total_amount,
			total_before_vat, total_discount, discount_word, total_except_vat,
			doc_ref, doc_ref_date, inquiry_type, remark, remark_2, remark_5, user_request, last_status
		) VALUES (
			$1,$2,$3,$4,$5,$6,
			$7,$8,$9,
			$10,$11,
			$12,$13,
			$14,$15,$16,$17,
			$18,$19,$20,$21,
			$22,$23,$24,$25,$26,$27,$28,0
		)`,
		route.transType, route.transFlag, docDate, p.DocNo, p.DocTime, p.DocFormatCode,
		p.CustCode, p.BranchCode, p.SaleCode,
		p.WHCode, p.ShelfCode,
		p.VATType, p.VATRate,
		p.TotalValue, p.TotalVATValue, p.TotalAfterVAT, p.TotalAmount,
		p.TotalBeforeVAT, p.TotalDiscount, headerDiscountWord(p.TotalDiscount), p.TotalExceptVAT,
		p.DocRef, docRefDate, p.InquiryType, p.Remark, p.Remark2, p.Remark5, p.UserRequest,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, false, newAppError(http.StatusConflict, "duplicate_doc_no", fmt.Sprintf("doc_no '%s' already exists", p.DocNo), nil)
		}
		return 0, false, fmt.Errorf("insert header: %w", err)
	}
	rowsWritten := 1

	for i, it := range items {
		lineNo := it.LineNumber
		if lineNo == 0 {
			lineNo = i
		}
		wh := firstNonEmpty(it.WHCode, p.WHCode)
		shelf := firstNonEmpty(it.ShelfCode, p.ShelfCode)
		wh2 := firstNonEmpty(it.WHCode2, wh)
		shelf2 := firstNonEmpty(it.ShelfCode2, shelf)
		vatValue := it.TotalVATValue
		if vatValue == 0 {
			vatValue = it.VATAmount
		}
		sumAmount := it.SumAmount
		if sumAmount == 0 {
			sumAmount = round2(it.Qty * it.Price)
		}
		sumExc := it.SumAmountExclVAT
		if sumExc == 0 {
			sumExc = sumAmount
		}
		priceExc := it.PriceExcludeVAT
		if priceExc == 0 {
			priceExc = it.Price
		}
		docRef := firstNonEmpty(it.DocRef, p.DocRef)
		isGetPrice := it.IsGetPrice
		if isGetPrice == 0 {
			isGetPrice = 1
		}

		// calc_flag: sale side = -1, purchase side = 1 (SML convention)
		calcFlag := 1
		if route.transType == models.TransTypeSale {
			calcFlag = -1
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO ic_trans_detail (
				trans_type, trans_flag, doc_date, doc_no, line_number,
				cust_code, doc_time, calc_flag, inquiry_type,
				item_code, item_name, unit_code, is_permium, is_get_price,
				wh_code, shelf_code, wh_code_2, shelf_code_2,
				qty, price, price_exclude_vat,
				discount_amount, discount, total_vat_value,
				sum_amount, sum_amount_exclude_vat,
				tax_type, vat_type,
				doc_ref, branch_code, last_status
			) VALUES (
				$1,$2,$3,$4,$5,
				$6,$7,$8,$9,
				$10,$11,$12,$13,$14,
				$15,$16,$17,$18,
				$19,$20,$21,
				$22,$23,$24,
				$25,$26,
				$27,$28,
				$29,$30,0
			)`,
			route.transType, route.transFlag, docDate, p.DocNo, lineNo,
			p.CustCode, p.DocTime, calcFlag, p.InquiryType,
			strings.TrimSpace(it.ItemCode), it.ItemName, strings.TrimSpace(it.UnitCode), it.IsPremium, isGetPrice,
			wh, shelf, wh2, shelf2,
			it.Qty, it.Price, priceExc,
			it.DiscountAmount, fmt.Sprintf("%v", it.DiscountAmount), vatValue,
			sumAmount, sumExc,
			it.TaxType, p.VATType,
			docRef, p.BranchCode,
		)
		if err != nil {
			return rowsWritten, false, fmt.Errorf("insert item %d: %w", i, err)
		}
		rowsWritten++
	}

	if err := normalizeInsertedDocument(ctx, tx, p.DocNo, route.transFlag); err != nil {
		return rowsWritten, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return rowsWritten, false, fmt.Errorf("commit: %w", err)
	}
	return rowsWritten, false, nil
}

func updatePurchaseOrderCreditor(ctx context.Context, pool txBeginner, docNo string, p updateCreditorPayload) (updateCreditorResult, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return updateCreditorResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var current string
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(cust_code, '')
		FROM ic_trans
		WHERE doc_no=$1 AND trans_flag=$2 AND last_status=0
		FOR UPDATE`, docNo, models.TransFlagPurchaseOrder).Scan(&current)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			var otherFlag int
			otherErr := tx.QueryRow(ctx, `
				SELECT trans_flag
				FROM ic_trans
				WHERE doc_no=$1 AND last_status=0
				ORDER BY trans_flag
				LIMIT 1`, docNo).Scan(&otherFlag)
			if otherErr == nil {
				return updateCreditorResult{}, newAppError(
					http.StatusBadRequest,
					"not_purchase_order",
					fmt.Sprintf("doc_no '%s' is trans_flag %d, not purchase order", docNo, otherFlag),
					gin.H{"doc_no": docNo, "trans_flag": otherFlag},
				)
			}
			if errors.Is(otherErr, pgx.ErrNoRows) {
				return updateCreditorResult{}, newAppError(
					http.StatusNotFound,
					"purchase_order_not_found",
					fmt.Sprintf("purchase order not found: %s", docNo),
					gin.H{"doc_no": docNo},
				)
			}
			return updateCreditorResult{}, fmt.Errorf("lookup active document: %w", otherErr)
		}
		return updateCreditorResult{}, fmt.Errorf("lookup purchase order: %w", err)
	}

	if p.ExpectedOldCustCode != "" && p.ExpectedOldCustCode != current {
		return updateCreditorResult{}, newAppError(
			http.StatusConflict,
			"creditor_changed",
			"purchase order creditor changed before this request",
			gin.H{"doc_no": docNo, "expected_old_cust_code": p.ExpectedOldCustCode, "actual_old_cust_code": current},
		)
	}

	var supplierExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ap_supplier WHERE code=$1 AND status=0)`, p.CustCode).Scan(&supplierExists); err != nil {
		return updateCreditorResult{}, fmt.Errorf("validate supplier: %w", err)
	}
	if !supplierExists {
		return updateCreditorResult{}, newAppError(
			http.StatusBadRequest,
			"supplier_not_found",
			"supplier not found: "+p.CustCode,
			gin.H{"cust_code": p.CustCode},
		)
	}

	result := updateCreditorResult{
		DocNo:        docNo,
		OldCustCode:  current,
		NewCustCode:  p.CustCode,
		SupplierName: p.SupplierName,
		Changed:      current != p.CustCode,
	}
	if result.Changed {
		if _, err := tx.Exec(ctx, `
			UPDATE ic_trans
			SET cust_code=$1
			WHERE doc_no=$2 AND trans_flag=$3 AND last_status=0`,
			p.CustCode, docNo, models.TransFlagPurchaseOrder); err != nil {
			return updateCreditorResult{}, fmt.Errorf("update purchase order header: %w", err)
		}
		tag, err := tx.Exec(ctx, `
			UPDATE ic_trans_detail
			SET cust_code=$1
			WHERE doc_no=$2 AND trans_flag=$3 AND last_status=0`,
			p.CustCode, docNo, models.TransFlagPurchaseOrder)
		if err != nil {
			return updateCreditorResult{}, fmt.Errorf("update purchase order detail: %w", err)
		}
		result.UpdatedDetailRows = tag.RowsAffected()
	}

	if err := tx.Commit(ctx); err != nil {
		return updateCreditorResult{}, fmt.Errorf("commit creditor update: %w", err)
	}
	return result, nil
}

func (h *WriteHandler) updateERPLogCreditor(c *gin.Context, docNo string, route docRoute, custCode string, changed bool) erpLogResult {
	if !changed {
		return erpLogResult{Status: "skipped"}
	}
	tenant := strings.TrimSpace(c.GetString(middleware.TenantKey))
	if tenant == "" {
		return erpLogResult{Status: "warning", Warning: "อัปเดต SML erp_logs ไม่สำเร็จ: ไม่พบ tenant ของร้าน"}
	}
	logDB := tenant + "_logs"

	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	pool, err := h.dbm.Get(ctx, logDB)
	if err != nil {
		msg := erpLogWarningMessage(logDB, err)
		if h.log != nil {
			h.log.Warn("sml_erp_log_creditor_update_pool_unavailable",
				zap.String("tenant", tenant),
				zap.String("logs_db", logDB),
				zap.String("doc_no", docNo),
				zap.Error(err),
			)
		}
		return erpLogResult{Status: "warning", Warning: msg}
	}
	status, err := updateERPLogCreditor(ctx, pool, docNo, route.transFlag, custCode)
	if err != nil {
		msg := erpLogWarningMessage(logDB, err)
		if h.log != nil {
			h.log.Warn("sml_erp_log_creditor_update_failed",
				zap.String("tenant", tenant),
				zap.String("logs_db", logDB),
				zap.String("doc_no", docNo),
				zap.Int("trans_flag", route.transFlag),
				zap.Error(err),
			)
		}
		return erpLogResult{Status: "warning", Warning: msg}
	}
	return erpLogResult{Status: status}
}

func updateERPLogCreditor(ctx context.Context, pool erpLogPool, docNo string, transFlag int, custCode string) (string, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE erp_logs
		SET cust_code=$1
		WHERE doc_no=$2 AND trans_flag=$3 AND function_code=1
		  AND COALESCE(menu_name,'') LIKE 'BillFlow%'`,
		custCode, docNo, transFlag)
	if err != nil {
		return "warning", fmt.Errorf("update erp_logs creditor: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "skipped", nil
	}
	return "updated", nil
}

func headerDiscountWord(totalDiscount float64) string {
	if totalDiscount == 0 {
		return ""
	}
	return fmt.Sprintf("%v", totalDiscount)
}

type txBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

func docExists(ctx context.Context, tx pgx.Tx, docNo string, transFlag int) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM ic_trans WHERE doc_no=$1 AND trans_flag=$2 AND last_status=0
	)`, docNo, transFlag).Scan(&exists)
	return exists, err
}

func validateRefs(ctx context.Context, tx pgx.Tx, p docPayload, items []docItem, route docRoute) error {
	partyTable := "ar_customer"
	if route.partyKind == "supplier" {
		partyTable = "ap_supplier"
	}
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+partyTable+` WHERE code=$1 AND status=0)`, p.CustCode).Scan(&ok); err != nil {
		return fmt.Errorf("validate party: %w", err)
	}
	if !ok {
		return newAppError(http.StatusBadRequest, route.partyKind+"_not_found", route.partyKind+" not found: "+p.CustCode, nil)
	}

	if p.WHCode != "" {
		if err := tx.QueryRow(ctx, warehouseExistsSQL(), p.WHCode).Scan(&ok); err != nil {
			return fmt.Errorf("validate warehouse: %w", err)
		}
		if !ok {
			return newAppError(http.StatusBadRequest, "warehouse_not_found", "warehouse not found: "+p.WHCode, nil)
		}
	}
	if p.WHCode != "" && p.ShelfCode != "" {
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ic_shelf WHERE whcode=$1 AND code=$2 AND COALESCE(status,0)=0)`, p.WHCode, p.ShelfCode).Scan(&ok); err != nil {
			return fmt.Errorf("validate shelf: %w", err)
		}
		if !ok {
			return newAppError(http.StatusBadRequest, "shelf_not_found", fmt.Sprintf("shelf %s not found under warehouse %s", p.ShelfCode, p.WHCode), nil)
		}
	}

	for i, it := range items {
		itemCode := strings.TrimSpace(it.ItemCode)
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ic_inventory WHERE code=$1 AND status=0)`, itemCode).Scan(&ok); err != nil {
			return fmt.Errorf("validate product %s: %w", itemCode, err)
		}
		if !ok {
			return newAppError(http.StatusBadRequest, "product_not_found", fmt.Sprintf("item %d product not found: %s", i, itemCode), nil)
		}
		unitCode := strings.TrimSpace(it.UnitCode)
		if err := tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM ic_unit_use WHERE ic_code=$1 AND code=$2
			UNION
			SELECT 1 FROM ic_inventory WHERE code=$1 AND unit_standard=$2
		)`, itemCode, unitCode).Scan(&ok); err != nil {
			return fmt.Errorf("validate unit %s/%s: %w", itemCode, unitCode, err)
		}
		if !ok {
			return newAppError(http.StatusBadRequest, "unit_not_found", fmt.Sprintf("item %d unit %s not found for product %s", i, unitCode, itemCode), nil)
		}
	}
	return nil
}

func warehouseExistsSQL() string {
	return `SELECT EXISTS(SELECT 1 FROM ic_warehouse WHERE code=$1 AND COALESCE(status,0)=0)`
}

func normalizeInsertedDocument(ctx context.Context, tx pgx.Tx, docNo string, transFlag int) error {
	_, err := tx.Exec(ctx, `
		UPDATE ic_trans_detail d SET
			item_name = COALESCE(NULLIF(d.item_name,''), (
				SELECT i.name_1 FROM ic_inventory i WHERE i.code = d.item_code LIMIT 1
			)),
			stand_value = COALESCE((
				SELECT u.stand_value FROM ic_unit_use u WHERE u.ic_code = d.item_code AND u.code = d.unit_code LIMIT 1
			), d.stand_value, 1),
			divide_value = COALESCE((
				SELECT u.divide_value FROM ic_unit_use u WHERE u.ic_code = d.item_code AND u.code = d.unit_code LIMIT 1
			), d.divide_value, 1),
			doc_date_calc = d.doc_date,
			doc_time_calc = d.doc_time,
			is_serial_number = COALESCE((
				SELECT i.ic_serial_no FROM ic_inventory i WHERE i.code = d.item_code LIMIT 1
			), 0)
		WHERE d.doc_no=$1 AND d.trans_flag=$2`, docNo, transFlag)
	if err != nil {
		return fmt.Errorf("normalize detail: %w", err)
	}
	return nil
}

func (h *WriteHandler) CreateProduct(c *gin.Context) {
	var p createProductPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		api.BadRequest(c, "invalid_json", "invalid request body", err.Error())
		return
	}
	p.Code = strings.TrimSpace(p.Code)
	p.Name = strings.TrimSpace(p.Name)
	if p.Code == "" || p.Name == "" {
		api.BadRequest(c, "validation_failed", "code and name are required", nil)
		return
	}
	unit := firstNonEmpty(p.UnitStandard, p.UnitCost)
	if unit == "" && len(p.Units) > 0 {
		unit = p.Units[0].UnitCode
	}
	if unit == "" {
		api.BadRequest(c, "validation_failed", "unit_standard or unit_cost is required", nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	pool := getPool(c, h.dbm)
	if pool == nil {
		return
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		api.Internal(c, "begin_failed", "begin transaction failed", err.Error())
		return
	}
	defer tx.Rollback(ctx)

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ic_inventory WHERE code=$1 AND status=0)`, p.Code).Scan(&exists); err != nil {
		api.Internal(c, "product_lookup_failed", "check existing product failed", err.Error())
		return
	}
	if exists {
		api.Conflict(c, "duplicate_product_code", fmt.Sprintf("product code '%s' already exists", p.Code), gin.H{"code": p.Code})
		return
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO ic_inventory (
			code, name_1, name_eng_1, name_eng_2,
			tax_type, item_type, unit_type,
			unit_cost, unit_standard, group_main, status
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,0)`,
		p.Code, p.Name, p.NameEng, p.NameEng2,
		p.TaxType, p.ItemType, p.UnitType,
		firstNonEmpty(p.UnitCost, unit), unit, p.GroupMain,
	)
	if err != nil {
		if isUniqueViolation(err) {
			api.Conflict(c, "duplicate_product_code", fmt.Sprintf("product code '%s' already exists", p.Code), gin.H{"code": p.Code})
			return
		}
		api.Internal(c, "product_insert_failed", "insert product failed", err.Error())
		return
	}

	units := p.Units
	if len(units) == 0 {
		units = []createProductUnit{{UnitCode: unit, UnitName: unit, StandValue: 1, DivideValue: 1}}
	}
	for i, u := range units {
		unitCode := firstNonEmpty(u.UnitCode, unit)
		stand := u.StandValue
		if stand == 0 {
			stand = 1
		}
		divide := u.DivideValue
		if divide == 0 {
			divide = 1
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO ic_unit_use (ic_code, code, stand_value, divide_value, line_number)
			VALUES ($1,$2,$3,$4,$5)`,
			p.Code, unitCode, stand, divide, i)
		if err != nil && !isUniqueViolation(err) {
			api.Internal(c, "product_unit_insert_failed", fmt.Sprintf("insert unit %d failed", i), err.Error())
			return
		}
	}

	for i, pf := range p.PriceFormulas {
		unitCode := firstNonEmpty(pf.UnitCode, unit)
		_, err = tx.Exec(ctx, `
			INSERT INTO ic_inventory_price_formula (ic_code, unit_code, sale_type, price_0, tax_type, price_currency)
			VALUES ($1,$2,$3,$4,$5,$6)`,
			p.Code, unitCode, pf.SaleType, pf.Price0, pf.TaxType, pf.PriceCurrency,
		)
		if err != nil {
			api.Internal(c, "product_price_insert_failed", fmt.Sprintf("insert price formula %d failed", i), err.Error())
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		api.Internal(c, "commit_failed", "commit product failed", err.Error())
		return
	}
	api.Created(c, gin.H{"code": p.Code, "status": "created"})
}

type appError struct {
	Status  int
	Code    string
	Message string
	Details interface{}
}

func (e *appError) Error() string { return e.Message }

func newAppError(status int, code, message string, details interface{}) *appError {
	return &appError{Status: status, Code: code, Message: message, Details: details}
}

func writeAppError(c *gin.Context, e *appError) {
	api.Error(c, e.Status, e.Code, e.Message, e.Details)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func isUniqueViolation(err error) bool {
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

func (h *WriteHandler) UpdatePurchaseOrderDocRef(c *gin.Context) {
	start := time.Now()
	docNo := strings.TrimSpace(c.Param("doc_no"))
	var p updateDocRefPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		api.BadRequest(c, "invalid_json", "invalid doc_ref update payload", err.Error())
		h.logWrite(c, routePurchaseOrder, docNo, 0, start, "invalid_json")
		return
	}
	p.DocRef = strings.TrimSpace(p.DocRef)
	p.ExpectedOldDocRef = strings.TrimSpace(p.ExpectedOldDocRef)
	p.ExpectedRemark5 = strings.TrimSpace(p.ExpectedRemark5)
	if docNo == "" {
		api.BadRequest(c, "validation_failed", "doc_no is required", nil)
		h.logWrite(c, routePurchaseOrder, docNo, 0, start, "validation_failed")
		return
	}
	if p.DocRef == "" {
		api.BadRequest(c, "validation_failed", "doc_ref is required", nil)
		h.logWrite(c, routePurchaseOrder, docNo, 0, start, "validation_failed")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	pool := getPool(c, h.dbm)
	if pool == nil {
		h.logWrite(c, routePurchaseOrder, docNo, 0, start, "db_pool_error")
		return
	}

	result, err := updatePurchaseOrderDocRef(ctx, pool, docNo, p)
	if err != nil {
		var ae *appError
		if errors.As(err, &ae) {
			writeAppError(c, ae)
			h.logWrite(c, routePurchaseOrder, docNo, 0, start, ae.Code)
			return
		}
		api.Internal(c, "purchase_order_doc_ref_update_failed", "update purchase order doc_ref failed", err.Error())
		h.logWrite(c, routePurchaseOrder, docNo, 0, start, "purchase_order_doc_ref_update_failed")
		return
	}

	api.OK(c, result)
	h.logWrite(c, routePurchaseOrder, docNo, int(result.UpdatedDetailRows)+1, start, "")
}

func updatePurchaseOrderDocRef(ctx context.Context, pool txBeginner, docNo string, p updateDocRefPayload) (updateDocRefResult, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return updateDocRefResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var currentDocRef, currentRemark5 string
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(doc_ref, ''), COALESCE(remark_5, '')
		FROM ic_trans
		WHERE doc_no=$1 AND trans_flag=$2 AND last_status=0
		FOR UPDATE`, docNo, models.TransFlagPurchaseOrder).Scan(&currentDocRef, &currentRemark5)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			var otherFlag int
			otherErr := tx.QueryRow(ctx, `
				SELECT trans_flag
				FROM ic_trans
				WHERE doc_no=$1 AND last_status=0
				ORDER BY trans_flag
				LIMIT 1`, docNo).Scan(&otherFlag)
			if otherErr == nil {
				return updateDocRefResult{}, newAppError(
					http.StatusBadRequest,
					"not_purchase_order",
					fmt.Sprintf("doc_no '%s' is trans_flag %d, not purchase order", docNo, otherFlag),
					gin.H{"doc_no": docNo, "trans_flag": otherFlag},
				)
			}
			if errors.Is(otherErr, pgx.ErrNoRows) {
				return updateDocRefResult{}, newAppError(
					http.StatusNotFound,
					"purchase_order_not_found",
					fmt.Sprintf("purchase order not found: %s", docNo),
					gin.H{"doc_no": docNo},
				)
			}
			return updateDocRefResult{}, fmt.Errorf("lookup active document: %w", otherErr)
		}
		return updateDocRefResult{}, fmt.Errorf("lookup purchase order: %w", err)
	}

	if p.ExpectedOldDocRef != "" && p.ExpectedOldDocRef != currentDocRef {
		return updateDocRefResult{}, newAppError(
			http.StatusConflict,
			"doc_ref_changed",
			"purchase order doc_ref does not match expected value",
			gin.H{"doc_no": docNo, "expected_old_doc_ref": p.ExpectedOldDocRef, "actual_old_doc_ref": currentDocRef},
		)
	}
	if p.ExpectedRemark5 != "" && p.ExpectedRemark5 != currentRemark5 {
		return updateDocRefResult{}, newAppError(
			http.StatusConflict,
			"remark_5_mismatch",
			"purchase order remark_5 does not match expected value",
			gin.H{"doc_no": docNo, "expected_remark_5": p.ExpectedRemark5, "actual_remark_5": currentRemark5},
		)
	}

	result := updateDocRefResult{
		DocNo:      docNo,
		OldDocRef:  currentDocRef,
		NewDocRef:  p.DocRef,
		OldRemark5: currentRemark5,
		Changed:    currentDocRef != p.DocRef,
		DryRun:     p.DryRun,
	}

	if p.DryRun {
		// Roll back — no writes for dry-run.
		return result, nil
	}

	if result.Changed {
		if _, err := tx.Exec(ctx, `
			UPDATE ic_trans
			SET doc_ref=$1
			WHERE doc_no=$2 AND trans_flag=$3 AND last_status=0`,
			p.DocRef, docNo, models.TransFlagPurchaseOrder); err != nil {
			return updateDocRefResult{}, fmt.Errorf("update purchase order header doc_ref: %w", err)
		}
		tag, err := tx.Exec(ctx, `
			UPDATE ic_trans_detail
			SET doc_ref=$1
			WHERE doc_no=$2 AND trans_flag=$3 AND last_status=0`,
			p.DocRef, docNo, models.TransFlagPurchaseOrder)
		if err != nil {
			return updateDocRefResult{}, fmt.Errorf("update purchase order detail doc_ref: %w", err)
		}
		result.UpdatedDetailRows = tag.RowsAffected()
	}

	if err := tx.Commit(ctx); err != nil {
		return updateDocRefResult{}, fmt.Errorf("commit doc_ref update: %w", err)
	}
	return result, nil
}

func (h *WriteHandler) logWrite(c *gin.Context, route docRoute, docNo string, rows int, start time.Time, errCode string) {
	if h.log == nil {
		return
	}
	fields := []zap.Field{
		zap.String("request_id", c.GetString(middleware.RequestIDKey)),
		zap.String("tenant", c.GetString(middleware.TenantKey)),
		zap.String("route", route.name),
		zap.String("doc_no", docNo),
		zap.Int("trans_flag", route.transFlag),
		zap.Int("rows_written", rows),
		zap.Int64("duration_ms", time.Since(start).Milliseconds()),
	}
	if errCode != "" {
		fields = append(fields, zap.String("error_code", errCode))
		h.log.Warn("sml_document_write", fields...)
		return
	}
	h.log.Info("sml_document_write", fields...)
}
