package compat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/middleware"
	"sml-api-bybos/internal/models"
)

type saleOrderForCancel struct {
	DocNo, DocTime, DocFormatCode                        string
	CustCode, BranchCode, SaleCode, WHFrom, LocationFrom string
	DocDate                                              time.Time
	VATType, InquiryType, UsedStatus                     int
	VATRate, TotalValue, TotalVATValue                   string
	TotalAfterVAT, TotalAmount, TotalBeforeVAT           string
	TotalDiscount, TotalExceptVAT                        string
	Items                                                []saleOrderCancelLine
}

type saleOrderCancelLine struct {
	LineNumber                                                 int
	ItemCode, ItemName, UnitCode                               string
	WHCode, ShelfCode, WHCode2, ShelfCode2, BranchCode         string
	Qty, Price, PriceExcludeVAT, DiscountAmount, TotalVATValue string
	SumAmount, SumAmountExcludeVAT                             string
	Discount                                                   string
	IsPremium, IsGetPrice, TaxType, VATType                    int
}

func (h *WriteHandler) PreviewSaleOrderVoid(c *gin.Context) {
	h.handleSaleOrderVoid(c, true)
}

func (h *WriteHandler) CreateSaleOrderVoid(c *gin.Context) {
	h.handleSaleOrderVoid(c, false)
}

func (h *WriteHandler) handleSaleOrderVoid(c *gin.Context, previewOnly bool) {
	start := time.Now()
	sourceDocNo := strings.TrimSpace(c.Param("doc_no"))
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxDocumentRequestBytes)
	var req saleInvoiceCancelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, "invalid_json", "invalid sale-order cancellation payload", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	pool := getPool(c, h.dbm)
	if pool == nil {
		return
	}
	tenant := c.GetString(middleware.TenantKey)
	result, rows, err := executeSaleOrderVoid(ctx, pool, tenant, sourceDocNo, req, previewOnly)
	if err != nil {
		var appErr *appError
		if errorsAsAppError(err, &appErr) {
			writeAppError(c, appErr)
		} else {
			api.Internal(c, "sale_order_cancel_failed", "sale-order cancellation failed", err.Error())
		}
		h.logWrite(c, routeSaleOrderCancel, req.DocNo, rows, start, errorCode(err))
		return
	}
	if previewOnly {
		api.OK(c, result)
		h.logWrite(c, routeSaleOrderCancel, req.DocNo, 0, start, "")
		return
	}
	if result.profilePayload != nil {
		logResult := h.writeERPLog(c, *result.profilePayload, routeSaleOrderCancel, true)
		applyCancellationProfileStatus(&result, routeSaleOrderCancel, logResult)
	}
	if result.Status == "already_exists" {
		api.OK(c, result)
	} else {
		api.Created(c, result)
	}
	h.logWrite(c, routeSaleOrderCancel, result.CancelDocNo, rows, start, "")
}

func errorsAsAppError(err error, target **appError) bool {
	return errors.As(err, target)
}

func errorCode(err error) string {
	var appErr *appError
	if errors.As(err, &appErr) {
		return appErr.Code
	}
	return "sale_order_cancel_failed"
}

