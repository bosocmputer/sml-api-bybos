package handlers

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/middleware"
	"sml-api-bybos/internal/models"
)

const receiptDocTypeTransfer = 1
const receiptDocTypeExpense = 11

type ARReceiptHandler struct {
	dbm *db.Manager
}

func NewARReceiptHandler(dbm *db.Manager) *ARReceiptHandler {
	return &ARReceiptHandler{dbm: dbm}
}

type receiptCandidateRequest struct {
	OrderSNs []string `json:"order_sns"`
}

type receiptCandidate struct {
	OrderSN              string  `json:"order_sn"`
	InvoiceDocNo         string  `json:"invoice_doc_no,omitempty"`
	InvoiceDocDate       string  `json:"invoice_doc_date,omitempty"`
	CustCode             string  `json:"cust_code,omitempty"`
	InvoiceAmount        float64 `json:"invoice_amount,omitempty"`
	AlreadyReceived      bool    `json:"already_received"`
	ExistingReceiptDocNo string  `json:"existing_receipt_doc_no,omitempty"`
	Status               string  `json:"status"`
	Message              string  `json:"message,omitempty"`
}

type createReceiptRequest struct {
	DocNo         string              `json:"doc_no"`
	DocDate       string              `json:"doc_date" binding:"required"`
	DocTime       string              `json:"doc_time" binding:"required"`
	DocFormatCode string              `json:"doc_format_code" binding:"required"`
	CustCode      string              `json:"cust_code"`
	Remark        string              `json:"remark"`
	PassbookCode  string              `json:"passbook_code" binding:"required"`
	ExpenseCode   string              `json:"expense_code"`
	Lines         []createReceiptLine `json:"lines" binding:"required,min=1"`
}

type createReceiptLine struct {
	OrderSN      string  `json:"order_sn" binding:"required"`
	InvoiceDocNo string  `json:"invoice_doc_no"`
	PayoutAmount float64 `json:"payout_amount"`
}

type receiptInvoice struct {
	OrderSN      string
	DocNo        string
	DocDate      time.Time
	CustCode     string
	TotalAmount  float64
	ReceiptDocNo string
}

type receiptPassbook struct {
	Code       string
	Name1      string
	BankCode   string
	BankBranch string
}

// POST /api/v1/ar/receipt-candidates
func (h *ARReceiptHandler) Candidates(c *gin.Context) {
	var req receiptCandidateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, "invalid_json", "invalid receipt candidate payload", err.Error())
		return
	}
	orderSNs := normalizeOrderSNs(req.OrderSNs, 200)
	if len(orderSNs) == 0 {
		api.BadRequest(c, "validation_failed", "order_sns is required", nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "connect to SML database failed", err.Error())
		return
	}

	invoices, err := loadReceiptInvoices(ctx, pool, orderSNs, nil)
	if err != nil {
		api.Internal(c, "receipt_candidate_failed", "load receipt candidates failed", err.Error())
		return
	}

	results := make([]receiptCandidate, 0, len(orderSNs))
	for _, orderSN := range orderSNs {
		inv, ok := invoices[orderSN]
		if !ok {
			results = append(results, receiptCandidate{
				OrderSN: orderSN,
				Status:  "not_found",
				Message: "ไม่พบใบขาย SML ที่ doc_ref ตรงกับคำสั่งซื้อ Shopee",
			})
			continue
		}
		status := "ready"
		msg := ""
		if inv.ReceiptDocNo != "" {
			status = "already_received"
			msg = "ใบขายนี้เคยถูกนำไปรับชำระแล้ว"
		}
		results = append(results, receiptCandidate{
			OrderSN:              orderSN,
			InvoiceDocNo:         inv.DocNo,
			InvoiceDocDate:       inv.DocDate.Format("2006-01-02"),
			CustCode:             inv.CustCode,
			InvoiceAmount:        roundReceipt(inv.TotalAmount),
			AlreadyReceived:      inv.ReceiptDocNo != "",
			ExistingReceiptDocNo: inv.ReceiptDocNo,
			Status:               status,
			Message:              msg,
		})
	}
	api.OK(c, gin.H{"items": results})
}

