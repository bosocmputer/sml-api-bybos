package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/middleware"
)

type fakeNextStepPool struct {
	summary      NextStepMarketplaceSummary
	orders       []NextStepMarketplaceOrder
	trend        []NextStepMarketplaceTrendPoint
	rowErr       error
	queryErr     error
	lastSQL      string
	lastArgs     []any
	orderQueries int
	trendQueries int
}

func (p *fakeNextStepPool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	p.lastSQL = sql
	p.lastArgs = args
	return fakeNextStepSummaryRow{summary: p.summary, err: p.rowErr}
}

func (p *fakeNextStepPool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	p.lastSQL = sql
	p.lastArgs = args
	if p.queryErr != nil {
		return nil, p.queryErr
	}
	if strings.Contains(sql, "GROUP BY doc_date") {
		p.trendQueries++
		return &fakeNextStepTrendRows{trend: p.trend, idx: -1}, nil
	}
	p.orderQueries++
	return &fakeNextStepRows{orders: p.orders, idx: -1}, nil
}

type fakeNextStepSummaryRow struct {
	summary NextStepMarketplaceSummary
	err     error
}

func (r fakeNextStepSummaryRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*int)) = r.summary.TotalOrders
	*(dest[1].(*float64)) = r.summary.TotalAmount
	*(dest[2].(*float64)) = r.summary.CNTotalAmount
	*(dest[3].(*float64)) = r.summary.TotalExceptVAT
	*(dest[4].(*float64)) = r.summary.TotalAfterVAT
	*(dest[5].(*float64)) = r.summary.TotalVATValue
	*(dest[6].(*int)) = r.summary.PendingCount
	*(dest[7].(*int)) = r.summary.PackingCount
	*(dest[8].(*int)) = r.summary.PaymentCount
	*(dest[9].(*int)) = r.summary.SuccessCount
	*(dest[10].(*int)) = r.summary.CancelCount
	return nil
}

type fakeNextStepRows struct {
	orders []NextStepMarketplaceOrder
	idx    int
	err    error
}

func (r *fakeNextStepRows) Close() {}
func (r *fakeNextStepRows) Err() error {
	return r.err
}
func (r *fakeNextStepRows) CommandTag() pgconn.CommandTag {
	return pgconn.CommandTag{}
}
func (r *fakeNextStepRows) FieldDescriptions() []pgconn.FieldDescription {
	return nil
}
func (r *fakeNextStepRows) Next() bool {
	if r.idx+1 >= len(r.orders) {
		return false
	}
	r.idx++
	return true
}
func (r *fakeNextStepRows) Scan(dest ...any) error {
	if r.idx < 0 || r.idx >= len(r.orders) {
		return errors.New("scan before next")
	}
	o := r.orders[r.idx]
	*(dest[0].(*string)) = o.Remark5
	*(dest[1].(*string)) = o.InvDocNo
	*(dest[2].(*string)) = o.InvDocDate
	*(dest[3].(*float64)) = o.WalletAmount
	*(dest[4].(*string)) = o.RemarkQT
	*(dest[5].(*string)) = o.RemarkCancel
	*(dest[6].(*string)) = o.RemarkInv
	*(dest[7].(*string)) = o.DocNo
	*(dest[8].(*string)) = o.DocDate
	*(dest[9].(*string)) = o.DocTime
	*(dest[10].(*string)) = o.CustCode
	*(dest[11].(*int)) = o.SendType
	*(dest[12].(*string)) = o.EmpCode
	*(dest[13].(*string)) = o.EmpName
	*(dest[14].(*float64)) = o.TotalAmount
	*(dest[15].(*float64)) = o.CNTotalAmount
	*(dest[16].(*float64)) = o.TotalExceptVAT
	*(dest[17].(*float64)) = o.TotalAfterVAT
	*(dest[18].(*float64)) = o.TotalVATValue
	*(dest[19].(*float64)) = o.Balance
	*(dest[20].(*string)) = o.Status
	return nil
}
func (r *fakeNextStepRows) Values() ([]any, error) {
	return nil, nil
}
func (r *fakeNextStepRows) RawValues() [][]byte {
	return nil
}
func (r *fakeNextStepRows) Conn() *pgx.Conn {
	return nil
}

type fakeNextStepTrendRows struct {
	trend []NextStepMarketplaceTrendPoint
	idx   int
	err   error
}

