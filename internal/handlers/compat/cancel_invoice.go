package compat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/middleware"
	"sml-api-bybos/internal/models"
)

type saleInvoiceCancelRequest struct {
	DocumentProfileVersion string `json:"document_profile_version"`
	DocNo                  string `json:"doc_no"`
	DocDate                string `json:"doc_date"`
	DocTime                string `json:"doc_time"`
	DocFormatCode          string `json:"doc_format_code"`
	Remark                 string `json:"remark"`
	Remark2                string `json:"remark_2"`
	Remark5                string `json:"remark_5"`
	CreatorCode            string `json:"creator_code"`
	CashierCode            string `json:"cashier_code"`
	UserRequest            string `json:"user_request"`
}

type saleInvoiceCancelKind string

const (
	saleInvoiceCancelKindSaleOrder  saleInvoiceCancelKind = "sale_order_cancel"
	saleInvoiceCancelKindVoid       saleInvoiceCancelKind = "sale_invoice_cancel"
	saleInvoiceCancelKindCreditNote saleInvoiceCancelKind = "credit_note"
)

type saleInvoiceCancelPreview struct {
	Status                 string                         `json:"status"`
	Kind                   saleInvoiceCancelKind          `json:"kind"`
	SaleDocNo              string                         `json:"sale_doc_no"`
	CancelDocNo            string                         `json:"cancel_doc_no,omitempty"`
	ExistingCancelDocNo    string                         `json:"existing_cancel_doc_no,omitempty"`
	TransFlag              int                            `json:"trans_flag"`
	DocFormatCode          string                         `json:"doc_format_code"`
	DocDate                string                         `json:"doc_date"`
	CustCode               string                         `json:"cust_code"`
	TotalAmount            float64                        `json:"total_amount"`
	TotalValue             float64                        `json:"total_value"`
	TotalVATValue          float64                        `json:"total_vat_value"`
	TotalAfterVAT          float64                        `json:"total_after_vat"`
	ItemCount              int                            `json:"item_count"`
	Items                  []saleInvoiceCancelPreviewItem `json:"items"`
	SourceTotalAmount      float64                        `json:"source_total_amount"`
	SourceItemCount        int                            `json:"source_item_count"`
	Message                string                         `json:"message,omitempty"`
	LogStatus              string                         `json:"log_status,omitempty"`
	LogWarning             string                         `json:"log_warning,omitempty"`
	PayloadHash            string                         `json:"payload_hash,omitempty"`
	CoreStatus             string                         `json:"core_status,omitempty"`
	ProfileStatus          string                         `json:"profile_status,omitempty"`
	RequiredChecks         []string                       `json:"required_checks,omitempty"`
	CompletedChecks        []string                       `json:"completed_checks,omitempty"`
	ReconciliationRequired bool                           `json:"reconciliation_required"`
	profilePayload         *docPayload                    `json:"-"`
}

type cancellationLockExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func acquireCancellationSourceLock(ctx context.Context, tx cancellationLockExecutor, sourceTransFlag int, sourceDocNo string) error {
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '3s'`); err != nil {
		return cancellationLockError(err, sourceDocNo)
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`,
		fmt.Sprintf("sml-sales-cancellation:%d:%s", sourceTransFlag, strings.TrimSpace(sourceDocNo)))
	if err != nil {
		return cancellationLockError(err, sourceDocNo)
	}
	return nil
}

func cancellationLockError(err error, sourceDocNo string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
		return newAppError(http.StatusConflict, "document_busy", "another cancellation request is processing this source document", gin.H{"source_doc_no": sourceDocNo})
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return newAppError(http.StatusConflict, "document_busy", "another cancellation request is processing this source document", gin.H{"source_doc_no": sourceDocNo})
	}
	return fmt.Errorf("lock cancellation source document: %w", err)
}

type saleInvoiceCancelPreviewItem struct {
	LineNumber      int     `json:"line_number"`
	ItemCode        string  `json:"item_code"`
	ItemName        string  `json:"item_name"`
	UnitCode        string  `json:"unit_code"`
	Qty             float64 `json:"qty"`
	Price           float64 `json:"price"`
	SumAmount       float64 `json:"sum_amount"`
	RefDocNo        string  `json:"ref_doc_no"`
	RefLineNumber   int     `json:"ref_line_number"`
	DocRefType      int     `json:"doc_ref_type"`
	PriceExcludeVAT float64 `json:"price_exclude_vat"`
	SumExcludeVAT   float64 `json:"sum_amount_exclude_vat"`
}

type saleInvoiceForCancel struct {
	DocNo                                                           string
	DocDate                                                         time.Time
	DocTime                                                         string
	DocFormatCode                                                   string
	CustCode                                                        string
	BranchCode                                                      string
	SaleCode                                                        string
	WHFrom                                                          string
	LocationFrom                                                    string
	VATType                                                         int
	VATRate                                                         float64
	TotalValue                                                      float64
	TotalVATValue                                                   float64
	TotalAfterVAT                                                   float64
	TotalAmount                                                     float64
	TotalBeforeVAT                                                  float64
	TotalDiscount                                                   float64
	TotalExceptVAT                                                  float64
	InquiryType                                                     int
	UsedStatus                                                      int
	VATRateDecimal, TotalValueDecimal, TotalVATValueDecimal         string
	TotalAfterVATDecimal, TotalAmountDecimal, TotalBeforeVATDecimal string
	TotalDiscountDecimal, TotalExceptVATDecimal                     string
	Items                                                           []saleInvoiceCancelLine
}

type saleInvoiceCancelLine struct {
	LineNumber                                       int
	ItemCode                                         string
	ItemName                                         string
	UnitCode                                         string
	IsPremium                                        int
	IsGetPrice                                       int
	WHCode                                           string
	ShelfCode                                        string
	WHCode2                                          string
	ShelfCode2                                       string
	Qty                                              float64
	Price                                            float64
	PriceExcludeVAT                                  float64
	DiscountAmount                                   float64
	Discount                                         string
	TotalVATValue                                    float64
	SumAmount                                        float64
	SumAmountExclVAT                                 float64
	TaxType                                          int
	VATType                                          int
	ItemType                                         int
	RefGUID                                          string
	SetRefPrice                                      float64
	SetRefQty                                        float64
	ItemCodeMain                                     string
	SetRefLine                                       string
	PriceSetRatio                                    float64
	BranchCode                                       string
	QtyDecimal, PriceDecimal, PriceExcludeVATDecimal string
	DiscountAmountDecimal, TotalVATValueDecimal      string
	SumAmountDecimal, SumAmountExclVATDecimal        string
}