// POST /api/v1/ar/receipts
func (h *ARReceiptHandler) Create(c *gin.Context) {
	var req createReceiptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, "invalid_json", "invalid receipt payload", err.Error())
		return
	}
	if err := normalizeReceiptRequest(&req); err != nil {
		api.BadRequest(c, "validation_failed", err.Error(), nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "connect to SML database failed", err.Error())
		return
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		api.Internal(c, "receipt_tx_failed", "start receipt transaction failed", err.Error())
		return
	}
	defer tx.Rollback(ctx)

	docDate, _ := time.Parse("2006-01-02", req.DocDate)
	if req.DocNo == "" {
		req.DocNo, err = nextReceiptDocNo(ctx, tx, req.DocFormatCode, docDate)
		if err != nil {
			api.Internal(c, "receipt_doc_no_failed", "ออกเลขเอกสารรับชำระไม่สำเร็จ", err.Error())
			return
		}
	}

	receipt, err := prepareReceipt(ctx, tx, req)
	if err != nil {
		writeReceiptError(c, err)
		return
	}
	if err := insertReceipt(ctx, tx, req, receipt); err != nil {
		writeReceiptError(c, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		api.Internal(c, "receipt_commit_failed", "commit receipt failed", err.Error())
		return
	}

	api.Created(c, gin.H{
		"doc_no":            req.DocNo,
		"status":            "created",
		"invoice_count":     len(receipt.Lines),
		"invoice_amount":    receipt.InvoiceAmount,
		"payout_amount":     receipt.PayoutAmount,
		"difference_amount": receipt.DifferenceAmount,
		"passbook_code":     receipt.Passbook.Code,
		"expense_code":      req.ExpenseCode,
		"trans_flag":        models.TransFlagARReceipt,
		"trans_type":        models.TransTypeSale,
	})
}

type preparedReceipt struct {
	Lines            []preparedReceiptLine
	CustCode         string
	InvoiceAmount    float64
	PayoutAmount     float64
	DifferenceAmount float64
	Passbook         receiptPassbook
}

type preparedReceiptLine struct {
	OrderSN       string
	InvoiceDocNo  string
	InvoiceDate   time.Time
	InvoiceAmount float64
	PayoutAmount  float64
}

func normalizeReceiptRequest(req *createReceiptRequest) error {
	req.DocNo = strings.TrimSpace(req.DocNo)
	req.DocDate = strings.TrimSpace(req.DocDate)
	req.DocTime = strings.TrimSpace(req.DocTime)
	req.DocFormatCode = strings.TrimSpace(req.DocFormatCode)
	req.CustCode = strings.TrimSpace(req.CustCode)
	req.Remark = strings.TrimSpace(req.Remark)
	req.PassbookCode = strings.TrimSpace(req.PassbookCode)
	req.ExpenseCode = strings.TrimSpace(req.ExpenseCode)
	if _, err := time.Parse("2006-01-02", req.DocDate); err != nil {
		return fmt.Errorf("doc_date format must be YYYY-MM-DD")
	}
	if req.DocTime == "" || len(req.DocTime) > 5 {
		return fmt.Errorf("doc_time format must be HH:MM")
	}
	if req.DocFormatCode == "" {
		return fmt.Errorf("doc_format_code is required")
	}
	if req.PassbookCode == "" {
		return fmt.Errorf("passbook_code is required")
	}
	if len(req.Lines) == 0 {
		return fmt.Errorf("lines is required")
	}
	seen := map[string]bool{}
	for i := range req.Lines {
		req.Lines[i].OrderSN = strings.TrimSpace(req.Lines[i].OrderSN)
		req.Lines[i].InvoiceDocNo = strings.TrimSpace(req.Lines[i].InvoiceDocNo)
		if req.Lines[i].OrderSN == "" {
			return fmt.Errorf("line %d order_sn is required", i)
		}
		if seen[req.Lines[i].OrderSN] {
			return fmt.Errorf("line %d duplicate order_sn %s", i, req.Lines[i].OrderSN)
		}
		seen[req.Lines[i].OrderSN] = true
		if req.Lines[i].PayoutAmount < 0 {
			return fmt.Errorf("line %d payout_amount must be >= 0", i)
		}
	}
	return nil
}

