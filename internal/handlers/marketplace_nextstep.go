package handlers

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/middleware"
)

const (
	nextStepDocPrefix       = "MQT"
	nextStepDefaultPageSize = 5
	nextStepMaxPageSize     = 50
	nextStepMaxDateDays     = 366
)

type nextStepMarketplaceQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type NextStepMarketplaceHandler struct {
	getPool func(context.Context, string) (nextStepMarketplaceQuerier, error)
}

func NewNextStepMarketplaceHandler(dbm *db.Manager) *NextStepMarketplaceHandler {
	return &NextStepMarketplaceHandler{
		getPool: func(ctx context.Context, tenant string) (nextStepMarketplaceQuerier, error) {
			return dbm.Get(ctx, tenant)
		},
	}
}

type NextStepMarketplaceSummary struct {
	TotalOrders    int            `json:"total_orders"`
	TotalAmount    float64        `json:"total_amount"`
	CNTotalAmount  float64        `json:"cn_total_amount"`
	TotalExceptVAT float64        `json:"total_except_vat"`
	TotalAfterVAT  float64        `json:"total_after_vat"`
	TotalVATValue  float64        `json:"total_vat_value"`
	StatusCounts   map[string]int `json:"status_counts"`
	PendingCount   int            `json:"pending_count"`
	PackingCount   int            `json:"packing_count"`
	PaymentCount   int            `json:"payment_count"`
	SuccessCount   int            `json:"success_count"`
	CancelCount    int            `json:"cancel_count"`
}

type NextStepMarketplaceOrder struct {
	Remark5        string  `json:"remark_5"`
	InvDocNo       string  `json:"inv_doc_no"`
	InvDocDate     string  `json:"inv_doc_date"`
	WalletAmount   float64 `json:"wallet_amount"`
	RemarkQT       string  `json:"remark_qt"`
	RemarkCancel   string  `json:"remark_cancel"`
	RemarkInv      string  `json:"remark_inv"`
	DocNo          string  `json:"doc_no"`
	DocDate        string  `json:"doc_date"`
	DocTime        string  `json:"doc_time"`
	CustCode       string  `json:"cust_code"`
	SendType       int     `json:"send_type"`
	EmpCode        string  `json:"emp_code"`
	EmpName        string  `json:"emp_name"`
	TotalAmount    float64 `json:"total_amount"`
	CNTotalAmount  float64 `json:"cn_total_amount"`
	TotalExceptVAT float64 `json:"total_except_vat"`
	TotalAfterVAT  float64 `json:"total_after_vat"`
	TotalVATValue  float64 `json:"total_vat_value"`
	Balance        float64 `json:"balance"`
	Status         string  `json:"status"`
}

type NextStepMarketplaceTrendPoint struct {
	Date        string  `json:"date"`
	TotalAmount float64 `json:"total_amount"`
}

type NextStepMarketplaceMeta struct {
	Tenant    string `json:"tenant"`
	CustCode  string `json:"cust_code"`
	DocPrefix string `json:"doc_prefix"`
	DateFrom  string `json:"date_from"`
	DateTo    string `json:"date_to"`
	DateBasis string `json:"date_basis"`
	Source    string `json:"source"`
	Search    string `json:"search,omitempty"`
	Page      int    `json:"page"`
	Size      int    `json:"size"`
	Total     int    `json:"total"`
}

type NextStepMarketplaceOrdersResponse struct {
	Summary NextStepMarketplaceSummary      `json:"summary"`
	Orders  []NextStepMarketplaceOrder      `json:"orders"`
	Trend   []NextStepMarketplaceTrendPoint `json:"trend"`
	Meta    NextStepMarketplaceMeta         `json:"meta"`
}