func saleInvoiceCancellationProfilePayload(tenant string, src saleInvoiceForCancel, req saleInvoiceCancelRequest, route docRoute) (docPayload, error) {
	if route.name != routeSaleInvoiceCancel.name && route.name != routeCreditNote.name {
		return docPayload{}, fmt.Errorf("unsupported invoice cancellation profile route %s", route.name)
	}
	defaultFormat := "CN"
	if route.name == routeSaleInvoiceCancel.name {
		defaultFormat = "SIC"
	}
	remark := strings.TrimSpace(req.Remark)
	if remark == "" {
		remark = "รับคืนสินค้า/ลดหนี้จาก Nexflow"
		if route.name == routeSaleInvoiceCancel.name {
			remark = "ยกเลิกขายสินค้าและบริการจาก Nexflow"
		}
	}
	docDate, docTime, docFormat := normalizedCancellationDocFields(req, defaultFormat)
	p := docPayload{
		DocumentProfileVersion: req.DocumentProfileVersion,
		ShipmentApplicability:  "not_applicable",
		DocNo:                  req.DocNo,
		DocDate:                docDate.Format("2006-01-02"),
		DocTime:                docTime,
		DocFormatCode:          docFormat,
		DocRef:                 src.DocNo,
		DocRefDate:             src.DocDate.Format("2006-01-02"),
		CustCode:               src.CustCode,
		BranchCode:             src.BranchCode,
		SaleCode:               src.SaleCode,
		WHCode:                 src.WHFrom,
		ShelfCode:              src.LocationFrom,
		VATType:                src.VATType,
		InquiryType:            src.InquiryType,
		Remark:                 remark,
		Remark2:                req.Remark2,
		Remark5:                req.Remark5,
		CreatorCode:            req.CreatorCode,
		CashierCode:            req.CashierCode,
		UserRequest:            req.UserRequest,
		CurrencyCode:           "THB",
		ExchangeRateDecimal:    "1",
	}
	if route.name == routeCreditNote.name {
		p.VATRateDecimal, p.VATRate = sourceExactDecimal(src.VATRateDecimal, src.VATRate)
		p.TotalValueDecimal, p.TotalValue = sourceExactDecimal(src.TotalValueDecimal, src.TotalValue)
		p.TotalVATValueDecimal, p.TotalVATValue = sourceExactDecimal(src.TotalVATValueDecimal, src.TotalVATValue)
		p.TotalAfterVATDecimal, p.TotalAfterVAT = sourceExactDecimal(src.TotalAfterVATDecimal, src.TotalAfterVAT)
		p.TotalAmountDecimal, p.TotalAmount = sourceExactDecimal(src.TotalAmountDecimal, src.TotalAmount)
		p.TotalBeforeVATDecimal, p.TotalBeforeVAT = sourceExactDecimal(src.TotalBeforeVATDecimal, src.TotalBeforeVAT)
		p.TotalDiscountDecimal, p.TotalDiscount = sourceExactDecimal(src.TotalDiscountDecimal, src.TotalDiscount)
		p.TotalExceptVATDecimal, p.TotalExceptVAT = sourceExactDecimal(src.TotalExceptVATDecimal, src.TotalExceptVAT)
		for _, line := range src.Items {
			item := docItem{
				LineNumber: line.LineNumber, ItemCode: line.ItemCode, ItemName: line.ItemName, UnitCode: line.UnitCode,
				WHCode: line.WHCode, ShelfCode: line.ShelfCode, WHCode2: line.WHCode2, ShelfCode2: line.ShelfCode2,
				RefDocNo: src.DocNo, RefLineNumber: line.LineNumber, DocRefType: 1, BranchCode: line.BranchCode,
				IsPremium: line.IsPremium, IsGetPrice: line.IsGetPrice, TaxType: line.TaxType, VATType: line.VATType,
			}
			item.QtyDecimal, item.Qty = sourceExactDecimal(line.QtyDecimal, line.Qty)
			item.PriceDecimal, item.Price = sourceExactDecimal(line.PriceDecimal, line.Price)
			item.PriceExcludeVATDecimal, item.PriceExcludeVAT = sourceExactDecimal(line.PriceExcludeVATDecimal, line.PriceExcludeVAT)
			item.DiscountAmountDecimal, item.DiscountAmount = sourceExactDecimal(line.DiscountAmountDecimal, line.DiscountAmount)
			item.SumAmountDecimal, item.SumAmount = sourceExactDecimal(line.SumAmountDecimal, line.SumAmount)
			item.VATAmountDecimal, item.VATAmount = sourceExactDecimal(line.TotalVATValueDecimal, line.TotalVATValue)
			item.TotalVATValue = item.VATAmount
			item.SumAmountExclVATDecimal, item.SumAmountExclVAT = sourceExactDecimal(line.SumAmountExclVATDecimal, line.SumAmountExclVAT)
			p.Details = append(p.Details, item)
		}
	} else {
		p.VATRateDecimal = "0"
		p.TotalValueDecimal = "0"
		p.TotalVATValueDecimal = "0"
		p.TotalAfterVATDecimal = "0"
		p.TotalAmountDecimal = "0"
		p.TotalBeforeVATDecimal = "0"
		p.TotalDiscountDecimal = "0"
		p.TotalExceptVATDecimal = "0"
	}
	if req.DocumentProfileVersion != documentProfileV1 {
		return p, nil
	}
	if err := normalizeAndValidateProfile(&p, p.Details, route); err != nil {
		return docPayload{}, err
	}
	baseHash, err := canonicalProfileHash(tenant, p, p.Details, route)
	if err != nil {
		return docPayload{}, err
	}
	p.ProfilePayloadHash = cancellationIntentProfileHash(tenant, models.TransFlagSaleInvoice, src.DocNo, route.transFlag, baseHash)
	return p, nil
}

func sourceExactDecimal(exact string, numeric float64) (string, float64) {
	exact = strings.TrimSpace(exact)
	if exact == "" {
		exact = strconv.FormatFloat(numeric, 'f', -1, 64)
	}
	parsed, err := strconv.ParseFloat(exact, 64)
	if err != nil {
		return exact, numeric
	}
	return exact, parsed
}

func creditNoteReferenceAmounts(src saleInvoiceForCancel) (string, string) {
	refAmount, _ := sourceExactDecimal(src.TotalValueDecimal, src.TotalValue)
	receivableAmount, _ := sourceExactDecimal(src.TotalAmountDecimal, src.TotalAmount)
	return refAmount, receivableAmount
}

func saleInvoiceCancellationPreviewFromSource(src saleInvoiceForCancel, req saleInvoiceCancelRequest, p docPayload, route docRoute) saleInvoiceCancelPreview {
	kind := saleInvoiceCancelKindCreditNote
	if route.name == routeSaleInvoiceCancel.name {
		kind = saleInvoiceCancelKindVoid
	}
	_, sourceTotalAmount := sourceExactDecimal(src.TotalAmountDecimal, src.TotalAmount)
	result := saleInvoiceCancelPreview{
		Status: "ready", Kind: kind, SaleDocNo: src.DocNo, CancelDocNo: req.DocNo,
		TransFlag: route.transFlag, DocFormatCode: p.DocFormatCode, DocDate: p.DocDate,
		CustCode: src.CustCode, ItemCount: len(p.Details), SourceTotalAmount: sourceTotalAmount,
		SourceItemCount: len(src.Items), PayloadHash: p.ProfilePayloadHash,
	}
	result.TotalAmount = p.TotalAmount
	result.TotalValue = p.TotalValue
	result.TotalVATValue = p.TotalVATValue
	result.TotalAfterVAT = p.TotalAfterVAT
	for _, item := range p.Details {
		result.Items = append(result.Items, saleInvoiceCancelPreviewItem{
			LineNumber: item.LineNumber, ItemCode: item.ItemCode, ItemName: item.ItemName,
			UnitCode: item.UnitCode, Qty: item.Qty, Price: item.Price, SumAmount: item.SumAmount,
			RefDocNo: item.RefDocNo, RefLineNumber: item.RefLineNumber, DocRefType: item.DocRefType,
			PriceExcludeVAT: item.PriceExcludeVAT, SumExcludeVAT: item.SumAmountExclVAT,
		})
	}
	if result.Items == nil {
		result.Items = []saleInvoiceCancelPreviewItem{}
	}
	if p.DocumentProfileVersion == documentProfileV1 {
		result.profilePayload = &p
		result.CoreStatus = "pending"
		result.ProfileStatus = "pending"
		result.RequiredChecks = cancellationProfileRequiredChecks(route)
		result.CompletedChecks = []string{}
	}
	return result
}

