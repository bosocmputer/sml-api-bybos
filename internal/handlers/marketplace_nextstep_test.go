package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/middleware"
)

type fakeNextStepPool struct {
	summary  NextStepMarketplaceSummary
	orders   []NextStepMarketplaceOrder
	rowErr   error
	queryErr error
	lastSQL  string
	lastArgs []any
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

func TestNextStepMarketplaceOrdersRequiresCustCode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &NextStepMarketplaceHandler{}
	r := nextStepTestRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/orders?date_from=2026-07-01&date_to=2026-07-03", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var got api.Response
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error == nil || got.Error.Code != "missing_cust_code" {
		t.Fatalf("error = %+v", got.Error)
	}
}

func TestNextStepMarketplaceOrdersRejectsLargeDateRange(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &NextStepMarketplaceHandler{}
	r := nextStepTestRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/orders?cust_code=C001&date_from=2026-01-01&date_to=2027-01-05", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/orders?cust_code=C001&date_from=2026-07-01&date_to=2026-07-03&page=2&size=1&search=MQT2607", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	args, ok := pool.lastArgs[0].(pgx.NamedArgs)
	if !ok {
		t.Fatalf("args type = %T", pool.lastArgs[0])
	}
	if args["cust_code"] != "C001" || args["date_from"] != "2026-07-01" || args["date_to"] != "2026-07-03" || args["doc_prefix_like"] != "MQT%" {
		t.Fatalf("unexpected args = %#v", args)
	}
	if args["search"] != "MQT2607" || args["search_like"] != "%MQT2607%" {
		t.Fatalf("search args = %#v", args)
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
	if got.Data.Summary.StatusCounts["success"] != 1 {
		t.Fatalf("status_counts = %#v", got.Data.Summary.StatusCounts)
	}
	if len(got.Data.Orders) != 1 || got.Data.Orders[0].DocNo != "MQT26070001" {
		t.Fatalf("orders = %+v", got.Data.Orders)
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