func prepareReceipt(ctx context.Context, tx pgx.Tx, req createReceiptRequest) (preparedReceipt, error) {
	if err := ensureDocFormat(ctx, tx, req.DocFormatCode); err != nil {
		return preparedReceipt{}, err
	}
	passbook, err := loadReceiptPassbook(ctx, tx, req.PassbookCode)
	if err != nil {
		return preparedReceipt{}, err
	}
	orderSNs := make([]string, 0, len(req.Lines))
	invoiceNos := make([]string, 0, len(req.Lines))
	for _, line := range req.Lines {
		orderSNs = append(orderSNs, line.OrderSN)
		if line.InvoiceDocNo != "" {
			invoiceNos = append(invoiceNos, line.InvoiceDocNo)
		}
	}
	invoices, err := loadReceiptInvoices(ctx, tx, orderSNs, invoiceNos)
	if err != nil {
		return preparedReceipt{}, fmt.Errorf("load invoices: %w", err)
	}
	var out preparedReceipt
	out.Passbook = passbook
	for i, line := range req.Lines {
		inv, ok := invoices[line.OrderSN]
		if !ok && line.InvoiceDocNo != "" {
			inv, ok = invoices[line.InvoiceDocNo]
		}
		if !ok {
			return out, newReceiptAppError(http.StatusBadRequest, "invoice_not_found", "ไม่พบใบขาย SML ของคำสั่งซื้อ "+line.OrderSN, nil)
		}
		if inv.ReceiptDocNo != "" {
			return out, newReceiptAppError(http.StatusConflict, "invoice_already_received", "ใบขาย "+inv.DocNo+" เคยรับชำระแล้วในเอกสาร "+inv.ReceiptDocNo, gin.H{"receipt_doc_no": inv.ReceiptDocNo, "invoice_doc_no": inv.DocNo, "order_sn": inv.OrderSN})
		}
		if out.CustCode == "" {
			out.CustCode = inv.CustCode
		}
		if inv.CustCode != out.CustCode {
			return out, newReceiptAppError(http.StatusBadRequest, "multi_customer_not_allowed", "รายการที่เลือกมีลูกค้าหลายรหัส กรุณาแยกส่งรับชำระ", nil)
		}
		if req.CustCode != "" && req.CustCode != inv.CustCode {
			return out, newReceiptAppError(http.StatusBadRequest, "cust_code_mismatch", "cust_code ไม่ตรงกับใบขาย SML", gin.H{"request_cust_code": req.CustCode, "invoice_cust_code": inv.CustCode})
		}
		invoiceAmount := roundReceipt(inv.TotalAmount)
		payout := roundReceipt(line.PayoutAmount)
		if payout > invoiceAmount {
			return out, newReceiptAppError(http.StatusBadRequest, "payout_exceeds_invoice", "Shopee payout มากกว่ายอดใบขาย SML รอบนี้ยังไม่รองรับ กรุณาตรวจยอดก่อนส่ง", gin.H{"order_sn": line.OrderSN, "invoice_amount": invoiceAmount, "payout_amount": payout})
		}
		out.Lines = append(out.Lines, preparedReceiptLine{
			OrderSN:       line.OrderSN,
			InvoiceDocNo:  inv.DocNo,
			InvoiceDate:   inv.DocDate,
			InvoiceAmount: invoiceAmount,
			PayoutAmount:  payout,
		})
		out.InvoiceAmount = roundReceipt(out.InvoiceAmount + invoiceAmount)
		out.PayoutAmount = roundReceipt(out.PayoutAmount + payout)
		if i == len(req.Lines)-1 {
			out.DifferenceAmount = roundReceipt(out.InvoiceAmount - out.PayoutAmount)
		}
	}
	if out.DifferenceAmount > 0 {
		if req.ExpenseCode == "" {
			return out, newReceiptAppError(http.StatusBadRequest, "expense_code_required", "กรุณาเลือกค่าใช้จ่าย Shopee สำหรับส่วนต่าง", nil)
		}
		if err := ensureExpense(ctx, tx, req.ExpenseCode); err != nil {
			return out, err
		}
	}
	return out, nil
}