func executeSaleOrderVoid(ctx context.Context, pool txBeginner, tenant, sourceDocNo string, req saleInvoiceCancelRequest, previewOnly bool) (saleInvoiceCancelPreview, int, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return saleInvoiceCancelPreview{}, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ"); err != nil {
		return saleInvoiceCancelPreview{}, 0, fmt.Errorf("set repeatable-read isolation: %w", err)
	}
	if err := acquireCancellationSourceLock(ctx, tx, models.TransFlagSaleOrder, sourceDocNo); err != nil {
		return saleInvoiceCancelPreview{}, 0, err
	}
	if err := normalizeCancellationProfileRequest(&req, !previewOnly); err != nil {
		return saleInvoiceCancelPreview{}, 0, newAppError(http.StatusBadRequest, "validation_failed", err.Error(), nil)
	}
	existing, err := existingSaleOrderVoid(ctx, tx, sourceDocNo)
	if err != nil {
		return saleInvoiceCancelPreview{}, 0, err
	}
	src, err := loadSaleOrderForCancel(ctx, tx, sourceDocNo, !previewOnly)
	if err != nil {
		return saleInvoiceCancelPreview{}, 0, err
	}
	if len(src.Items) > maxDocumentItems {
		return saleInvoiceCancelPreview{}, 0, newAppError(http.StatusRequestEntityTooLarge, "source_item_limit_exceeded", fmt.Sprintf("source sale order has %d items; maximum is %d", len(src.Items), maxDocumentItems), gin.H{"source_items": len(src.Items), "max_items": maxDocumentItems})
	}
	if err := validateCancellationDocFormat(ctx, tx, firstNonEmpty(req.DocFormatCode, "SSC"), "SSC"); err != nil {
		return saleInvoiceCancelPreview{}, 0, err
	}
	p, err := saleOrderCancelProfilePayload(tenant, src, req)
	if err != nil {
		return saleInvoiceCancelPreview{}, 0, newAppError(http.StatusBadRequest, "validation_failed", err.Error(), nil)
	}
	result := saleOrderCancelPreviewFromSource(src, req, p)
	if existing != "" {
		result.Status = "already_exists"
		result.CancelDocNo = existing
		result.ExistingCancelDocNo = existing
		result.CoreStatus = "already_exists"
		if req.DocumentProfileVersion == documentProfileV1 {
			storedHash, err := storedProfileHash(ctx, tx, existing, models.TransFlagSaleOrderCancel)
			if err != nil {
				return saleInvoiceCancelPreview{}, 0, err
			}
			if err := validateExistingCancellationProfileOwnership(storedHash, p.ProfilePayloadHash, src.DocNo, existing); err != nil {
				return saleInvoiceCancelPreview{}, 0, err
			}
			p.DocNo = existing
			result.profilePayload = &p
		}
		if previewOnly {
			return result, 0, nil
		}
		rows := 0
		if req.DocumentProfileVersion == documentProfileV1 {
			rows, err = writeProfileRelations(ctx, tx, p, routeSaleOrderCancel, p.ProfilePayloadHash)
			if err != nil {
				return saleInvoiceCancelPreview{}, rows, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return saleInvoiceCancelPreview{}, rows, fmt.Errorf("commit existing SSC reconciliation: %w", err)
		}
		return result, rows, nil
	}
	if src.UsedStatus != 0 {
		return saleInvoiceCancelPreview{}, 0, newAppError(http.StatusConflict, "source_sale_order_already_used", "source sale order is already referenced but no SSC document was found", gin.H{"source_doc_no": src.DocNo})
	}
	if previewOnly {
		return result, 0, nil
	}
	rows, err := insertSaleOrderCancelCore(ctx, tx, src, req, p)
	if err != nil {
		return saleInvoiceCancelPreview{}, rows, err
	}
	if req.DocumentProfileVersion == documentProfileV1 {
		profileRows, err := writeProfileRelations(ctx, tx, p, routeSaleOrderCancel, p.ProfilePayloadHash)
		rows += profileRows
		if err != nil {
			return saleInvoiceCancelPreview{}, rows, err
		}
	}
	updated, err := tx.Exec(ctx, `UPDATE ic_trans SET used_status=1 WHERE doc_no=$1 AND trans_flag=$2 AND COALESCE(last_status,0)=0 AND COALESCE(used_status,0)=0`, src.DocNo, models.TransFlagSaleOrder)
	if err != nil {
		return saleInvoiceCancelPreview{}, rows, fmt.Errorf("mark source sale order used: %w", err)
	}
	if updated.RowsAffected() != 1 {
		return saleInvoiceCancelPreview{}, rows, newAppError(http.StatusConflict, "source_sale_order_state_changed", "source sale order changed before SSC creation", gin.H{"source_doc_no": src.DocNo})
	}
	if err := tx.Commit(ctx); err != nil {
		return saleInvoiceCancelPreview{}, rows, fmt.Errorf("commit SSC: %w", err)
	}
	result.Status = "created"
	result.CoreStatus = "created"
	result.profilePayload = &p
	return result, rows, nil
}