func cancellationProfileRequiredChecks(route docRoute) []string {
	checks := []string{"core"}
	if saleVATRegisterApplicable(docPayload{DocumentProfileVersion: documentProfileV1, VATType: 1}, route) {
		checks = append(checks, "vat")
	}
	if route.name == routeCreditNote.name {
		checks = append(checks, "ap_ar")
	}
	return append(checks, "main_log", "erp_log")
}

func (h *WriteHandler) PreviewSaleInvoiceCancel(c *gin.Context) {
	h.previewSaleInvoiceCancellation(c, saleInvoiceCancelKindCreditNote)
}

func (h *WriteHandler) PreviewSaleInvoiceVoid(c *gin.Context) {
	h.previewSaleInvoiceCancellation(c, saleInvoiceCancelKindVoid)
}

func (h *WriteHandler) previewSaleInvoiceCancellation(c *gin.Context, kind saleInvoiceCancelKind) {
	start := time.Now()
	docNo := strings.TrimSpace(c.Param("doc_no"))
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxDocumentRequestBytes)
	var req saleInvoiceCancelRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		writeDocumentJSONError(c, "invalid cancellation preview payload", err)
		return
	}
	if err := normalizeCancellationProfileRequest(&req, false); err != nil {
		api.BadRequest(c, "validation_failed", err.Error(), nil)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	pool := getPool(c, h.dbm)
	if pool == nil {
		h.logWrite(c, cancellationRoute(kind), docNo, 0, start, "db_pool_error")
		return
	}
	result, err := previewSaleInvoiceCancellation(ctx, pool, c.GetString(middleware.TenantKey), docNo, req, kind)
	route := cancellationRoute(kind)
	if err != nil {
		var ae *appError
		if errors.As(err, &ae) {
			writeAppError(c, ae)
			h.logWrite(c, route, req.DocNo, 0, start, ae.Code)
			return
		}
		api.Internal(c, "sale_invoice_cancel_preview_failed", "preview sale invoice cancellation failed", err.Error())
		h.logWrite(c, route, req.DocNo, 0, start, "sale_invoice_cancel_preview_failed")
		return
	}
	api.OK(c, result)
	h.logWrite(c, route, result.CancelDocNo, 0, start, "")
}

func (h *WriteHandler) CreateSaleInvoiceCancel(c *gin.Context) {
	h.createSaleInvoiceCancellation(c, saleInvoiceCancelKindCreditNote)
}

func (h *WriteHandler) CreateSaleInvoiceVoid(c *gin.Context) {
	h.createSaleInvoiceCancellation(c, saleInvoiceCancelKindVoid)
}

func (h *WriteHandler) createSaleInvoiceCancellation(c *gin.Context, kind saleInvoiceCancelKind) {
	start := time.Now()
	docNo := strings.TrimSpace(c.Param("doc_no"))
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxDocumentRequestBytes)
	var req saleInvoiceCancelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		code := writeDocumentJSONError(c, "invalid cancellation payload", err)
		h.logWrite(c, cancellationRoute(kind), "", 0, start, code)
		return
	}
	if err := normalizeCancellationProfileRequest(&req, true); err != nil {
		api.BadRequest(c, "validation_failed", err.Error(), nil)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	pool := getPool(c, h.dbm)
	if pool == nil {
		h.logWrite(c, cancellationRoute(kind), req.DocNo, 0, start, "db_pool_error")
		return
	}
	result, rows, err := createSaleInvoiceCancellation(ctx, pool, c.GetString(middleware.TenantKey), docNo, req, kind)
	route := cancellationRoute(kind)
	if err != nil {
		var ae *appError
		if errors.As(err, &ae) {
			writeAppError(c, ae)
			h.logWrite(c, route, req.DocNo, 0, start, ae.Code)
			return
		}
		api.Internal(c, "sale_invoice_cancel_create_failed", "create sale invoice cancellation failed", err.Error())
		h.logWrite(c, route, req.DocNo, rows, start, "sale_invoice_cancel_create_failed")
		return
	}
	if result.profilePayload != nil {
		logResult := h.writeERPLog(c, *result.profilePayload, route, true)
		applyCancellationProfileStatus(&result, route, logResult)
	}
	if result.Status == "already_exists" {
		api.OK(c, result)
		h.logWrite(c, route, result.ExistingCancelDocNo, 0, start, "")
		return
	}
	api.Created(c, result)
	h.logWrite(c, route, result.CancelDocNo, rows, start, "")
}

func cancellationRoute(kind saleInvoiceCancelKind) docRoute {
	if kind == saleInvoiceCancelKindVoid {
		return routeSaleInvoiceCancel
	}
	return routeCreditNote
}