func insertReceipt(ctx context.Context, tx pgx.Tx, req createReceiptRequest, receipt preparedReceipt) error {
	docDate, _ := time.Parse("2006-01-02", req.DocDate)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM ap_ar_trans WHERE doc_no=$1 AND trans_flag=$2 AND last_status=0
		UNION
		SELECT 1 FROM cb_trans WHERE doc_no=$1 AND trans_flag=$2
	)`, req.DocNo, models.TransFlagARReceipt).Scan(&exists); err != nil {
		return fmt.Errorf("check duplicate receipt: %w", err)
	}
	if exists {
		return newReceiptAppError(http.StatusConflict, "duplicate_receipt_doc_no", "เลขเอกสารรับชำระนี้มีอยู่แล้วใน SML: "+req.DocNo, nil)
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO ap_ar_trans (
			trans_type, trans_flag, doc_date, doc_no, cust_code,
			credit_day, exchange_rate, currency_money, vat_type, vat_rate,
			total_net_value, amount, money_balance,
			sum_pay_money_cash, sum_pay_money_chq, sum_pay_money_credit, sum_pay_money_transfer,
			total_before_vat, total_discount, total_pay_money, total_debt_balance,
			total_after_discount, last_status, used_status, doc_success,
			total_pay_tax, total_debt_value, sum_pay_money_diff,
			doc_time, doc_format_code, creator_code, branch_code,
			is_manual_vat, is_cancel, create_datetime, create_date_time_now, remark, status
		) VALUES (
			$1,$2,$3,$4,$5,
			0,0,0,0,0,
			$6,0,0,
			0,0,0,0,
			0,0,0,0,
			0,0,0,0,
			0,0,0,
			$7,$8,$9,'',
			0,0,NOW(),NOW(),$10,0
		)`,
		models.TransTypeSale, models.TransFlagARReceipt, docDate, req.DocNo, receipt.CustCode,
		receipt.InvoiceAmount, req.DocTime, req.DocFormatCode, "BILLFLOW", req.Remark,
	)
	if err != nil {
		return fmt.Errorf("insert ap_ar_trans: %w", err)
	}

	for i, line := range receipt.Lines {
		_, err = tx.Exec(ctx, `
			INSERT INTO ap_ar_trans_detail (
				trans_type, trans_flag, doc_date, doc_no,
				billing_no, billing_date, due_date, exchange_rate,
				sum_debt_value, sum_tax_value, sum_debt_amount, sum_discount, sum_after_discount,
				sum_pay_money_cash, sum_pay_money_chq, sum_pay_money_credit, sum_pay_money_transfer,
				sum_debt_balance, status, line_number, bill_type,
				sum_pay_money, sum_before_vat, sum_value, balance_ref, vat_rate, final_amount,
				last_status, calc_flag, ref_doc_no, ref_doc_date, create_datetime, create_date_time_now
			) VALUES (
				$1,$2,$3,$4,
				$5,$6,$6,0,
				0,0,$7,0,0,
				0,0,0,0,
				0,0,$8,$9,
				$7,0,0,$7,0,0,
				0,0,$10,$6,NOW(),NOW()
			)`,
			models.TransTypeSale, models.TransFlagARReceipt, docDate, req.DocNo,
			line.InvoiceDocNo, line.InvoiceDate, line.InvoiceAmount, i,
			models.TransFlagSaleInvoice, line.OrderSN,
		)
		if err != nil {
			return fmt.Errorf("insert ap_ar_trans_detail %d: %w", i, err)
		}
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO cb_trans (
			trans_type, trans_flag, doc_date, doc_no,
			total_amount, total_fee_amount, total_other_amount, total_tax_at_pay, total_net_amount,
			status, cash_amount, chq_amount, tranfer_amount, card_amount, total_amount_pay,
			total_income_amount, deposit_amount, advance_amount, petty_cash_amount,
			doc_time, ap_ar_code, pay_type, doc_format_code, pay_cash_amount, money_change,
			coupon_amount, point_amount, point_qty, point_rate, total_credit_charge,
			discount_amount, total_income_other, total_expense_other, total_other_currency,
			is_doc_copy, wallet_amount, total_other_currency_charge, create_date_time_now
		) VALUES (
			$1,$2,$3,$4,
			$5,0,0,0,$5,
			0,0,0,$6,0,$5,
			0,0,0,0,
			$7,$8,1,$9,0,0,
			0,0,0,0,0,
			0,$10,0,0,
			0,0,0,NOW()
		)`,
		models.TransTypeSale, models.TransFlagARReceipt, docDate, req.DocNo,
		receipt.InvoiceAmount, receipt.PayoutAmount, req.DocTime, receipt.CustCode,
		req.DocFormatCode, receipt.DifferenceAmount,
	)
	if err != nil {
		return fmt.Errorf("insert cb_trans: %w", err)
	}

	if receipt.PayoutAmount > 0 {
		if err := insertCBDetail(ctx, tx, req, docDate, 0, receiptDocTypeTransfer, receipt.Passbook.Code, receipt.Passbook.BankCode, receipt.Passbook.BankBranch, receipt.PayoutAmount); err != nil {
			return err
		}
	}
	if receipt.DifferenceAmount > 0 {
		if err := insertCBDetail(ctx, tx, req, docDate, 0, receiptDocTypeExpense, req.ExpenseCode, "", "", receipt.DifferenceAmount); err != nil {
			return err
		}
	}
	return nil
}

func insertCBDetail(ctx context.Context, tx pgx.Tx, req createReceiptRequest, docDate time.Time, lineNumber, docType int, transNumber, bankCode, bankBranch string, amount float64) error {
	var dueDate any
	if docType == receiptDocTypeTransfer {
		dueDate = docDate
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO cb_trans_detail (
			trans_type, trans_flag, doc_date, doc_no,
			trans_number, pass_book_code, bank_code, bank_branch,
			exchange_rate, amount, fee_amount, other_amount, tax_at_pay, sum_amount,
			status, line_number, chq_due_date, doc_type, doc_time,
			trans_number_type, ap_ar_type, ap_ar_code, last_status, create_date_time_now
		) VALUES (
			$1,$2,$3,$4,
			$5,'',$6,$7,
			0,$8,0,0,0,0,
			0,$9,$10,$11,$12,
			0,0,'',0,NOW()
		)`,
		models.TransTypeSale, models.TransFlagARReceipt, docDate, req.DocNo,
		transNumber, bankCode, bankBranch, amount, lineNumber, dueDate, docType, req.DocTime,
	)
	if err != nil {
		return fmt.Errorf("insert cb_trans_detail doc_type %d: %w", docType, err)
	}
	return nil
}