func normalizeCancellationProfileRequest(req *saleInvoiceCancelRequest, requireDocNo bool) error {
	req.DocumentProfileVersion = strings.TrimSpace(req.DocumentProfileVersion)
	req.DocNo = strings.TrimSpace(req.DocNo)
	req.DocDate = strings.TrimSpace(req.DocDate)
	req.DocTime = strings.TrimSpace(req.DocTime)
	req.DocFormatCode = strings.TrimSpace(req.DocFormatCode)
	req.Remark = strings.TrimSpace(req.Remark)
	req.Remark2 = strings.TrimSpace(req.Remark2)
	req.Remark5 = strings.TrimSpace(req.Remark5)
	if requireDocNo && req.DocNo == "" {
		return fmt.Errorf("doc_no is required for cancellation create")
	}
	if req.DocumentProfileVersion != "" && req.DocumentProfileVersion != documentProfileV1 {
		return fmt.Errorf("document_profile_version must be %q", documentProfileV1)
	}
	// Preserve the legacy cancellation contract: the pre-Profile writers fall
	// back to today's date when doc_date is missing or malformed. Profile V1 is
	// deliberately strict because its canonical hash and audit relations must
	// all agree on the same document date.
	if req.DocumentProfileVersion == "" {
		return nil
	}
	if req.DocDate != "" {
		if _, err := time.Parse("2006-01-02", req.DocDate); err != nil {
			return fmt.Errorf("doc_date format must be YYYY-MM-DD")
		}
	}
	for _, field := range []struct{ name, value string }{{"remark", req.Remark}, {"remark_2", req.Remark2}, {"remark_5", req.Remark5}} {
		if err := validateBoundedLiteral(field.name, field.value, maxProfileTextRunes); err != nil {
			return err
		}
	}
	if !strings.HasPrefix(req.Remark5, "NEXFLOW|") {
		return fmt.Errorf("remark_5 must use NEXFLOW|<channel>|<order-or-bill>")
	}
	for _, field := range []struct {
		name  string
		value *string
		want  string
	}{
		{"creator_code", &req.CreatorCode, "BILLFLOW"}, {"cashier_code", &req.CashierCode, "BILLFLOW"}, {"user_request", &req.UserRequest, "NEXFLOW"},
	} {
		*field.value = strings.TrimSpace(*field.value)
		if *field.value == "" {
			*field.value = field.want
		}
		if *field.value != field.want {
			return fmt.Errorf("%s must be %s for %s", field.name, field.want, documentProfileV1)
		}
	}
	return nil
}