func (r *fakeNextStepTrendRows) Close() {}
func (r *fakeNextStepTrendRows) Err() error {
	return r.err
}
func (r *fakeNextStepTrendRows) CommandTag() pgconn.CommandTag {
	return pgconn.CommandTag{}
}
func (r *fakeNextStepTrendRows) FieldDescriptions() []pgconn.FieldDescription {
	return nil
}
func (r *fakeNextStepTrendRows) Next() bool {
	if r.idx+1 >= len(r.trend) {
		return false
	}
	r.idx++
	return true
}
func (r *fakeNextStepTrendRows) Scan(dest ...any) error {
	if r.idx < 0 || r.idx >= len(r.trend) {
		return errors.New("scan before next")
	}
	point := r.trend[r.idx]
	*(dest[0].(*string)) = point.Date
	*(dest[1].(*float64)) = point.TotalAmount
	return nil
}
func (r *fakeNextStepTrendRows) Values() ([]any, error) {
	return nil, nil
}
func (r *fakeNextStepTrendRows) RawValues() [][]byte {
	return nil
}
func (r *fakeNextStepTrendRows) Conn() *pgx.Conn {
	return nil
}

func TestNextStepMarketplaceOrdersDoesNotRequireCustCode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	pool := &fakeNextStepPool{
		summary: NextStepMarketplaceSummary{
			TotalOrders:  1,
			TotalAmount:  1200,
			PendingCount: 1,
		},
		trend: []NextStepMarketplaceTrendPoint{
			{Date: "2026-07-01", TotalAmount: 1200},
		},
	}
	h := &NextStepMarketplaceHandler{
		getPool: func(ctx context.Context, tenant string) (nextStepMarketplaceQuerier, error) {
			return pool, nil
		},
	}
	r := nextStepTestRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/orders?date_from=2026-07-01&date_to=2026-07-03", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	args, ok := pool.lastArgs[0].(pgx.NamedArgs)
	if !ok {
		t.Fatalf("args type = %T", pool.lastArgs[0])
	}
	if _, ok := args["cust_code"]; ok {
		t.Fatalf("cust_code arg should not be used: %#v", args)
	}
	if args["doc_prefix_mqt_like"] != "MQT%" || args["doc_prefix_preqt_like"] != "PREQT%" {
		t.Fatalf("prefix args = %#v", args)
	}
	if strings.Contains(pool.lastSQL, "ic_qt.cust_code = @cust_code") {
		t.Fatalf("query should not filter by cust_code: %s", pool.lastSQL)
	}
	if !strings.Contains(pool.lastSQL, "ic_qt.doc_no ILIKE @doc_prefix_mqt_like") ||
		!strings.Contains(pool.lastSQL, "ic_qt.doc_no ILIKE @doc_prefix_preqt_like") {
		t.Fatalf("query should filter by MQT/PREQT prefixes: %s", pool.lastSQL)
	}

	var got struct {
		Success bool                              `json:"success"`
		Data    NextStepMarketplaceOrdersResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || got.Data.Meta.DocPrefix != "MQT/PREQT" || len(got.Data.Meta.DocPrefixes) != 2 {
		t.Fatalf("unexpected response = %+v", got)
	}
}

func TestNextStepMarketplaceOrdersRejectsLargeDateRange(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &NextStepMarketplaceHandler{}
	r := nextStepTestRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/orders?date_from=2026-01-01&date_to=2027-01-05", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var got api.Response
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error == nil || got.Error.Code != "invalid_date_range" {
		t.Fatalf("error = %+v", got.Error)
	}
}