func loadReceiptInvoices(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, orderSNs []string, invoiceNos []string) (map[string]receiptInvoice, error) {
	rows, err := q.Query(ctx, `
		SELECT COALESCE(t.doc_ref,''), COALESCE(t.doc_no,''), t.doc_date, COALESCE(t.cust_code,''),
		       COALESCE(NULLIF(t.total_amount,0), NULLIF(t.total_after_vat,0), NULLIF(t.total_value,0), 0),
		       COALESCE((
		        SELECT d.doc_no
		          FROM ap_ar_trans_detail d
		         WHERE d.trans_flag=$3
		           AND COALESCE(d.last_status,0)=0
		           AND (d.billing_no=t.doc_no OR d.ref_doc_no=t.doc_ref)
		         ORDER BY d.roworder DESC
		         LIMIT 1
		       ), '')
		  FROM ic_trans t
		 WHERE t.trans_flag=$1
		   AND COALESCE(t.last_status,0)=0
		   AND (
		     (COALESCE(t.doc_ref,'') <> '' AND t.doc_ref = ANY($2))
		     OR (COALESCE(t.doc_no,'') <> '' AND t.doc_no = ANY($4))
		   )`,
		models.TransFlagSaleInvoice,
		orderSNs,
		models.TransFlagARReceipt,
		invoiceNos,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]receiptInvoice{}
	for rows.Next() {
		var inv receiptInvoice
		if err := rows.Scan(&inv.OrderSN, &inv.DocNo, &inv.DocDate, &inv.CustCode, &inv.TotalAmount, &inv.ReceiptDocNo); err != nil {
			return nil, err
		}
		inv.OrderSN = strings.TrimSpace(inv.OrderSN)
		inv.DocNo = strings.TrimSpace(inv.DocNo)
		inv.CustCode = strings.TrimSpace(inv.CustCode)
		inv.ReceiptDocNo = strings.TrimSpace(inv.ReceiptDocNo)
		if inv.OrderSN != "" {
			out[inv.OrderSN] = inv
		}
		if inv.DocNo != "" {
			out[inv.DocNo] = inv
		}
	}
	return out, rows.Err()
}