func saleOrderCancelProfilePayload(tenant string, src saleOrderForCancel, req saleInvoiceCancelRequest) (docPayload, error) {
	docDate, docTime, docFormat := normalizedCancellationDocFields(req, "SSC")
	p := docPayload{
		DocumentProfileVersion: req.DocumentProfileVersion, DocNo: req.DocNo,
		DocDate: docDate.Format("2006-01-02"), DocTime: docTime, DocFormatCode: docFormat,
		DocRef: src.DocNo, DocRefDate: src.DocDate.Format("2006-01-02"), CustCode: src.CustCode,
		BranchCode: src.BranchCode, SaleCode: src.SaleCode, WHCode: src.WHFrom, ShelfCode: src.LocationFrom,
		VATType: src.VATType, InquiryType: src.InquiryType, Remark: req.Remark, Remark2: req.Remark2,
		Remark5: req.Remark5, CreatorCode: req.CreatorCode, CashierCode: req.CashierCode,
		UserRequest: req.UserRequest, CurrencyCode: "THB", ExchangeRateDecimal: "1",
		ShipmentApplicability: "not_applicable", VATRateDecimal: src.VATRate,
		TotalValueDecimal: src.TotalValue, TotalVATValueDecimal: src.TotalVATValue,
		TotalAfterVATDecimal: src.TotalAfterVAT, TotalAmountDecimal: src.TotalAmount,
		TotalBeforeVATDecimal: src.TotalBeforeVAT, TotalDiscountDecimal: src.TotalDiscount,
		TotalExceptVATDecimal: src.TotalExceptVAT,
	}
	p.VATRate, _ = strconv.ParseFloat(src.VATRate, 64)
	p.TotalValue, _ = strconv.ParseFloat(src.TotalValue, 64)
	p.TotalVATValue, _ = strconv.ParseFloat(src.TotalVATValue, 64)
	p.TotalAfterVAT, _ = strconv.ParseFloat(src.TotalAfterVAT, 64)
	p.TotalAmount, _ = strconv.ParseFloat(src.TotalAmount, 64)
	p.TotalBeforeVAT, _ = strconv.ParseFloat(src.TotalBeforeVAT, 64)
	p.TotalDiscount, _ = strconv.ParseFloat(src.TotalDiscount, 64)
	p.TotalExceptVAT, _ = strconv.ParseFloat(src.TotalExceptVAT, 64)
	for _, line := range src.Items {
		item := docItem{LineNumber: line.LineNumber, ItemCode: line.ItemCode, ItemName: line.ItemName, UnitCode: line.UnitCode,
			WHCode: line.WHCode, ShelfCode: line.ShelfCode, WHCode2: line.WHCode2, ShelfCode2: line.ShelfCode2,
			RefDocNo: src.DocNo, RefLineNumber: line.LineNumber, DocRefType: 0, BranchCode: line.BranchCode,
			IsPremium: line.IsPremium, IsGetPrice: line.IsGetPrice, TaxType: line.TaxType, VATType: line.VATType,
			QtyDecimal: line.Qty, PriceDecimal: line.Price, PriceExcludeVATDecimal: line.PriceExcludeVAT,
			DiscountAmountDecimal: line.DiscountAmount, SumAmountDecimal: line.SumAmount,
			VATAmountDecimal: line.TotalVATValue, SumAmountExclVATDecimal: line.SumAmountExcludeVAT}
		item.Qty, _ = strconv.ParseFloat(line.Qty, 64)
		item.Price, _ = strconv.ParseFloat(line.Price, 64)
		item.PriceExcludeVAT, _ = strconv.ParseFloat(line.PriceExcludeVAT, 64)
		item.DiscountAmount, _ = strconv.ParseFloat(line.DiscountAmount, 64)
		item.SumAmount, _ = strconv.ParseFloat(line.SumAmount, 64)
		item.VATAmount, _ = strconv.ParseFloat(line.TotalVATValue, 64)
		item.TotalVATValue = item.VATAmount
		item.SumAmountExclVAT, _ = strconv.ParseFloat(line.SumAmountExcludeVAT, 64)
		p.Details = append(p.Details, item)
	}
	if req.DocumentProfileVersion != documentProfileV1 {
		return p, nil
	}
	if err := normalizeAndValidateProfile(&p, p.Details, routeSaleOrderCancel); err != nil {
		return docPayload{}, err
	}
	baseHash, err := canonicalProfileHash(tenant, p, p.Details, routeSaleOrderCancel)
	if err != nil {
		return docPayload{}, err
	}
	p.ProfilePayloadHash = cancellationIntentProfileHash(tenant, models.TransFlagSaleOrder, src.DocNo, models.TransFlagSaleOrderCancel, baseHash)
	return p, nil
}

func cancellationIntentProfileHash(tenant string, sourceTransFlag int, sourceDocNo string, destinationTransFlag int, baseHash string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s|%d|%s", strings.TrimSpace(tenant), sourceTransFlag, strings.TrimSpace(sourceDocNo), destinationTransFlag, baseHash)))
	return hex.EncodeToString(sum[:])
}

