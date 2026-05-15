package models

// TransFlag คือประเภท document ใน ic_trans (จาก DB จริง)
const (
	TransFlagPurchaseRequest  = 2
	TransFlagPurchaseOrder    = 6
	TransFlagPurchaseInvoice  = 12
	TransFlagQuotation        = 30
	TransFlagSaleReserve      = 34
	TransFlagSaleOrder        = 36
	TransFlagSaleInvoice      = 44
	TransFlagCreditNote       = 48
	TransFlagInventoryAdjust  = 66
	TransFlagStockTransferOut = 70
	TransFlagStockTransferIn  = 72
	TransFlagEPurchaseOrder   = 260
	TransFlagPurchaseInvoiceE = 310
)

type TransactionItem struct {
	ItemCode   string  `json:"item_code"`
	ItemName   string  `json:"item_name"`
	UnitCode   string  `json:"unit_code"`
	Qty        float64 `json:"qty"`
	Price      float64 `json:"price"`
	SumAmount  float64 `json:"sum_amount"`
	WHCode     string  `json:"wh_code"`
	ShelfCode  string  `json:"shelf_code"`
	LineNumber int     `json:"line_number"`
}

type Transaction struct {
	DocNo        string            `json:"doc_no"`
	DocDate      string            `json:"doc_date"`
	DocGroup     string            `json:"doc_group"`
	TransFlag    int               `json:"trans_flag"`
	CustCode     string            `json:"cust_code"`
	VatType      int               `json:"vat_type"`
	VatRate      float64           `json:"vat_rate"`
	TotalValue   float64           `json:"total_value"`
	TotalVat     float64           `json:"total_vat_value"`
	TotalAmount  float64           `json:"total_amount"`
	Remark       string            `json:"remark"`
	Status       int               `json:"status"`
	SaleCode     string            `json:"sale_code"`
	BranchCode   string            `json:"branch_code"`
	Items        []TransactionItem `json:"items,omitempty"`
}

type TransactionListResponse struct {
	Data  []Transaction `json:"data"`
	Total int           `json:"total"`
	Page  int           `json:"page"`
	Size  int           `json:"size"`
}