func loadReceiptPassbook(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, code string) (receiptPassbook, error) {
	var p receiptPassbook
	err := q.QueryRow(ctx, `
		SELECT COALESCE(code,''), COALESCE(name_1,''), COALESCE(bank_code,''), COALESCE(bank_branch,'')
		  FROM erp_pass_book
		 WHERE code=$1 AND COALESCE(status,0)=0`, code).
		Scan(&p.Code, &p.Name1, &p.BankCode, &p.BankBranch)
	if err == pgx.ErrNoRows {
		return p, newReceiptAppError(http.StatusBadRequest, "passbook_not_found", "ไม่พบบัญชีรับเงินใน SML: "+code, nil)
	}
	if err != nil {
		return p, fmt.Errorf("load passbook: %w", err)
	}
	return p, nil
}

func ensureExpense(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, code string) error {
	var ok bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM erp_expenses_list WHERE code=$1)`, code).Scan(&ok); err != nil {
		return fmt.Errorf("check expense: %w", err)
	}
	if !ok {
		return newReceiptAppError(http.StatusBadRequest, "expense_not_found", "ไม่พบรหัสค่าใช้จ่ายใน SML: "+code, nil)
	}
	return nil
}

func ensureDocFormat(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, code string) error {
	var ok bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM erp_doc_format WHERE screen_code='EE' AND code=$1)`, code).Scan(&ok); err != nil {
		return fmt.Errorf("check doc format: %w", err)
	}
	if !ok {
		return newReceiptAppError(http.StatusBadRequest, "doc_format_not_found", "ไม่พบรูปแบบเอกสารรับชำระใน SML: "+code, nil)
	}
	return nil
}

func nextReceiptDocNo(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, docFormatCode string, docDate time.Time) (string, error) {
	var format string
	if err := q.QueryRow(ctx, `SELECT COALESCE(format,'') FROM erp_doc_format WHERE screen_code='EE' AND code=$1`, docFormatCode).Scan(&format); err != nil {
		return "", err
	}
	format = strings.TrimSpace(format)
	prefix := docFormatCode
	if strings.HasPrefix(format, "@") {
		format = strings.TrimPrefix(format, "@")
	}
	if format == "" {
		format = "YYMM####"
	}
	route := docNoRoute{name: "receipt", transFlag: models.TransFlagARReceipt, table: "ap_ar_trans"}
	queryPrefix, err := docNoStaticPrefix(prefix, format, docDate)
	if err != nil {
		return "", err
	}
	rows, err := q.Query(ctx, `
		SELECT COALESCE(doc_no, '')
		  FROM ap_ar_trans
		 WHERE trans_flag=$1
		   AND COALESCE(last_status,0)=0
		   AND doc_no LIKE $2 ESCAPE '\'
		 ORDER BY doc_no DESC`,
		route.transFlag, escapeSQLLike(queryPrefix)+"%",
	)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var existing []string
	for rows.Next() {
		var docNo string
		if err := rows.Scan(&docNo); err != nil {
			return "", err
		}
		existing = append(existing, docNo)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	next, err := nextDocNoFromExisting(route, prefix, format, docDate, existing)
	if err != nil {
		return "", err
	}
	return next.NextDocNo, nil
}

func normalizeOrderSNs(values []string, max int) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

func roundReceipt(v float64) float64 {
	return math.Round(v*100) / 100
}

type receiptAppError struct {
	Status  int
	Code    string
	Message string
	Details interface{}
}

func (e *receiptAppError) Error() string { return e.Message }

func newReceiptAppError(status int, code, message string, details interface{}) *receiptAppError {
	return &receiptAppError{Status: status, Code: code, Message: message, Details: details}
}

func writeReceiptError(c *gin.Context, err error) {
	var ae *receiptAppError
	if errors.As(err, &ae) {
		api.Error(c, ae.Status, ae.Code, ae.Message, ae.Details)
		return
	}
	var pgErr *pgconn.PgError
	if (errors.As(err, &pgErr) && pgErr.Code == "23505") || strings.Contains(strings.ToLower(err.Error()), "duplicate") {
		api.Conflict(c, "duplicate_receipt", "เอกสารรับชำระนี้มีอยู่แล้วใน SML", nil)
		return
	}
	api.Internal(c, "receipt_write_failed", "write SML receipt failed", err.Error())
}

var _ interface {
	Begin(context.Context) (pgx.Tx, error)
} = (*pgxpool.Pool)(nil)
