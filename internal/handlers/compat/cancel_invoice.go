package compat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/models"
)

type saleInvoiceCancelRequest struct {
	DocNo         string `json:"doc_no"`
	DocDate       string `json:"doc_date"`
	DocTime       string `json:"doc_time"`
	DocFormatCode string `json:"doc_format_code"`
	Remark        string `json:"remark"`
	UserRequest   string `json:"user_request"`
}

type saleInvoiceCancelPreview struct {
	Status              string                         `json:"status"`
	SaleDocNo           string                         `json:"sale_doc_no"`
	CancelDocNo         string                         `json:"cancel_doc_no,omitempty"`
	ExistingCancelDocNo string                         `json:"existing_cancel_doc_no,omitempty"`
	TransFlag           int                            `json:"trans_flag"`
	DocFormatCode       string                         `json:"doc_format_code"`
	DocDate             string                         `json:"doc_date"`
	CustCode            string                         `json:"cust_code"`
	TotalAmount         float64                        `json:"total_amount"`
	TotalValue          float64                        `json:"total_value"`
	TotalVATValue       float64                        `json:"total_vat_value"`
	TotalAfterVAT       float64                        `json:"total_after_vat"`
	ItemCount           int                            `json:"item_count"`
	Items               []saleInvoiceCancelPreviewItem `json:"items"`
	Message             string                         `json:"message,omitempty"`
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
	DocNo          string
	DocDate        time.Time
	DocTime        string
	DocFormatCode  string
	CustCode       string
	BranchCode     string
	SaleCode       string
	WHFrom         string
	LocationFrom   string
	VATType        int
	VATRate        float64
	TotalValue     float64
	TotalVATValue  float64
	TotalAfterVAT  float64
	TotalAmount    float64
	TotalBeforeVAT float64
	TotalDiscount  float64
	TotalExceptVAT float64
	InquiryType    int
	UsedStatus     int
	Items          []saleInvoiceCancelLine
}

type saleInvoiceCancelLine struct {
	LineNumber       int
	ItemCode         string
	ItemName         string
	UnitCode         string
	IsPremium        int
	IsGetPrice       int
	WHCode           string
	ShelfCode        string
	WHCode2          string
	ShelfCode2       string
	Qty              float64
	Price            float64
	PriceExcludeVAT  float64
	DiscountAmount   float64
	Discount         string
	TotalVATValue    float64
	SumAmount        float64
	SumAmountExclVAT float64
	TaxType          int
	VATType          int
}

func (h *WriteHandler) PreviewSaleInvoiceCancel(c *gin.Context) {
	start := time.Now()
	docNo := strings.TrimSpace(c.Param("doc_no"))
	var req saleInvoiceCancelRequest
	_ = c.ShouldBindJSON(&req)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	pool := getPool(c, h.dbm)
	if pool == nil {
		h.logWrite(c, routeCreditNote, docNo, 0, start, "db_pool_error")
		return
	}
	result, err := previewSaleInvoiceCancel(ctx, pool, docNo, req)
	if err != nil {
		var ae *appError
		if errors.As(err, &ae) {
			writeAppError(c, ae)
			h.logWrite(c, routeCreditNote, req.DocNo, 0, start, ae.Code)
			return
		}
		api.Internal(c, "sale_invoice_cancel_preview_failed", "preview sale invoice cancellation failed", err.Error())
		h.logWrite(c, routeCreditNote, req.DocNo, 0, start, "sale_invoice_cancel_preview_failed")
		return
	}
	api.OK(c, result)
	h.logWrite(c, routeCreditNote, result.CancelDocNo, 0, start, "")
}