func TestNextStepMarketplaceOrdersReturnsBoundedData(t *testing.T) {
	gin.SetMode(gin.TestMode)

	pool := &fakeNextStepPool{
		summary: NextStepMarketplaceSummary{
			TotalOrders:   1,
			TotalAmount:   1200,
			CNTotalAmount: 100,
			SuccessCount:  1,
		},
		orders: []NextStepMarketplaceOrder{
			{
				DocNo:         "MQT26070001",
				DocDate:       "2026-07-03",
				DocTime:       "10:30",
				CustCode:      "C001",
				EmpCode:       "E01",
				EmpName:       "Admin",
				TotalAmount:   1200,
				CNTotalAmount: 100,
				Status:        "success",
			},
		},
		trend: []NextStepMarketplaceTrendPoint{
			{Date: "2026-07-03", TotalAmount: 1200},
		},
	}
	h := &NextStepMarketplaceHandler{
		getPool: func(ctx context.Context, tenant string) (nextStepMarketplaceQuerier, error) {
			if tenant != "aoy" {
				t.Fatalf("tenant = %q, want aoy", tenant)
			}
			return pool, nil
		},
	}
	r := nextStepTestRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/orders?date_from=2026-07-01&date_to=2026-07-03&page=2&size=1&search=MQT2607&status=success", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	args, ok := pool.lastArgs[0].(pgx.NamedArgs)
	if !ok {
		t.Fatalf("args type = %T", pool.lastArgs[0])
	}
	if _, ok := args["cust_code"]; ok {
		t.Fatalf("cust_code arg should not be used: %#v", args)
	}
	if args["date_from"] != "2026-07-01" || args["date_to"] != "2026-07-03" ||
		args["doc_prefix_mqt_like"] != "MQT%" || args["doc_prefix_preqt_like"] != "PREQT%" {
		t.Fatalf("unexpected args = %#v", args)
	}
	if args["search"] != "MQT2607" || args["search_like"] != "%MQT2607%" {
		t.Fatalf("search args = %#v", args)
	}
	if args["status"] != "success" {
		t.Fatalf("status arg = %#v", args)
	}
	if args["size"] != 1 || args["offset"] != 1 {
		t.Fatalf("pagination args = %#v", args)
	}

	var got struct {
		Success bool                              `json:"success"`
		Data    NextStepMarketplaceOrdersResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || got.Data.Summary.TotalOrders != 1 || got.Data.Meta.Total != 1 {
		t.Fatalf("unexpected response = %+v", got)
	}
	if got.Data.Meta.Search != "MQT2607" {
		t.Fatalf("meta search = %q", got.Data.Meta.Search)
	}
	if got.Data.Meta.Status != "success" {
		t.Fatalf("meta status = %q", got.Data.Meta.Status)
	}
	if got.Data.Meta.DocPrefix != "MQT/PREQT" || len(got.Data.Meta.DocPrefixes) != 2 {
		t.Fatalf("meta prefixes = %+v", got.Data.Meta)
	}
	if got.Data.Summary.StatusCounts["success"] != 1 {
		t.Fatalf("status_counts = %#v", got.Data.Summary.StatusCounts)
	}
	if !strings.Contains(pool.lastSQL, "WHERE (@status = '' OR o.status = @status)") {
		t.Fatalf("orders query should filter by status: %s", pool.lastSQL)
	}
	if len(got.Data.Orders) != 1 || got.Data.Orders[0].DocNo != "MQT26070001" {
		t.Fatalf("orders = %+v", got.Data.Orders)
	}
	if len(got.Data.Trend) != 3 {
		t.Fatalf("trend length = %d, trend=%+v", len(got.Data.Trend), got.Data.Trend)
	}
	if got.Data.Trend[0].Date != "2026-07-01" || got.Data.Trend[0].TotalAmount != 0 {
		t.Fatalf("trend[0] = %+v", got.Data.Trend[0])
	}
	if got.Data.Trend[2].Date != "2026-07-03" || got.Data.Trend[2].TotalAmount != 1200 {
		t.Fatalf("trend[2] = %+v", got.Data.Trend[2])
	}
}

func TestNextStepMarketplaceOrdersRejectsInvalidStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &NextStepMarketplaceHandler{}
	r := nextStepTestRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/orders?date_from=2026-07-01&date_to=2026-07-03&status=unknown", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var got api.Response
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error == nil || got.Error.Code != "invalid_status" {
		t.Fatalf("error = %+v", got.Error)
	}
}

func TestNextStepMarketplaceOrdersCanSkipOrderRows(t *testing.T) {
	gin.SetMode(gin.TestMode)

	pool := &fakeNextStepPool{
		summary: NextStepMarketplaceSummary{
			TotalOrders:  2,
			TotalAmount:  2400,
			PendingCount: 1,
			SuccessCount: 1,
		},
		orders: []NextStepMarketplaceOrder{
			{DocNo: "MQT26070001", DocDate: "2026-07-01", TotalAmount: 1200, Status: "success"},
		},
		trend: []NextStepMarketplaceTrendPoint{
			{Date: "2026-07-01", TotalAmount: 1200},
			{Date: "2026-07-02", TotalAmount: 1200},
		},
	}
	h := &NextStepMarketplaceHandler{
		getPool: func(ctx context.Context, tenant string) (nextStepMarketplaceQuerier, error) {
			return pool, nil
		},
	}
	r := nextStepTestRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/orders?date_from=2026-07-01&date_to=2026-07-02&include_orders=false", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if pool.trendQueries != 1 || pool.orderQueries != 0 {
		t.Fatalf("query counts = trend %d order %d, want trend 1 order 0", pool.trendQueries, pool.orderQueries)
	}
	var got struct {
		Success bool                              `json:"success"`
		Data    NextStepMarketplaceOrdersResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data.Orders) != 0 {
		t.Fatalf("orders = %+v, want empty when include_orders=false", got.Data.Orders)
	}
	if got.Data.Summary.TotalOrders != 2 || got.Data.Summary.StatusCounts["pending"] != 1 {
		t.Fatalf("summary = %+v", got.Data.Summary)
	}
	if len(got.Data.Trend) != 2 || got.Data.Trend[1].TotalAmount != 1200 {
		t.Fatalf("trend = %+v", got.Data.Trend)
	}
}

func nextStepTestRouter(h *NextStepMarketplaceHandler) *gin.Engine {
	r := gin.New()
	r.GET("/orders", func(c *gin.Context) {
		c.Set(middleware.TenantKey, "aoy")
		h.Orders(c)
	})
	return r
}