func saleOrderCancelPreviewFromSource(src saleOrderForCancel, req saleInvoiceCancelRequest, p docPayload) saleInvoiceCancelPreview {
	result := saleInvoiceCancelPreview{Status: "ready", Kind: saleInvoiceCancelKindSaleOrder, SaleDocNo: src.DocNo,
		CancelDocNo: req.DocNo, TransFlag: models.TransFlagSaleOrderCancel, DocFormatCode: p.DocFormatCode,
		DocDate: p.DocDate, CustCode: src.CustCode, SourceItemCount: len(src.Items), ItemCount: len(src.Items),
		PayloadHash: p.ProfilePayloadHash}
	result.TotalAmount, _ = strconv.ParseFloat(src.TotalAmount, 64)
	result.TotalValue, _ = strconv.ParseFloat(src.TotalValue, 64)
	result.TotalVATValue, _ = strconv.ParseFloat(src.TotalVATValue, 64)
	result.TotalAfterVAT, _ = strconv.ParseFloat(src.TotalAfterVAT, 64)
	result.SourceTotalAmount = result.TotalAmount
	if p.DocumentProfileVersion == documentProfileV1 {
		result.profilePayload = &p
		result.CoreStatus = "pending"
		result.ProfileStatus = "pending"
		result.RequiredChecks = []string{"core", "main_log", "erp_log"}
		result.CompletedChecks = []string{}
	}
	for _, line := range src.Items {
		qty, _ := strconv.ParseFloat(line.Qty, 64)
		price, _ := strconv.ParseFloat(line.Price, 64)
		sum, _ := strconv.ParseFloat(line.SumAmount, 64)
		priceEx, _ := strconv.ParseFloat(line.PriceExcludeVAT, 64)
		sumEx, _ := strconv.ParseFloat(line.SumAmountExcludeVAT, 64)
		result.Items = append(result.Items, saleInvoiceCancelPreviewItem{LineNumber: line.LineNumber, ItemCode: line.ItemCode,
			ItemName: line.ItemName, UnitCode: line.UnitCode, Qty: qty, Price: price, SumAmount: sum,
			RefDocNo: src.DocNo, RefLineNumber: line.LineNumber, DocRefType: 0, PriceExcludeVAT: priceEx, SumExcludeVAT: sumEx})
	}
	return result
}

func applyCancellationProfileStatus(result *saleInvoiceCancelPreview, route docRoute, logResult erpLogResult) {
	if result == nil || result.profilePayload == nil || result.profilePayload.DocumentProfileVersion != documentProfileV1 {
		return
	}
	result.RequiredChecks = cancellationProfileRequiredChecks(route)
	result.CompletedChecks = make([]string, 0, len(result.RequiredChecks))
	for _, check := range result.RequiredChecks {
		if check != "erp_log" {
			result.CompletedChecks = append(result.CompletedChecks, check)
		}
	}
	result.LogStatus = logResult.Status
	result.LogWarning = logResult.Warning
	result.ProfileStatus = "needs_reconciliation"
	result.ReconciliationRequired = true
	if logResult.Status != "warning" {
		result.CompletedChecks = append(result.CompletedChecks, "erp_log")
		result.ProfileStatus = "complete"
		result.ReconciliationRequired = false
	}
}

func existingSaleOrderVoid(ctx context.Context, tx pgx.Tx, sourceDocNo string) (string, error) {
	var docNo string
	err := tx.QueryRow(ctx, `SELECT COALESCE(doc_no,'') FROM ic_trans WHERE trans_flag=$1 AND COALESCE(last_status,0)=0 AND doc_ref=$2 ORDER BY doc_date DESC,doc_no DESC LIMIT 1`, models.TransFlagSaleOrderCancel, strings.TrimSpace(sourceDocNo)).Scan(&docNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup existing SSC: %w", err)
	}
	return strings.TrimSpace(docNo), nil
}