func (h *WriteHandler) CreateSaleInvoiceCancel(c *gin.Context) {
	start := time.Now()
	docNo := strings.TrimSpace(c.Param("doc_no"))
	var req saleInvoiceCancelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, "invalid_json", "invalid cancellation payload", err.Error())
		h.logWrite(c, routeCreditNote, "", 0, start, "invalid_json")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	pool := getPool(c, h.dbm)
	if pool == nil {
		h.logWrite(c, routeCreditNote, req.DocNo, 0, start, "db_pool_error")
		return
	}
	result, rows, err := createSaleInvoiceCancel(ctx, pool, docNo, req)
	if err != nil {
		var ae *appError
		if errors.As(err, &ae) {
			writeAppError(c, ae)
			h.logWrite(c, routeCreditNote, req.DocNo, 0, start, ae.Code)
			return
		}
		api.Internal(c, "sale_invoice_cancel_create_failed", "create sale invoice cancellation failed", err.Error())
		h.logWrite(c, routeCreditNote, req.DocNo, rows, start, "sale_invoice_cancel_create_failed")
		return
	}
	if result.Status == "already_exists" {
		api.OK(c, result)
		h.logWrite(c, routeCreditNote, result.ExistingCancelDocNo, 0, start, "")
		return
	}
	api.Created(c, result)
	h.logWrite(c, routeCreditNote, result.CancelDocNo, rows, start, "")
}

func previewSaleInvoiceCancel(ctx context.Context, pool txBeginner, saleDocNo string, req saleInvoiceCancelRequest) (saleInvoiceCancelPreview, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return saleInvoiceCancelPreview{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	return buildSaleInvoiceCancelPreview(ctx, tx, saleDocNo, req, false)
}

func createSaleInvoiceCancel(ctx context.Context, pool txBeginner, saleDocNo string, req saleInvoiceCancelRequest) (saleInvoiceCancelPreview, int, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return saleInvoiceCancelPreview{}, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	req.DocNo = strings.TrimSpace(req.DocNo)
	if req.DocNo == "" {
		return saleInvoiceCancelPreview{}, 0, newAppError(http.StatusBadRequest, "validation_failed", "doc_no is required for cancellation create", nil)
	}
	preview, err := buildSaleInvoiceCancelPreview(ctx, tx, saleDocNo, req, true)
	if err != nil {
		return saleInvoiceCancelPreview{}, 0, err
	}
	if preview.Status == "already_exists" {
		return preview, 0, nil
	}
	src, err := loadSaleInvoiceForCancel(ctx, tx, saleDocNo, true)
	if err != nil {
		return saleInvoiceCancelPreview{}, 0, err
	}
	docDate, docTime, docFormat := normalizedCancelDocFields(req)
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
			inquiry_type, remark, user_request, last_status
		) VALUES (
			$1,$2,$3,$4,$5,$6,
			$7,$8,$9,
			$10,$11,
			$12,$13,
			$14,$15,$16,$17,
			$18,$19,$20,$21,
			$22,$23,
			$24,0,$25,
			$26,$27,$28,0
		)`,
		models.TransTypeSale, models.TransFlagCreditNote, docDate, req.DocNo, docTime, docFormat,
		src.CustCode, src.BranchCode, src.SaleCode,
		src.WHFrom, src.LocationFrom,
		src.VATType, src.VATRate,
		src.TotalValue, src.TotalVATValue, src.TotalAfterVAT, src.TotalAmount,
		src.TotalBeforeVAT, src.TotalDiscount, headerDiscountWord(src.TotalDiscount), src.TotalExceptVAT,
		req.DocNo, docDate,
		src.TotalAmount, src.TotalAmount,
		src.InquiryType, firstNonEmpty(req.Remark, "ยกเลิกจาก Nexflow"), req.UserRequest,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return saleInvoiceCancelPreview{}, 0, newAppError(http.StatusConflict, "duplicate_doc_no", fmt.Sprintf("doc_no '%s' already exists", req.DocNo), nil)
		}
		return saleInvoiceCancelPreview{}, 0, fmt.Errorf("insert credit note header: %w", err)
	}
	rowsWritten := 1
	for _, it := range src.Items {
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
				ref_doc_no, ref_line_number, doc_ref_type,
				branch_code, last_status
			) VALUES (
				$1,$2,$3,$4,$5,
				$6,$7,-1,$8,
				$9,$10,$11,$12,$13,
				$14,$15,$16,$17,
				$18,$19,$20,
				$21,$22,$23,
				$24,$25,
				$26,$27,
				$28,$29,1,
				$30,0
			)`,
			models.TransTypeSale, models.TransFlagCreditNote, docDate, req.DocNo, it.LineNumber,
			src.CustCode, docTime, src.InquiryType,
			it.ItemCode, it.ItemName, it.UnitCode, it.IsPremium, firstNonZero(it.IsGetPrice, 1),
			it.WHCode, it.ShelfCode, firstNonEmpty(it.WHCode2, it.WHCode), firstNonEmpty(it.ShelfCode2, it.ShelfCode),
			it.Qty, it.Price, firstNonZeroFloat(it.PriceExcludeVAT, it.Price),
			it.DiscountAmount, it.Discount, it.TotalVATValue,
			it.SumAmount, firstNonZeroFloat(it.SumAmountExclVAT, it.SumAmount),
			it.TaxType, firstNonZero(it.VATType, src.VATType),
			src.DocNo, it.LineNumber,
			src.BranchCode,
		)
		if err != nil {
			return saleInvoiceCancelPreview{}, rowsWritten, fmt.Errorf("insert credit note item %d: %w", it.LineNumber, err)
		}
		rowsWritten++
	}
	if _, err := tx.Exec(ctx,
		`UPDATE ic_trans SET used_status=1 WHERE doc_no=$1 AND trans_flag=$2 AND last_status=0`,
		src.DocNo, models.TransFlagSaleInvoice); err != nil {
		return saleInvoiceCancelPreview{}, rowsWritten, fmt.Errorf("mark source sale used: %w", err)
	}
	if err := normalizeInsertedDocument(ctx, tx, req.DocNo, models.TransFlagCreditNote); err != nil {
		return saleInvoiceCancelPreview{}, rowsWritten, err
	}
	if err := tx.Commit(ctx); err != nil {
		return saleInvoiceCancelPreview{}, rowsWritten, fmt.Errorf("commit: %w", err)
	}
	preview.Status = "created"
	preview.Message = "created cancellation document"
	return preview, rowsWritten, nil
}