// Orders exposes NextStep marketplace MQT order lifecycle from the SML tenant DB.
// It is read-only and keeps the provided SML SQL semantics while bounding the
// query by cust_code and ic_qt.doc_date for dashboard use.
func (h *NextStepMarketplaceHandler) Orders(c *gin.Context) {
	custCode := strings.TrimSpace(c.Query("cust_code"))
	if custCode == "" {
		api.BadRequest(c, "missing_cust_code", "cust_code is required", nil)
		return
	}

	dateFrom, dateTo, errMsg := parseNextStepDateRange(c.Query("date_from"), c.Query("date_to"))
	if errMsg != "" {
		api.BadRequest(c, "invalid_date_range", errMsg, nil)
		return
	}
	search := strings.TrimSpace(c.Query("search"))
	includeOrders := parseNextStepIncludeOrders(c.Query("include_orders"))
	page, size := nextStepPageParams(c)
	offset := (page - 1) * size

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	tenant := c.GetString(middleware.TenantKey)
	pool, err := h.getPool(ctx, tenant)
	if err != nil {
		api.Internal(c, "db_pool_error", "connect to SML database failed", err.Error())
		return
	}

	args := pgx.NamedArgs{
		"cust_code":       custCode,
		"date_from":       dateFrom,
		"date_to":         dateTo,
		"doc_prefix_like": nextStepDocPrefix + "%",
		"search":          search,
		"search_like":     "%" + search + "%",
		"size":            size,
		"offset":          offset,
	}

	summary, err := h.querySummary(ctx, pool, args)
	if err != nil {
		api.Internal(c, "nextstep_summary_error", "query NextStep marketplace summary failed", err.Error())
		return
	}

	trend, err := h.queryTrend(ctx, pool, args, dateFrom, dateTo)
	if err != nil {
		api.Internal(c, "nextstep_trend_error", "query NextStep marketplace trend failed", err.Error())
		return
	}

	orders := []NextStepMarketplaceOrder{}
	if includeOrders {
		orders, err = h.queryOrders(ctx, pool, args)
		if err != nil {
			api.Internal(c, "nextstep_orders_error", "query NextStep marketplace orders failed", err.Error())
			return
		}
	}

	api.OK(c, NextStepMarketplaceOrdersResponse{
		Summary: summary,
		Orders:  orders,
		Trend:   trend,
		Meta: NextStepMarketplaceMeta{
			Tenant:    tenant,
			CustCode:  custCode,
			DocPrefix: nextStepDocPrefix,
			DateFrom:  dateFrom,
			DateTo:    dateTo,
			DateBasis: "ic_qt.doc_date",
			Source:    "sml.ic_trans",
			Search:    search,
			Page:      page,
			Size:      size,
			Total:     summary.TotalOrders,
		},
	})
}

func (h *NextStepMarketplaceHandler) querySummary(ctx context.Context, pool nextStepMarketplaceQuerier, args pgx.NamedArgs) (NextStepMarketplaceSummary, error) {
	var summary NextStepMarketplaceSummary
	err := pool.QueryRow(ctx, nextStepMarketplaceCTE+`
SELECT
  COUNT(*)::int AS total_orders,
  COALESCE(SUM(total_amount), 0)::float8 AS total_amount,
  COALESCE(SUM(cn_total_amount), 0)::float8 AS cn_total_amount,
  COALESCE(SUM(total_except_vat), 0)::float8 AS total_except_vat,
  COALESCE(SUM(total_after_vat), 0)::float8 AS total_after_vat,
  COALESCE(SUM(total_vat_value), 0)::float8 AS total_vat_value,
  COUNT(*) FILTER (WHERE status = 'pending')::int AS pending_count,
  COUNT(*) FILTER (WHERE status = 'packing')::int AS packing_count,
  COUNT(*) FILTER (WHERE status = 'payment')::int AS payment_count,
  COUNT(*) FILTER (WHERE status = 'success')::int AS success_count,
  COUNT(*) FILTER (WHERE status = 'cancel')::int AS cancel_count
FROM order_amounts`, args).Scan(
		&summary.TotalOrders,
		&summary.TotalAmount,
		&summary.CNTotalAmount,
		&summary.TotalExceptVAT,
		&summary.TotalAfterVAT,
		&summary.TotalVATValue,
		&summary.PendingCount,
		&summary.PackingCount,
		&summary.PaymentCount,
		&summary.SuccessCount,
		&summary.CancelCount,
	)
	if err != nil {
		return NextStepMarketplaceSummary{}, err
	}
	summary.StatusCounts = nextStepStatusCounts(summary)
	return summary, nil
}