func loadSaleOrderForCancel(ctx context.Context, tx pgx.Tx, sourceDocNo string, lock bool) (saleOrderForCancel, error) {
	query := `SELECT doc_no,doc_date,COALESCE(doc_time,''),COALESCE(doc_format_code,''),COALESCE(cust_code,''),COALESCE(branch_code,''),COALESCE(sale_code,''),COALESCE(wh_from,''),COALESCE(location_from,''),COALESCE(vat_type,0),COALESCE(vat_rate,0)::text,COALESCE(total_value,0)::text,COALESCE(total_vat_value,0)::text,COALESCE(total_after_vat,0)::text,COALESCE(total_amount,0)::text,COALESCE(total_before_vat,0)::text,COALESCE(total_discount,0)::text,COALESCE(total_except_vat,0)::text,COALESCE(inquiry_type,0),COALESCE(used_status,0) FROM ic_trans WHERE doc_no=$1 AND trans_flag=$2 AND COALESCE(last_status,0)=0`
	if lock {
		query += ` FOR UPDATE`
	}
	var src saleOrderForCancel
	err := tx.QueryRow(ctx, query, strings.TrimSpace(sourceDocNo), models.TransFlagSaleOrder).Scan(&src.DocNo, &src.DocDate, &src.DocTime, &src.DocFormatCode, &src.CustCode, &src.BranchCode, &src.SaleCode, &src.WHFrom, &src.LocationFrom, &src.VATType, &src.VATRate, &src.TotalValue, &src.TotalVATValue, &src.TotalAfterVAT, &src.TotalAmount, &src.TotalBeforeVAT, &src.TotalDiscount, &src.TotalExceptVAT, &src.InquiryType, &src.UsedStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return saleOrderForCancel{}, newAppError(http.StatusNotFound, "sale_order_not_found", "source sale order not found", gin.H{"source_doc_no": sourceDocNo})
	}
	if err != nil {
		return saleOrderForCancel{}, fmt.Errorf("load source sale order: %w", err)
	}
	detailQuery := `SELECT COALESCE(line_number,0),COALESCE(item_code,''),COALESCE(item_name,''),COALESCE(unit_code,''),COALESCE(is_permium,0),COALESCE(is_get_price,0),COALESCE(wh_code,''),COALESCE(shelf_code,''),COALESCE(wh_code_2,''),COALESCE(shelf_code_2,''),COALESCE(qty,0)::text,COALESCE(price,0)::text,COALESCE(price_exclude_vat,0)::text,COALESCE(discount_amount,0)::text,COALESCE(discount,''),COALESCE(total_vat_value,0)::text,COALESCE(sum_amount,0)::text,COALESCE(sum_amount_exclude_vat,0)::text,COALESCE(tax_type,0),COALESCE(vat_type,0),COALESCE(branch_code,'') FROM ic_trans_detail WHERE doc_no=$1 AND trans_flag=$2 AND COALESCE(last_status,0)=0 ORDER BY COALESCE(line_number,0),roworder LIMIT $3`
	if lock {
		detailQuery += ` FOR UPDATE`
	}
	rows, err := tx.Query(ctx, detailQuery, src.DocNo, models.TransFlagSaleOrder, maxDocumentItems+1)
	if err != nil {
		return saleOrderForCancel{}, fmt.Errorf("load source sale-order details: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var line saleOrderCancelLine
		if err := rows.Scan(&line.LineNumber, &line.ItemCode, &line.ItemName, &line.UnitCode, &line.IsPremium, &line.IsGetPrice, &line.WHCode, &line.ShelfCode, &line.WHCode2, &line.ShelfCode2, &line.Qty, &line.Price, &line.PriceExcludeVAT, &line.DiscountAmount, &line.Discount, &line.TotalVATValue, &line.SumAmount, &line.SumAmountExcludeVAT, &line.TaxType, &line.VATType, &line.BranchCode); err != nil {
			return saleOrderForCancel{}, fmt.Errorf("scan source sale-order detail: %w", err)
		}
		src.Items = append(src.Items, line)
	}
	if err := rows.Err(); err != nil {
		return saleOrderForCancel{}, err
	}
	if len(src.Items) == 0 {
		return saleOrderForCancel{}, newAppError(http.StatusConflict, "sale_order_has_no_items", "source sale order has no active detail rows", gin.H{"source_doc_no": sourceDocNo})
	}
	return src, nil
}

func insertSaleOrderCancelCore(ctx context.Context, tx pgx.Tx, src saleOrderForCancel, req saleInvoiceCancelRequest, p docPayload) (int, error) {
	docDate, _ := time.Parse("2006-01-02", p.DocDate)
	_, err := tx.Exec(ctx, `INSERT INTO ic_trans (trans_type,trans_flag,doc_date,doc_no,doc_time,doc_format_code,cust_code,branch_code,sale_code,wh_from,location_from,vat_type,vat_rate,total_value,total_vat_value,total_after_vat,total_amount,total_before_vat,total_discount,discount_word,total_except_vat,doc_ref,doc_ref_date,inquiry_type,remark,remark_2,remark_5,user_request,creator_code,cashier_code,cancel_type,send_date,credit_date,last_status) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,2,$31,$32,0)`, models.TransTypeSale, models.TransFlagSaleOrderCancel, docDate, p.DocNo, p.DocTime, p.DocFormatCode, src.CustCode, src.BranchCode, src.SaleCode, src.WHFrom, src.LocationFrom, src.VATType, src.VATRate, src.TotalValue, src.TotalVATValue, src.TotalAfterVAT, src.TotalAmount, src.TotalBeforeVAT, src.TotalDiscount, headerDiscountWord(p.TotalDiscount), src.TotalExceptVAT, src.DocNo, src.DocDate, src.InquiryType, req.Remark, req.Remark2, req.Remark5, req.UserRequest, req.CreatorCode, req.CashierCode, docDate, docDate)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, newAppError(http.StatusConflict, "duplicate_doc_no", "SSC doc_no already exists", gin.H{"doc_no": p.DocNo})
		}
		return 0, fmt.Errorf("insert SSC header: %w", err)
	}
	rowsWritten := 1
	for _, line := range src.Items {
		_, err := tx.Exec(ctx, `INSERT INTO ic_trans_detail (trans_type,trans_flag,doc_date,doc_no,line_number,cust_code,doc_time,calc_flag,inquiry_type,item_code,item_name,unit_code,is_permium,is_get_price,wh_code,shelf_code,wh_code_2,shelf_code_2,qty,price,price_exclude_vat,discount_amount,discount,total_vat_value,sum_amount,sum_amount_exclude_vat,tax_type,vat_type,ref_doc_no,ref_line_number,doc_ref_type,branch_code,last_status) VALUES ($1,$2,$3,$4,$5,$6,$7,-1,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,0,$30,0)`, models.TransTypeSale, models.TransFlagSaleOrderCancel, docDate, p.DocNo, line.LineNumber, src.CustCode, p.DocTime, src.InquiryType, line.ItemCode, line.ItemName, line.UnitCode, line.IsPremium, line.IsGetPrice, line.WHCode, line.ShelfCode, line.WHCode2, line.ShelfCode2, line.Qty, line.Price, line.PriceExcludeVAT, line.DiscountAmount, line.Discount, line.TotalVATValue, line.SumAmount, line.SumAmountExcludeVAT, line.TaxType, line.VATType, src.DocNo, line.LineNumber, line.BranchCode)
		if err != nil {
			return rowsWritten, fmt.Errorf("insert SSC detail %d: %w", line.LineNumber, err)
		}
		rowsWritten++
	}
	return rowsWritten, nil
}