func buildSaleInvoiceCancelPreview(ctx context.Context, tx pgx.Tx, saleDocNo string, req saleInvoiceCancelRequest, lock bool) (saleInvoiceCancelPreview, error) {
	saleDocNo = strings.TrimSpace(saleDocNo)
	if saleDocNo == "" {
		return saleInvoiceCancelPreview{}, newAppError(http.StatusBadRequest, "validation_failed", "sale invoice doc_no is required", nil)
	}
	existing, err := existingCreditNoteForSale(ctx, tx, saleDocNo, lock)
	if err != nil {
		return saleInvoiceCancelPreview{}, err
	}
	if existing != "" {
		return saleInvoiceCancelPreview{
			Status:              "already_exists",
			SaleDocNo:           saleDocNo,
			ExistingCancelDocNo: existing,
			TransFlag:           models.TransFlagCreditNote,
			Message:             "credit note already exists for this sale invoice",
		}, nil
	}
	src, err := loadSaleInvoiceForCancel(ctx, tx, saleDocNo, lock)
	if err != nil {
		return saleInvoiceCancelPreview{}, err
	}
	if src.UsedStatus == 1 {
		return saleInvoiceCancelPreview{}, newAppError(http.StatusConflict, "source_sale_already_used", "source sale invoice is already referenced but no credit note was found", gin.H{"sale_doc_no": saleDocNo})
	}
	docDate, _, docFormat := normalizedCancelDocFields(req)
	out := saleInvoiceCancelPreview{
		Status:        "ready",
		SaleDocNo:     src.DocNo,
		CancelDocNo:   strings.TrimSpace(req.DocNo),
		TransFlag:     models.TransFlagCreditNote,
		DocFormatCode: docFormat,
		DocDate:       docDate.Format("2006-01-02"),
		CustCode:      src.CustCode,
		TotalAmount:   src.TotalAmount,
		TotalValue:    src.TotalValue,
		TotalVATValue: src.TotalVATValue,
		TotalAfterVAT: src.TotalAfterVAT,
		ItemCount:     len(src.Items),
	}
	for _, it := range src.Items {
		out.Items = append(out.Items, saleInvoiceCancelPreviewItem{
			LineNumber:      it.LineNumber,
			ItemCode:        it.ItemCode,
			ItemName:        it.ItemName,
			UnitCode:        it.UnitCode,
			Qty:             it.Qty,
			Price:           it.Price,
			SumAmount:       it.SumAmount,
			RefDocNo:        src.DocNo,
			RefLineNumber:   it.LineNumber,
			DocRefType:      1,
			PriceExcludeVAT: firstNonZeroFloat(it.PriceExcludeVAT, it.Price),
			SumExcludeVAT:   firstNonZeroFloat(it.SumAmountExclVAT, it.SumAmount),
		})
	}
	return out, nil
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
	sql := `
		SELECT doc_no, doc_date, COALESCE(doc_time,''), COALESCE(doc_format_code,''),
		       COALESCE(cust_code,''), COALESCE(branch_code,''), COALESCE(sale_code,''),
		       COALESCE(wh_from,''), COALESCE(location_from,''),
		       COALESCE(vat_type,0), COALESCE(vat_rate,0),
		       COALESCE(total_value,0)::float8, COALESCE(total_vat_value,0)::float8,
		       COALESCE(total_after_vat,0)::float8, COALESCE(total_amount,0)::float8,
		       COALESCE(total_before_vat,0)::float8, COALESCE(total_discount,0)::float8,
		       COALESCE(total_except_vat,0)::float8, COALESCE(inquiry_type,0),
		       COALESCE(used_status,0)
		  FROM ic_trans
		 WHERE doc_no=$1 AND trans_flag=$2 AND COALESCE(last_status,0)=0`
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
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return saleInvoiceForCancel{}, newAppError(http.StatusNotFound, "sale_invoice_not_found", "source sale invoice not found: "+saleDocNo, gin.H{"sale_doc_no": saleDocNo})
		}
		return saleInvoiceForCancel{}, fmt.Errorf("lookup source sale invoice: %w", err)
	}
	rows, err := tx.Query(ctx, `
		SELECT COALESCE(line_number,0), COALESCE(item_code,''), COALESCE(item_name,''), COALESCE(unit_code,''),
		       COALESCE(is_permium,0), COALESCE(is_get_price,0),
		       COALESCE(wh_code,''), COALESCE(shelf_code,''), COALESCE(wh_code_2,''), COALESCE(shelf_code_2,''),
		       COALESCE(qty,0)::float8, COALESCE(price,0)::float8, COALESCE(price_exclude_vat,0)::float8,
		       COALESCE(discount_amount,0)::float8, COALESCE(discount,''),
		       COALESCE(total_vat_value,0)::float8,
		       COALESCE(sum_amount,0)::float8, COALESCE(sum_amount_exclude_vat,0)::float8,
		       COALESCE(tax_type,0), COALESCE(vat_type,0)
		  FROM ic_trans_detail
		 WHERE doc_no=$1 AND trans_flag=$2 AND COALESCE(last_status,0)=0
		 ORDER BY COALESCE(line_number,0)`, saleDocNo, models.TransFlagSaleInvoice)
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
	return src, nil
}

func normalizedCancelDocFields(req saleInvoiceCancelRequest) (time.Time, string, string) {
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
		docFormat = "CN"
	}
	return docDate, docTime, docFormat
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