func previewSaleInvoiceCancellation(ctx context.Context, pool txBeginner, tenant, saleDocNo string, req saleInvoiceCancelRequest, kind saleInvoiceCancelKind) (saleInvoiceCancelPreview, error) {
	if kind == saleInvoiceCancelKindVoid {
		return previewSaleInvoiceVoid(ctx, pool, tenant, saleDocNo, req)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return saleInvoiceCancelPreview{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := configureDocumentTransaction(ctx, tx); err != nil {
		return saleInvoiceCancelPreview{}, err
	}
	if err := acquireCancellationSourceLock(ctx, tx, models.TransFlagSaleInvoice, saleDocNo); err != nil {
		return saleInvoiceCancelPreview{}, err
	}
	return buildSaleInvoiceCancelPreview(ctx, tx, tenant, saleDocNo, req, false)
}

func createSaleInvoiceCancellation(ctx context.Context, pool txBeginner, tenant, saleDocNo string, req saleInvoiceCancelRequest, kind saleInvoiceCancelKind) (saleInvoiceCancelPreview, int, error) {
	if kind == saleInvoiceCancelKindVoid {
		return createSaleInvoiceVoid(ctx, pool, tenant, saleDocNo, req)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return saleInvoiceCancelPreview{}, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := configureDocumentTransaction(ctx, tx); err != nil {
		return saleInvoiceCancelPreview{}, 0, err
	}
	if err := acquireCancellationSourceLock(ctx, tx, models.TransFlagSaleInvoice, saleDocNo); err != nil {
		return saleInvoiceCancelPreview{}, 0, err
	}

	req.DocNo = strings.TrimSpace(req.DocNo)
	if req.DocNo == "" {
		return saleInvoiceCancelPreview{}, 0, newAppError(http.StatusBadRequest, "validation_failed", "doc_no is required for cancellation create", nil)
	}
	preview, err := buildSaleInvoiceCancelPreview(ctx, tx, tenant, saleDocNo, req, true)
	if err != nil {
		return saleInvoiceCancelPreview{}, 0, err
	}
	if preview.Status == "already_exists" {
		if preview.profilePayload != nil {
			rows, err := writeProfileRelations(ctx, tx, *preview.profilePayload, routeCreditNote, preview.PayloadHash)
			if err != nil {
				return saleInvoiceCancelPreview{}, rows, err
			}
			if err := tx.Commit(ctx); err != nil {
				return saleInvoiceCancelPreview{}, rows, fmt.Errorf("commit existing credit-note profile reconciliation: %w", err)
			}
			return preview, rows, nil
		}
		return preview, 0, nil
	}
	src, err := loadSaleInvoiceForCancel(ctx, tx, saleDocNo, true)
	if err != nil {
		return saleInvoiceCancelPreview{}, 0, err
	}
	docDate, docTime, docFormat := normalizedCancelDocFields(req)
	p := preview.profilePayload
	vatRate, totalValue, totalVATValue := any(src.VATRate), any(src.TotalValue), any(src.TotalVATValue)
	totalAfterVAT, totalAmount := any(src.TotalAfterVAT), any(src.TotalAmount)
	totalBeforeVAT, totalDiscount, totalExceptVAT := any(src.TotalBeforeVAT), any(src.TotalDiscount), any(src.TotalExceptVAT)
	if p != nil && p.DocumentProfileVersion == documentProfileV1 {
		vatRate, totalValue, totalVATValue = p.VATRateDecimal, p.TotalValueDecimal, p.TotalVATValueDecimal
		totalAfterVAT, totalAmount = p.TotalAfterVATDecimal, p.TotalAmountDecimal
		totalBeforeVAT, totalDiscount, totalExceptVAT = p.TotalBeforeVATDecimal, p.TotalDiscountDecimal, p.TotalExceptVATDecimal
	}
	refAmount, receivableAmount := creditNoteReferenceAmounts(src)
	_, err = tx.Exec(ctx, `
		INSERT INTO ic_trans (
			trans_type, trans_flag, doc_date, doc_no, doc_time, doc_format_code,
			cust_code, branch_code, sale_code,
			wh_from, location_from,
			vat_type, vat_rate,
			total_value, total_vat_value, total_after_vat, total_amount,
			total_before_vat, total_discount, discount_word, total_except_vat,
			tax_doc_no, tax_doc_date,
			ref_amount, ref_new_amount, ref_diff,
			inquiry_type, remark, remark_2, remark_5, creator_code, cashier_code, user_request, last_status
		) VALUES (
			$1,$2,$3,$4,$5,$6,
			$7,$8,$9,
			$10,$11,
			$12,$13,
			$14,$15,$16,$17,
			$18,$19,$20,$21,
			$22,$23,
			$24,0,$25,
			$26,$27,$28,$29,$30,$31,$32,0
		)`,
		models.TransTypeSale, models.TransFlagCreditNote, docDate, req.DocNo, docTime, docFormat,
		src.CustCode, src.BranchCode, src.SaleCode,
		src.WHFrom, src.LocationFrom,
		src.VATType, vatRate,
		totalValue, totalVATValue, totalAfterVAT, totalAmount,
		totalBeforeVAT, totalDiscount, headerDiscountWord(src.TotalDiscount), totalExceptVAT,
		req.DocNo, docDate,
		refAmount, refAmount,
		src.InquiryType, firstNonEmpty(req.Remark, "รับคืนสินค้า/ลดหนี้จาก Nexflow"), req.Remark2, req.Remark5,
		req.CreatorCode, req.CashierCode, req.UserRequest,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return saleInvoiceCancelPreview{}, 0, newAppError(http.StatusConflict, "duplicate_doc_no", fmt.Sprintf("doc_no '%s' already exists", req.DocNo), nil)
		}
		return saleInvoiceCancelPreview{}, 0, fmt.Errorf("insert credit note header: %w", err)
	}
	rowsWritten := 1
	refGUIDs := make(map[string]string)
	var detailBatch pgx.Batch
	for _, it := range src.Items {
		if it.ItemType != 3 || strings.TrimSpace(it.RefGUID) == "" {
			continue
		}
		guid, err := newRefGUID()
		if err != nil {
			return saleInvoiceCancelPreview{}, rowsWritten, fmt.Errorf("generate credit note set ref_guid: %w", err)
		}
		refGUIDs[it.RefGUID] = guid
	}
	for _, it := range src.Items {
		refGUID := ""
		setRefLine := it.SetRefLine
		if it.ItemType == 3 {
			refGUID = refGUIDs[it.RefGUID]
		}
		if replacement, ok := refGUIDs[it.SetRefLine]; ok {
			setRefLine = replacement
		}
		detailBatch.Queue(`
			INSERT INTO ic_trans_detail (
				trans_type, trans_flag, doc_date, doc_no, line_number,
				cust_code, doc_time, calc_flag, inquiry_type,
				item_code, item_name, unit_code, is_permium, is_get_price,
				wh_code, shelf_code, wh_code_2, shelf_code_2,
				qty, price, price_exclude_vat,
				discount_amount, discount, total_vat_value,
				sum_amount, sum_amount_exclude_vat,
				tax_type, vat_type,
				ref_doc_no, ref_line_number, doc_ref_type,
				branch_code,
				item_type, ref_guid, set_ref_price, set_ref_qty,
				item_code_main, set_ref_line, price_set_ratio,
				last_status
			) VALUES (
				$1,$2,$3,$4,$5,
				$6,$7,1,$8,
				$9,$10,$11,$12,$13,
				$14,$15,$16,$17,
				$18,$19,$20,
				$21,$22,$23,
				$24,$25,
				$26,$27,
				$28,$29,1,
				$30,
				$31,$32,$33,$34,
				$35,$36,$37,
				0
			)`,
			models.TransTypeSale, models.TransFlagCreditNote, docDate, req.DocNo, it.LineNumber,
			src.CustCode, docTime, src.InquiryType,
			it.ItemCode, it.ItemName, it.UnitCode, it.IsPremium, firstNonZero(it.IsGetPrice, 1),
			it.WHCode, it.ShelfCode, it.WHCode2, it.ShelfCode2,
			profileDecimalArg(req.DocumentProfileVersion, it.QtyDecimal, it.Qty),
			profileDecimalArg(req.DocumentProfileVersion, it.PriceDecimal, it.Price),
			profileDecimalArg(req.DocumentProfileVersion, it.PriceExcludeVATDecimal, it.PriceExcludeVAT),
			profileDecimalArg(req.DocumentProfileVersion, it.DiscountAmountDecimal, it.DiscountAmount), it.Discount,
			profileDecimalArg(req.DocumentProfileVersion, it.TotalVATValueDecimal, it.TotalVATValue),
			profileDecimalArg(req.DocumentProfileVersion, it.SumAmountDecimal, it.SumAmount),
			profileDecimalArg(req.DocumentProfileVersion, it.SumAmountExclVATDecimal, it.SumAmountExclVAT),
			it.TaxType, it.VATType,
			src.DocNo, it.LineNumber,
			it.BranchCode,
			it.ItemType, refGUID, it.SetRefPrice, it.SetRefQty,
			it.ItemCodeMain, setRefLine, it.PriceSetRatio,
		)
	}
	detailResults := tx.SendBatch(ctx, &detailBatch)
	for _, it := range src.Items {
		tag, execErr := detailResults.Exec()
		if execErr != nil {
			_ = detailResults.Close()
			return saleInvoiceCancelPreview{}, rowsWritten, fmt.Errorf("insert credit note item %d: %w", it.LineNumber, execErr)
		}
		rowsWritten += int(tag.RowsAffected())
	}
	if err := detailResults.Close(); err != nil {
		return saleInvoiceCancelPreview{}, rowsWritten, fmt.Errorf("close credit note detail insert batch: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO ap_ar_trans_detail (
			trans_type, trans_flag, doc_date, doc_no, line_number,
			billing_no, billing_date,
			sum_debt_value, sum_debt_amount, sum_debt_balance,
			sum_before_vat, bill_type, last_status
		) VALUES ($1,$2,$3,$4,0,$5,$6,$7,$7,$7,$8,1,0)`,
		models.TransTypeSale, models.TransFlagCreditNote, docDate, req.DocNo,
		src.DocNo, src.DocDate, profileDecimalArg(req.DocumentProfileVersion, receivableAmount, src.TotalAmount),
		profileDecimalArg(req.DocumentProfileVersion, src.TotalBeforeVATDecimal, src.TotalBeforeVAT),
	)
	if err != nil {
		return saleInvoiceCancelPreview{}, rowsWritten, fmt.Errorf("insert credit note receivable reference: %w", err)
	}
	rowsWritten++
	if p != nil && p.DocumentProfileVersion == documentProfileV1 {
		profileRows, err := writeProfileRelations(ctx, tx, *p, routeCreditNote, p.ProfilePayloadHash)
		rowsWritten += profileRows
		if err != nil {
			return saleInvoiceCancelPreview{}, rowsWritten, err
		}
	}
	result, err := tx.Exec(ctx,
		`UPDATE ic_trans SET used_status=1 WHERE doc_no=$1 AND trans_flag=$2 AND COALESCE(last_status,0)=0 AND COALESCE(used_status,0)=0`,
		src.DocNo, models.TransFlagSaleInvoice)
	if err != nil {
		return saleInvoiceCancelPreview{}, rowsWritten, fmt.Errorf("mark source sale used: %w", err)
	}
	if result.RowsAffected() != 1 {
		return saleInvoiceCancelPreview{}, rowsWritten, newAppError(
			http.StatusConflict,
			"source_sale_state_changed",
			"source sale invoice changed before credit note creation",
			gin.H{"sale_doc_no": src.DocNo},
		)
	}
	if err := normalizeInsertedDocument(ctx, tx, req.DocNo, models.TransFlagCreditNote); err != nil {
		return saleInvoiceCancelPreview{}, rowsWritten, err
	}
	if err := tx.Commit(ctx); err != nil {
		return saleInvoiceCancelPreview{}, rowsWritten, fmt.Errorf("commit: %w", err)
	}
	preview.Status = "created"
	preview.CoreStatus = "created"
	preview.Message = "created cancellation document"
	return preview, rowsWritten, nil
}

func previewSaleInvoiceVoid(ctx context.Context, pool txBeginner, tenant, saleDocNo string, req saleInvoiceCancelRequest) (saleInvoiceCancelPreview, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return saleInvoiceCancelPreview{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := configureDocumentTransaction(ctx, tx); err != nil {
		return saleInvoiceCancelPreview{}, err
	}
	if err := acquireCancellationSourceLock(ctx, tx, models.TransFlagSaleInvoice, saleDocNo); err != nil {
		return saleInvoiceCancelPreview{}, err
	}
	return buildSaleInvoiceVoidPreview(ctx, tx, tenant, saleDocNo, req, false)
}

func createSaleInvoiceVoid(ctx context.Context, pool txBeginner, tenant, saleDocNo string, req saleInvoiceCancelRequest) (saleInvoiceCancelPreview, int, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return saleInvoiceCancelPreview{}, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := configureDocumentTransaction(ctx, tx); err != nil {
		return saleInvoiceCancelPreview{}, 0, err
	}
	if err := acquireCancellationSourceLock(ctx, tx, models.TransFlagSaleInvoice, saleDocNo); err != nil {
		return saleInvoiceCancelPreview{}, 0, err
	}

	req.DocNo = strings.TrimSpace(req.DocNo)
	if req.DocNo == "" {
		return saleInvoiceCancelPreview{}, 0, newAppError(http.StatusBadRequest, "validation_failed", "doc_no is required for sale invoice cancellation create", nil)
	}
	preview, err := buildSaleInvoiceVoidPreview(ctx, tx, tenant, saleDocNo, req, true)
	if err != nil {
		return saleInvoiceCancelPreview{}, 0, err
	}
	if preview.Status == "already_exists" {
		if preview.profilePayload != nil {
			rows, err := writeProfileRelations(ctx, tx, *preview.profilePayload, routeSaleInvoiceCancel, preview.PayloadHash)
			if err != nil {
				return saleInvoiceCancelPreview{}, rows, err
			}
			if err := tx.Commit(ctx); err != nil {
				return saleInvoiceCancelPreview{}, rows, fmt.Errorf("commit existing invoice-void profile reconciliation: %w", err)
			}
			return preview, rows, nil
		}
		return preview, 0, nil
	}
	src, err := loadSaleInvoiceForCancel(ctx, tx, saleDocNo, true)
	if err != nil {
		return saleInvoiceCancelPreview{}, 0, err
	}
	docDate, docTime, docFormat := normalizedVoidDocFields(req)
	_, err = tx.Exec(ctx, `
		INSERT INTO ic_trans (
			trans_type, trans_flag, doc_date, doc_no, doc_time, doc_format_code,
			cust_code, doc_ref, doc_ref_date, remark, remark_2, remark_5,
			creator_code, cashier_code, user_request, last_status
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,0)`,
		models.TransTypeSale, models.TransFlagSaleInvoiceCancel,
		docDate, req.DocNo, docTime, docFormat,
		src.CustCode, src.DocNo, src.DocDate,
		firstNonEmpty(req.Remark, "ยกเลิกขายสินค้าและบริการจาก Nexflow"), req.Remark2, req.Remark5,
		req.CreatorCode, req.CashierCode, req.UserRequest,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return saleInvoiceCancelPreview{}, 0, newAppError(http.StatusConflict, "duplicate_doc_no", fmt.Sprintf("doc_no '%s' already exists", req.DocNo), nil)
		}
		return saleInvoiceCancelPreview{}, 0, fmt.Errorf("insert sale invoice cancellation header: %w", err)
	}
	rowsWritten := 1
	headerResult, err := tx.Exec(ctx, `
		UPDATE ic_trans
		   SET last_status=1
		 WHERE doc_no=$1 AND trans_flag=$2 AND COALESCE(last_status,0)=0`,
		src.DocNo, models.TransFlagSaleInvoice)
	if err != nil {
		return saleInvoiceCancelPreview{}, rowsWritten, fmt.Errorf("cancel source sale invoice header: %w", err)
	}
	if headerResult.RowsAffected() != 1 {
		return saleInvoiceCancelPreview{}, rowsWritten, newAppError(http.StatusConflict, "source_sale_state_changed", "source sale invoice changed before cancellation", gin.H{"sale_doc_no": src.DocNo})
	}
	if _, err := tx.Exec(ctx, `
		UPDATE ic_trans_detail
		   SET last_status=1
		 WHERE doc_no=$1 AND trans_flag=$2 AND COALESCE(last_status,0)=0`,
		src.DocNo, models.TransFlagSaleInvoice); err != nil {
		return saleInvoiceCancelPreview{}, rowsWritten, fmt.Errorf("cancel source sale invoice details: %w", err)
	}
	if preview.profilePayload != nil && preview.profilePayload.DocumentProfileVersion == documentProfileV1 {
		profileRows, err := writeProfileRelations(ctx, tx, *preview.profilePayload, routeSaleInvoiceCancel, preview.PayloadHash)
		rowsWritten += profileRows
		if err != nil {
			return saleInvoiceCancelPreview{}, rowsWritten, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return saleInvoiceCancelPreview{}, rowsWritten, fmt.Errorf("commit: %w", err)
	}
	preview.Status = "created"
	preview.CoreStatus = "created"
	preview.Message = "created sale invoice cancellation document"
	return preview, rowsWritten, nil
}

func buildSaleInvoiceVoidPreview(ctx context.Context, tx pgx.Tx, tenant, saleDocNo string, req saleInvoiceCancelRequest, lock bool) (saleInvoiceCancelPreview, error) {
	saleDocNo = strings.TrimSpace(saleDocNo)
	if saleDocNo == "" {
		return saleInvoiceCancelPreview{}, newAppError(http.StatusBadRequest, "validation_failed", "sale invoice doc_no is required", nil)
	}
	existing, err := existingSaleInvoiceVoid(ctx, tx, saleDocNo, lock)
	if err != nil {
		return saleInvoiceCancelPreview{}, err
	}
	if existing != "" {
		if req.DocumentProfileVersion == documentProfileV1 {
			storedHash, err := storedProfileHash(ctx, tx, existing, models.TransFlagSaleInvoiceCancel)
			if err != nil {
				return saleInvoiceCancelPreview{}, err
			}
			if storedHash == "" {
				return saleInvoiceCancelPreview{}, validateExistingCancellationProfileOwnership("", "", saleDocNo, existing)
			}
			src, err := loadSaleInvoiceForCancelAnyState(ctx, tx, saleDocNo, lock)
			if err != nil {
				return saleInvoiceCancelPreview{}, err
			}
			req.DocNo = existing
			p, err := saleInvoiceCancellationProfilePayload(tenant, src, req, routeSaleInvoiceCancel)
			if err != nil {
				return saleInvoiceCancelPreview{}, newAppError(http.StatusBadRequest, "validation_failed", err.Error(), nil)
			}
			if err := validateExistingCancellationProfileOwnership(storedHash, p.ProfilePayloadHash, saleDocNo, existing); err != nil {
				return saleInvoiceCancelPreview{}, err
			}
			result := saleInvoiceCancellationPreviewFromSource(src, req, p, routeSaleInvoiceCancel)
			result.Status = "already_exists"
			result.ExistingCancelDocNo = existing
			result.CoreStatus = "already_exists"
			return result, nil
		}
		return saleInvoiceCancelPreview{
			Status:              "already_exists",
			Kind:                saleInvoiceCancelKindVoid,
			SaleDocNo:           saleDocNo,
			ExistingCancelDocNo: existing,
			TransFlag:           models.TransFlagSaleInvoiceCancel,
			Message:             "sale invoice cancellation already exists for this sale invoice",
		}, nil
	}
	if other, err := existingCreditNoteForSale(ctx, tx, saleDocNo, false); err != nil {
		return saleInvoiceCancelPreview{}, err
	} else if other != "" {
		return saleInvoiceCancelPreview{}, conflictingCancellationError(saleDocNo, other, models.TransFlagCreditNote)
	}
	src, err := loadSaleInvoiceForCancel(ctx, tx, saleDocNo, lock)
	if err != nil {
		return saleInvoiceCancelPreview{}, err
	}
	if src.UsedStatus == 1 {
		return saleInvoiceCancelPreview{}, newAppError(
			http.StatusConflict,
			"sale_invoice_already_referenced",
			"sale invoice is already referenced and cannot be voided",
			nil,
		)
	}
	_, _, docFormat := normalizedVoidDocFields(req)
	if err := validateCancellationDocFormat(ctx, tx, docFormat, "SIC"); err != nil {
		return saleInvoiceCancelPreview{}, err
	}
	p, err := saleInvoiceCancellationProfilePayload(tenant, src, req, routeSaleInvoiceCancel)
	if err != nil {
		return saleInvoiceCancelPreview{}, newAppError(http.StatusBadRequest, "validation_failed", err.Error(), nil)
	}
	return saleInvoiceCancellationPreviewFromSource(src, req, p, routeSaleInvoiceCancel), nil
}

func existingSaleInvoiceVoid(ctx context.Context, tx pgx.Tx, saleDocNo string, lock bool) (string, error) {
	sql := `
		SELECT COALESCE(doc_no, '')
		  FROM ic_trans
		 WHERE trans_flag=$1
		   AND COALESCE(last_status,0)=0
		   AND doc_ref=$2
		 ORDER BY doc_date DESC, doc_no DESC
		 LIMIT 1`
	if lock {
		sql += ` FOR UPDATE`
	}
	var docNo string
	err := tx.QueryRow(ctx, sql, models.TransFlagSaleInvoiceCancel, saleDocNo).Scan(&docNo)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("lookup existing sale invoice cancellation: %w", err)
	}
	return strings.TrimSpace(docNo), nil
}

func buildSaleInvoiceCancelPreview(ctx context.Context, tx pgx.Tx, tenant, saleDocNo string, req saleInvoiceCancelRequest, lock bool) (saleInvoiceCancelPreview, error) {
	saleDocNo = strings.TrimSpace(saleDocNo)
	if saleDocNo == "" {
		return saleInvoiceCancelPreview{}, newAppError(http.StatusBadRequest, "validation_failed", "sale invoice doc_no is required", nil)
	}
	existing, err := existingCreditNoteForSale(ctx, tx, saleDocNo, lock)
	if err != nil {
		return saleInvoiceCancelPreview{}, err
	}
	if existing != "" {
		if req.DocumentProfileVersion == documentProfileV1 {
			storedHash, err := storedProfileHash(ctx, tx, existing, models.TransFlagCreditNote)
			if err != nil {
				return saleInvoiceCancelPreview{}, err
			}
			if storedHash == "" {
				return saleInvoiceCancelPreview{}, validateExistingCancellationProfileOwnership("", "", saleDocNo, existing)
			}
			src, err := loadSaleInvoiceForCancelAnyState(ctx, tx, saleDocNo, lock)
			if err != nil {
				return saleInvoiceCancelPreview{}, err
			}
			req.DocNo = existing
			p, err := saleInvoiceCancellationProfilePayload(tenant, src, req, routeCreditNote)
			if err != nil {
				return saleInvoiceCancelPreview{}, newAppError(http.StatusBadRequest, "validation_failed", err.Error(), nil)
			}
			if err := validateExistingCancellationProfileOwnership(storedHash, p.ProfilePayloadHash, saleDocNo, existing); err != nil {
				return saleInvoiceCancelPreview{}, err
			}
			result := saleInvoiceCancellationPreviewFromSource(src, req, p, routeCreditNote)
			result.Status = "already_exists"
			result.ExistingCancelDocNo = existing
			result.CoreStatus = "already_exists"
			return result, nil
		}
		return saleInvoiceCancelPreview{
			Status:              "already_exists",
			Kind:                saleInvoiceCancelKindCreditNote,
			SaleDocNo:           saleDocNo,
			ExistingCancelDocNo: existing,
			TransFlag:           models.TransFlagCreditNote,
			Message:             "credit note already exists for this sale invoice",
		}, nil
	}
	if other, err := existingSaleInvoiceVoid(ctx, tx, saleDocNo, false); err != nil {
		return saleInvoiceCancelPreview{}, err
	} else if other != "" {
		return saleInvoiceCancelPreview{}, conflictingCancellationError(saleDocNo, other, models.TransFlagSaleInvoiceCancel)
	}
	src, err := loadSaleInvoiceForCancel(ctx, tx, saleDocNo, lock)
	if err != nil {
		return saleInvoiceCancelPreview{}, err
	}
	if src.UsedStatus == 1 {
		return saleInvoiceCancelPreview{}, newAppError(http.StatusConflict, "source_sale_already_used", "source sale invoice is already referenced but no credit note was found", gin.H{"sale_doc_no": saleDocNo})
	}
	_, _, docFormat := normalizedCancelDocFields(req)
	if err := validateCancellationDocFormat(ctx, tx, docFormat, "ST"); err != nil {
		return saleInvoiceCancelPreview{}, err
	}
	p, err := saleInvoiceCancellationProfilePayload(tenant, src, req, routeCreditNote)
	if err != nil {
		return saleInvoiceCancelPreview{}, newAppError(http.StatusBadRequest, "validation_failed", err.Error(), nil)
	}
	return saleInvoiceCancellationPreviewFromSource(src, req, p, routeCreditNote), nil
}

func conflictingCancellationError(sourceDocNo, existingDocNo string, destinationTransFlag int) error {
	return newAppError(
		http.StatusConflict,
		"source_already_cancelled",
		"source sale invoice already has a different cancellation document",
		gin.H{
			"source_doc_no":          strings.TrimSpace(sourceDocNo),
			"existing_cancel_doc_no": strings.TrimSpace(existingDocNo),
			"destination_trans_flag": destinationTransFlag,
		},
	)
}

func validateExistingCancellationProfileOwnership(storedHash, requestedHash, sourceDocNo, existingDocNo string) error {
	if strings.TrimSpace(storedHash) == "" {
		return newAppError(
			http.StatusConflict,
			"source_already_cancelled_externally",
			"source document was already cancelled outside Nexflow",
			gin.H{"source_doc_no": strings.TrimSpace(sourceDocNo), "existing_cancel_doc_no": strings.TrimSpace(existingDocNo)},
		)
	}
	return validateStoredProfileHash(storedHash, requestedHash, existingDocNo)
}

func existingCreditNoteForSale(ctx context.Context, tx pgx.Tx, saleDocNo string, lock bool) (string, error) {
	sql := `
		SELECT COALESCE(h.doc_no, '')
		  FROM ic_trans h
		  JOIN ic_trans_detail d ON d.doc_no = h.doc_no AND d.trans_flag = h.trans_flag AND COALESCE(d.last_status,0)=0
		 WHERE h.trans_flag=$1
		   AND COALESCE(h.last_status,0)=0
		   AND d.ref_doc_no=$2
		 ORDER BY h.doc_date DESC, h.doc_no DESC
		 LIMIT 1`
	if lock {
		sql += ` FOR UPDATE OF h`
	}
	var docNo string
	err := tx.QueryRow(ctx, sql, models.TransFlagCreditNote, saleDocNo).Scan(&docNo)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("lookup existing credit note: %w", err)
	}
	return strings.TrimSpace(docNo), nil
}

func loadSaleInvoiceForCancel(ctx context.Context, tx pgx.Tx, saleDocNo string, lock bool) (saleInvoiceForCancel, error) {
	return loadSaleInvoiceForCancelState(ctx, tx, saleDocNo, lock, true)
}

func loadSaleInvoiceForCancelAnyState(ctx context.Context, tx pgx.Tx, saleDocNo string, lock bool) (saleInvoiceForCancel, error) {
	return loadSaleInvoiceForCancelState(ctx, tx, saleDocNo, lock, false)
}

func loadSaleInvoiceForCancelState(ctx context.Context, tx pgx.Tx, saleDocNo string, lock, activeOnly bool) (saleInvoiceForCancel, error) {
	sql := `
		SELECT doc_no, doc_date, COALESCE(doc_time,''), COALESCE(doc_format_code,''),
		       COALESCE(cust_code,''), COALESCE(branch_code,''), COALESCE(sale_code,''),
		       COALESCE(wh_from,''), COALESCE(location_from,''),
		       COALESCE(vat_type,0), COALESCE(vat_rate,0),
		       COALESCE(total_value,0)::float8, COALESCE(total_vat_value,0)::float8,
		       COALESCE(total_after_vat,0)::float8, COALESCE(total_amount,0)::float8,
		       COALESCE(total_before_vat,0)::float8, COALESCE(total_discount,0)::float8,
		       COALESCE(total_except_vat,0)::float8, COALESCE(inquiry_type,0),
		       COALESCE(used_status,0),
		       COALESCE(vat_rate,0)::text, COALESCE(total_value,0)::text,
		       COALESCE(total_vat_value,0)::text, COALESCE(total_after_vat,0)::text,
		       COALESCE(total_amount,0)::text, COALESCE(total_before_vat,0)::text,
		       COALESCE(total_discount,0)::text, COALESCE(total_except_vat,0)::text
		  FROM ic_trans
		 WHERE doc_no=$1 AND trans_flag=$2`
	if activeOnly {
		sql += ` AND COALESCE(last_status,0)=0`
	}
	if lock {
		sql += ` FOR UPDATE`
	}
	var src saleInvoiceForCancel
	err := tx.QueryRow(ctx, sql, saleDocNo, models.TransFlagSaleInvoice).Scan(
		&src.DocNo, &src.DocDate, &src.DocTime, &src.DocFormatCode,
		&src.CustCode, &src.BranchCode, &src.SaleCode,
		&src.WHFrom, &src.LocationFrom,
		&src.VATType, &src.VATRate,
		&src.TotalValue, &src.TotalVATValue, &src.TotalAfterVAT, &src.TotalAmount,
		&src.TotalBeforeVAT, &src.TotalDiscount, &src.TotalExceptVAT, &src.InquiryType,
		&src.UsedStatus,
		&src.VATRateDecimal, &src.TotalValueDecimal, &src.TotalVATValueDecimal,
		&src.TotalAfterVATDecimal, &src.TotalAmountDecimal, &src.TotalBeforeVATDecimal,
		&src.TotalDiscountDecimal, &src.TotalExceptVATDecimal,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return saleInvoiceForCancel{}, newAppError(http.StatusNotFound, "sale_invoice_not_found", "source sale invoice not found: "+saleDocNo, gin.H{"sale_doc_no": saleDocNo})
		}
		return saleInvoiceForCancel{}, fmt.Errorf("lookup source sale invoice: %w", err)
	}
	detailSQL := `
		SELECT COALESCE(line_number,0), COALESCE(item_code,''), COALESCE(item_name,''), COALESCE(unit_code,''),
		       COALESCE(is_permium,0), COALESCE(is_get_price,0),
		       COALESCE(wh_code,''), COALESCE(shelf_code,''), COALESCE(wh_code_2,''), COALESCE(shelf_code_2,''),
		       COALESCE(qty,0)::float8, COALESCE(price,0)::float8, COALESCE(price_exclude_vat,0)::float8,
		       COALESCE(discount_amount,0)::float8, COALESCE(discount,''),
		       COALESCE(total_vat_value,0)::float8,
		       COALESCE(sum_amount,0)::float8, COALESCE(sum_amount_exclude_vat,0)::float8,
		       COALESCE(tax_type,0), COALESCE(vat_type,0),
		       COALESCE(item_type,0)::int, COALESCE(ref_guid,''),
		       COALESCE(set_ref_price,0)::float8, COALESCE(set_ref_qty,0)::float8,
		       COALESCE(item_code_main,''), COALESCE(set_ref_line,''),
		       COALESCE(price_set_ratio,0)::float8, COALESCE(branch_code,''),
		       COALESCE(qty,0)::text, COALESCE(price,0)::text, COALESCE(price_exclude_vat,0)::text,
		       COALESCE(discount_amount,0)::text, COALESCE(total_vat_value,0)::text,
		       COALESCE(sum_amount,0)::text, COALESCE(sum_amount_exclude_vat,0)::text
		  FROM ic_trans_detail
		 WHERE doc_no=$1 AND trans_flag=$2`
	if activeOnly {
		detailSQL += ` AND COALESCE(last_status,0)=0`
	}
	detailSQL += ` ORDER BY COALESCE(line_number,0) LIMIT $3`
	if lock {
		detailSQL += ` FOR UPDATE`
	}
	rows, err := tx.Query(ctx, detailSQL, saleDocNo, models.TransFlagSaleInvoice, maxDocumentItems+1)
	if err != nil {
		return saleInvoiceForCancel{}, fmt.Errorf("lookup source sale invoice details: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var it saleInvoiceCancelLine
		if err := rows.Scan(
			&it.LineNumber, &it.ItemCode, &it.ItemName, &it.UnitCode,
			&it.IsPremium, &it.IsGetPrice,
			&it.WHCode, &it.ShelfCode, &it.WHCode2, &it.ShelfCode2,
			&it.Qty, &it.Price, &it.PriceExcludeVAT,
			&it.DiscountAmount, &it.Discount,
			&it.TotalVATValue,
			&it.SumAmount, &it.SumAmountExclVAT,
			&it.TaxType, &it.VATType,
			&it.ItemType, &it.RefGUID, &it.SetRefPrice, &it.SetRefQty,
			&it.ItemCodeMain, &it.SetRefLine, &it.PriceSetRatio,
			&it.BranchCode, &it.QtyDecimal, &it.PriceDecimal, &it.PriceExcludeVATDecimal,
			&it.DiscountAmountDecimal, &it.TotalVATValueDecimal,
			&it.SumAmountDecimal, &it.SumAmountExclVATDecimal,
		); err != nil {
			return saleInvoiceForCancel{}, fmt.Errorf("scan source detail: %w", err)
		}
		src.Items = append(src.Items, it)
	}
	if err := rows.Err(); err != nil {
		return saleInvoiceForCancel{}, err
	}
	if len(src.Items) == 0 {
		return saleInvoiceForCancel{}, newAppError(http.StatusConflict, "sale_invoice_has_no_items", "source sale invoice has no active detail rows", gin.H{"sale_doc_no": saleDocNo})
	}
	if len(src.Items) > maxDocumentItems {
		return saleInvoiceForCancel{}, newAppError(http.StatusRequestEntityTooLarge, "source_item_limit_exceeded", fmt.Sprintf("source sale invoice has more than %d items", maxDocumentItems), gin.H{"max_items": maxDocumentItems})
	}
	return src, nil
}

func normalizedCancelDocFields(req saleInvoiceCancelRequest) (time.Time, string, string) {
	return normalizedCancellationDocFields(req, "CN")
}

func normalizedVoidDocFields(req saleInvoiceCancelRequest) (time.Time, string, string) {
	return normalizedCancellationDocFields(req, "SIC")
}

func normalizedCancellationDocFields(req saleInvoiceCancelRequest, defaultDocFormat string) (time.Time, string, string) {
	docDate := time.Now()
	if parsed, err := time.Parse("2006-01-02", strings.TrimSpace(req.DocDate)); err == nil {
		docDate = parsed
	}
	docTime := strings.TrimSpace(req.DocTime)
	if docTime == "" {
		docTime = time.Now().Format("15:04")
	}
	docFormat := strings.TrimSpace(req.DocFormatCode)
	if docFormat == "" {
		docFormat = defaultDocFormat
	}
	return docDate, docTime, docFormat
}

func validateCancellationDocFormat(ctx context.Context, tx pgx.Tx, docFormatCode, screenCode string) error {
	docFormatCode = strings.TrimSpace(docFormatCode)
	screenCode = strings.ToUpper(strings.TrimSpace(screenCode))
	if docFormatCode == "" || screenCode == "" {
		return newAppError(http.StatusBadRequest, "validation_failed", "doc_format_code and cancellation screen_code are required", nil)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			  FROM erp_doc_format
			 WHERE LOWER(code)=LOWER($1)
			   AND UPPER(COALESCE(screen_code,''))=$2
		)`, docFormatCode, screenCode).Scan(&exists); err != nil {
		return fmt.Errorf("validate cancellation doc format: %w", err)
	}
	if !exists {
		return newAppError(
			http.StatusBadRequest,
			"doc_format_not_valid_for_cancel_type",
			fmt.Sprintf("doc_format_code '%s' is not configured for SML screen_code %s", docFormatCode, screenCode),
			gin.H{"doc_format_code": docFormatCode, "screen_code": screenCode},
		)
	}
	return nil
}

func firstNonZero(v, fallback int) int {
	if v != 0 {
		return v
	}
	return fallback
}

func firstNonZeroFloat(v, fallback float64) float64 {
	if v != 0 {
		return v
	}
	return fallback
}