func (h *NextStepMarketplaceHandler) queryTrend(ctx context.Context, pool nextStepMarketplaceQuerier, args pgx.NamedArgs, dateFrom, dateTo string) ([]NextStepMarketplaceTrendPoint, error) {
	rows, err := pool.Query(ctx, nextStepMarketplaceCTE+`
SELECT
  doc_date::text AS date,
  COALESCE(SUM(total_amount), 0)::float8 AS total_amount
FROM order_amounts
GROUP BY doc_date
ORDER BY doc_date`, args)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	amountByDate := map[string]float64{}
	for rows.Next() {
		var point NextStepMarketplaceTrendPoint
		if err := rows.Scan(&point.Date, &point.TotalAmount); err != nil {
			return nil, err
		}
		amountByDate[point.Date] = point.TotalAmount
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return fillNextStepTrend(dateFrom, dateTo, amountByDate), nil
}

func (h *NextStepMarketplaceHandler) queryOrders(ctx context.Context, pool nextStepMarketplaceQuerier, args pgx.NamedArgs) ([]NextStepMarketplaceOrder, error) {
	rows, err := pool.Query(ctx, nextStepMarketplaceCTE+`
, latest_refs AS (
  SELECT DISTINCT ON (b.doc_no)
    b.doc_no,
    COALESCE(ic_inv.remark_5, '') AS remark_5,
    COALESCE(ap_inv.doc_no, '') AS inv_doc_no,
    COALESCE(ap_inv.doc_date::text, '') AS inv_doc_date,
    COALESCE(ap_ar_cb.wallet_amount, 0)::float8 AS wallet_amount,
    COALESCE(ic_soc.remark, '') AS remark_cancel,
    COALESCE(ic_inv.remark, '') AS remark_inv,
    COALESCE(ap_ar.billing_no, '') AS ar_billing_no
  FROM base_orders b
  LEFT JOIN ic_trans ic_soc ON ic_soc.doc_ref = b.doc_no AND ic_soc.trans_flag = 31
  LEFT JOIN ap_ar_trans_detail ap_so ON ap_so.billing_no = b.doc_no AND ap_so.trans_flag = 36
  LEFT JOIN ic_trans ic_ssc ON ic_ssc.doc_ref = ap_so.doc_no AND ic_ssc.trans_flag = 37
  LEFT JOIN ap_ar_trans_detail ap_inv ON ap_inv.billing_no = ap_so.doc_no AND ap_inv.trans_flag = 44
  LEFT JOIN ic_trans ic_inv ON ic_inv.doc_no = ap_inv.doc_no AND ic_inv.trans_flag = 44
  LEFT JOIN ap_ar_trans_detail ap_ar ON ap_ar.billing_no = ap_inv.doc_no AND ap_ar.trans_flag = 239
  LEFT JOIN cb_trans ap_ar_cb ON ap_ar_cb.doc_no = ap_ar.doc_no AND ap_ar_cb.trans_flag = 239
  ORDER BY b.doc_no, ap_inv.doc_date DESC NULLS LAST, ap_inv.doc_no DESC NULLS LAST, ap_ar.doc_no DESC NULLS LAST
),
paged AS (
  SELECT
    o.*,
    COALESCE(l.remark_5, '') AS remark_5,
    COALESCE(l.inv_doc_no, '') AS inv_doc_no,
    COALESCE(l.inv_doc_date, '') AS inv_doc_date,
    COALESCE(l.wallet_amount, 0)::float8 AS wallet_amount,
    COALESCE(l.remark_cancel, '') AS remark_cancel,
    COALESCE(l.remark_inv, '') AS remark_inv,
    COALESCE(l.ar_billing_no, '') AS ar_billing_no
  FROM order_amounts o
  LEFT JOIN latest_refs l ON l.doc_no = o.doc_no
  ORDER BY o.doc_date DESC, o.doc_no DESC
  LIMIT @size OFFSET @offset
)
SELECT
  p.remark_5,
  p.inv_doc_no,
  p.inv_doc_date,
  p.wallet_amount,
  p.remark_qt,
  p.remark_cancel,
  p.remark_inv,
  p.doc_no,
  p.doc_date::text,
  p.doc_time,
  p.cust_code,
  p.send_type,
  p.emp_code,
  p.emp_name,
  p.total_amount,
  p.cn_total_amount,
  p.total_except_vat,
  p.total_after_vat,
  p.total_vat_value,
  COALESCE((
    SELECT balance_amount FROM (
      SELECT cust_code, doc_date, credit_date AS due_date, doc_no, trans_flag AS doc_type, used_status,
        doc_ref AS ref_doc_no, doc_ref_date AS ref_doc_date,
        COALESCE(total_amount,0) AS amount,
        COALESCE(total_amount,0) - (
          SELECT COALESCE(SUM(COALESCE(sum_pay_money,0)),0)
          FROM ap_ar_trans_detail
          WHERE COALESCE(last_status,0)=0
            AND trans_flag IN (239)
            AND ic_trans.doc_no=ap_ar_trans_detail.billing_no
            AND ic_trans.doc_date=ap_ar_trans_detail.billing_date
        ) AS balance_amount,
        branch_code
      FROM ic_trans
      WHERE COALESCE(last_status,0)=0
        AND trans_flag=44
        AND inquiry_type IN (0,2)
        AND cust_code=p.cust_code
      UNION ALL
      SELECT cust_code, doc_date, credit_date AS due_date, doc_no, trans_flag AS doc_type, used_status,
        '' AS ref_doc_no, NULL AS ref_doc_date,
        COALESCE(total_amount,0) AS amount,
        COALESCE(total_amount,0) - (
          SELECT COALESCE(SUM(COALESCE(sum_pay_money,0)),0)
          FROM ap_ar_trans_detail
          WHERE COALESCE(last_status,0)=0
            AND trans_flag IN (239)
            AND ic_trans.doc_no=ap_ar_trans_detail.billing_no
            AND ic_trans.doc_date=ap_ar_trans_detail.billing_date
        ) AS balance_amount,
        branch_code
      FROM ic_trans
      WHERE COALESCE(last_status,0)=0
        AND trans_flag IN (46,93,95,99,101)
        AND cust_code=p.cust_code
      UNION ALL
      SELECT cust_code, doc_date, credit_date AS due_date, doc_no, trans_flag AS doc_type, used_status,
        '' AS ref_doc_no, NULL AS ref_doc_date,
        -1*COALESCE(total_amount,0) AS amount,
        -1*(COALESCE(total_amount,0) + (
          SELECT COALESCE(SUM(COALESCE(sum_pay_money,0)),0)
          FROM ap_ar_trans_detail
          WHERE COALESCE(last_status,0)=0
            AND trans_flag IN (239)
            AND ic_trans.doc_no=ap_ar_trans_detail.billing_no
            AND ic_trans.doc_date=ap_ar_trans_detail.billing_date
        )) AS balance_amount,
        branch_code
      FROM ic_trans
      WHERE COALESCE(last_status,0)=0
        AND ((trans_flag=48 AND inquiry_type IN (0,2,4)) OR trans_flag IN (97,103))
        AND cust_code=p.cust_code
    ) AS xx
    WHERE balance_amount <> 0
      AND doc_no = p.ar_billing_no
    ORDER BY cust_code, doc_date, doc_no
    LIMIT 1
  ), 0)::float8 AS balance,
  p.status
FROM paged p
ORDER BY p.doc_date DESC, p.doc_no DESC`, args)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	orders := []NextStepMarketplaceOrder{}
	for rows.Next() {
		var order NextStepMarketplaceOrder
		if err := rows.Scan(
			&order.Remark5,
			&order.InvDocNo,
			&order.InvDocDate,
			&order.WalletAmount,
			&order.RemarkQT,
			&order.RemarkCancel,
			&order.RemarkInv,
			&order.DocNo,
			&order.DocDate,
			&order.DocTime,
			&order.CustCode,
			&order.SendType,
			&order.EmpCode,
			&order.EmpName,
			&order.TotalAmount,
			&order.CNTotalAmount,
			&order.TotalExceptVAT,
			&order.TotalAfterVAT,
			&order.TotalVATValue,
			&order.Balance,
			&order.Status,
		); err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return orders, nil
}

func parseNextStepDateRange(rawFrom, rawTo string) (string, string, string) {
	rawFrom = strings.TrimSpace(rawFrom)
	rawTo = strings.TrimSpace(rawTo)
	if rawFrom == "" || rawTo == "" {
		return "", "", "date_from and date_to are required in YYYY-MM-DD format"
	}
	from, err := time.Parse("2006-01-02", rawFrom)
	if err != nil {
		return "", "", "date_from must use YYYY-MM-DD format"
	}
	to, err := time.Parse("2006-01-02", rawTo)
	if err != nil {
		return "", "", "date_to must use YYYY-MM-DD format"
	}
	if to.Before(from) {
		return "", "", "date_to must be on or after date_from"
	}
	if int(to.Sub(from).Hours()/24)+1 > nextStepMaxDateDays {
		return "", "", "date range is too large; choose 366 days or fewer"
	}
	return rawFrom, rawTo, ""
}

func nextStepPageParams(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", strconv.Itoa(nextStepDefaultPageSize)))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > nextStepMaxPageSize {
		size = nextStepDefaultPageSize
	}
	return page, size
}

func parseNextStepIncludeOrders(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "0", "false", "no", "n":
		return false
	default:
		return true
	}
}

func nextStepStatusCounts(summary NextStepMarketplaceSummary) map[string]int {
	return map[string]int{
		"pending": summary.PendingCount,
		"packing": summary.PackingCount,
		"payment": summary.PaymentCount,
		"success": summary.SuccessCount,
		"cancel":  summary.CancelCount,
	}
}

func fillNextStepTrend(dateFrom, dateTo string, amountByDate map[string]float64) []NextStepMarketplaceTrendPoint {
	from, fromErr := time.Parse("2006-01-02", dateFrom)
	to, toErr := time.Parse("2006-01-02", dateTo)
	if fromErr != nil || toErr != nil || to.Before(from) {
		return []NextStepMarketplaceTrendPoint{}
	}
	points := make([]NextStepMarketplaceTrendPoint, 0, int(to.Sub(from).Hours()/24)+1)
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		date := day.Format("2006-01-02")
		points = append(points, NextStepMarketplaceTrendPoint{
			Date:        date,
			TotalAmount: amountByDate[date],
		})
	}
	return points
}

const nextStepMarketplaceCTE = `
WITH base_orders AS (
  SELECT
    COALESCE(ic_qt.remark, '') AS remark_qt,
    ic_qt.doc_no,
    ic_qt.doc_date,
    COALESCE(ic_qt.doc_time::text, '') AS doc_time,
    COALESCE(ic_qt.cust_code, '') AS cust_code,
    COALESCE(ic_qt.send_type, 0)::int AS send_type,
    COALESCE(ic_qt.sale_code, '') AS emp_code,
    COALESCE((
      SELECT name_1
      FROM erp_user
      WHERE UPPER(code)=UPPER(ic_qt.sale_code)
      LIMIT 1
    ), '') AS emp_name,
    COALESCE(ic_qt.total_amount, 0)::float8 AS raw_total_amount,
    COALESCE(ic_qt.total_except_vat, 0)::float8 AS raw_total_except_vat,
    COALESCE(ic_qt.total_after_vat, 0)::float8 AS raw_total_after_vat,
    COALESCE(ic_qt.total_vat_value, 0)::float8 AS raw_total_vat_value
  FROM ic_trans ic_qt
  WHERE ic_qt.trans_flag = 30
    AND ic_qt.cust_code = @cust_code
    AND ic_qt.doc_no LIKE @doc_prefix_like
    AND ic_qt.doc_date >= @date_from::date
    AND ic_qt.doc_date <= @date_to::date
    AND (
      @search = ''
      OR ic_qt.doc_no ILIKE @search_like
      OR COALESCE(ic_qt.remark, '') ILIKE @search_like
      OR COALESCE(ic_qt.sale_code, '') ILIKE @search_like
      OR EXISTS (
        SELECT 1
        FROM ap_ar_trans_detail ap_so_search
        JOIN ap_ar_trans_detail ap_inv_search
          ON ap_inv_search.billing_no = ap_so_search.doc_no
         AND ap_inv_search.trans_flag = 44
        WHERE ap_so_search.billing_no = ic_qt.doc_no
          AND ap_so_search.trans_flag = 36
          AND ap_inv_search.doc_no ILIKE @search_like
      )
    )
),
lifecycle AS (
  SELECT
    b.doc_no,
    COALESCE(BOOL_OR(ic_soc.doc_no IS NOT NULL OR ic_ssc.doc_no IS NOT NULL), false) AS has_cancel,
    COALESCE(BOOL_OR(ap_so.doc_no IS NOT NULL), false) AS has_so,
    COALESCE(BOOL_OR(ap_inv.doc_no IS NOT NULL), false) AS has_invoice,
    COALESCE(BOOL_OR(ap_ar.doc_no IS NOT NULL OR COALESCE(cb_inv.total_amount_pay, 0) > 0), false) AS has_success
  FROM base_orders b
  LEFT JOIN ic_trans ic_soc ON ic_soc.doc_ref = b.doc_no AND ic_soc.trans_flag = 31
  LEFT JOIN ap_ar_trans_detail ap_so ON ap_so.billing_no = b.doc_no AND ap_so.trans_flag = 36
  LEFT JOIN ic_trans ic_ssc ON ic_ssc.doc_ref = ap_so.doc_no AND ic_ssc.trans_flag = 37
  LEFT JOIN ap_ar_trans_detail ap_inv ON ap_inv.billing_no = ap_so.doc_no AND ap_inv.trans_flag = 44
  LEFT JOIN ic_trans ic_inv ON ic_inv.doc_no = ap_inv.doc_no AND ic_inv.trans_flag = 44
  LEFT JOIN cb_trans cb_inv ON cb_inv.doc_no = ap_inv.doc_no AND ic_inv.trans_flag = 44
  LEFT JOIN ap_ar_trans_detail ap_ar ON ap_ar.billing_no = ap_inv.doc_no AND ap_ar.trans_flag = 239
  GROUP BY b.doc_no
),
order_invoice_docs AS (
  SELECT DISTINCT
    b.doc_no AS order_doc_no,
    ic_inv.doc_no AS inv_doc_no
  FROM base_orders b
  JOIN ap_ar_trans_detail ap_so ON ap_so.billing_no = b.doc_no AND ap_so.trans_flag = 36
  JOIN ap_ar_trans_detail ap_inv ON ap_inv.billing_no = ap_so.doc_no AND ap_inv.trans_flag = 44
  JOIN ic_trans ic_inv ON ic_inv.doc_no = ap_inv.doc_no AND ic_inv.trans_flag = 44
  WHERE ic_inv.doc_no IS NOT NULL
),
sum_cn AS (
  SELECT
    d.ref_doc_no,
    COALESCE(SUM(a.total_amount), 0)::float8 AS cn_total_amount,
    COALESCE(SUM(a.total_except_vat), 0)::float8 AS cn_total_except_vat,
    COALESCE(SUM(a.total_after_vat), 0)::float8 AS cn_total_after_vat,
    COALESCE(SUM(a.total_vat_value), 0)::float8 AS cn_total_vat_value
  FROM order_invoice_docs i
  JOIN ic_trans_detail d ON d.ref_doc_no = i.inv_doc_no
  JOIN ic_trans a ON a.doc_no = d.doc_no
  GROUP BY d.ref_doc_no
),
order_cn AS (
  SELECT
    i.order_doc_no AS doc_no,
    COALESCE(SUM(cn.cn_total_amount), 0)::float8 AS cn_total_amount,
    COALESCE(SUM(cn.cn_total_except_vat), 0)::float8 AS cn_total_except_vat,
    COALESCE(SUM(cn.cn_total_after_vat), 0)::float8 AS cn_total_after_vat,
    COALESCE(SUM(cn.cn_total_vat_value), 0)::float8 AS cn_total_vat_value
  FROM order_invoice_docs i
  LEFT JOIN sum_cn cn ON cn.ref_doc_no = i.inv_doc_no
  GROUP BY i.order_doc_no
),
order_amounts AS (
  SELECT
    b.remark_qt,
    b.doc_no,
    b.doc_date,
    b.doc_time,
    b.cust_code,
    b.send_type,
    b.emp_code,
    b.emp_name,
    (b.raw_total_amount - COALESCE(cn.cn_total_amount, 0))::float8 AS total_amount,
    COALESCE(cn.cn_total_amount, 0)::float8 AS cn_total_amount,
    (b.raw_total_except_vat - COALESCE(cn.cn_total_except_vat, 0))::float8 AS total_except_vat,
    (b.raw_total_after_vat - COALESCE(cn.cn_total_after_vat, 0))::float8 AS total_after_vat,
    (b.raw_total_vat_value - COALESCE(cn.cn_total_vat_value, 0))::float8 AS total_vat_value,
    CASE
      WHEN COALESCE(l.has_cancel, false) THEN 'cancel'
      WHEN COALESCE(l.has_success, false) THEN 'success'
      WHEN COALESCE(l.has_invoice, false) THEN 'payment'
      WHEN COALESCE(l.has_so, false) THEN 'packing'
      ELSE 'pending'
    END AS status
  FROM base_orders b
  LEFT JOIN lifecycle l ON l.doc_no = b.doc_no
  LEFT JOIN order_cn cn ON cn.doc_no = b.doc_no
)`
