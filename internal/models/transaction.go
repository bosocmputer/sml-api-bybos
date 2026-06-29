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
	TransFlagARReceipt        = 239
	TransFlagEPurchaseOrder   = 260
	TransFlagPurchaseInvoiceE = 310
)

// TransType: 1=ซื้อ, 2=ขาย, 3=คลัง
const (
	TransTypePurchase  = 1
	TransTypeSale      = 2
	TransTypeInventory = 3
)

// transFlagMeta เก็บ trans_type ของแต่ละ trans_flag
var transFlagMeta = map[int]int{
	TransFlagPurchaseRequest: TransTypePurchase,
	TransFlagPurchaseOrder:   TransTypePurchase,
	TransFlagPurchaseInvoice: TransTypePurchase,
	TransFlagQuotation:       TransTypeSale,
	TransFlagSaleReserve:     TransTypeSale,
	TransFlagSaleOrder:       TransTypeSale,
	TransFlagSaleInvoice:     TransTypeSale,
	TransFlagCreditNote:      TransTypeSale,
	TransFlagInventoryAdjust: TransTypeInventory,
	TransFlagARReceipt:       TransTypeSale,
	TransFlagEPurchaseOrder:  TransTypePurchase,
}

func TransTypeOf(flag int) int {
	if t, ok := transFlagMeta[flag]; ok {
		return t
	}
	return TransTypeSale
}

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
	DocNo       string            `json:"doc_no"`
	DocDate     string            `json:"doc_date"`
	DocGroup    string            `json:"doc_group"`
	TransFlag   int               `json:"trans_flag"`
	CustCode    string            `json:"cust_code"`
	VatType     int               `json:"vat_type"`
	VatRate     float64           `json:"vat_rate"`
	TotalValue  float64           `json:"total_value"`
	TotalVat    float64           `json:"total_vat_value"`
	TotalAmount float64           `json:"total_amount"`
	Remark      string            `json:"remark"`
	Status      int               `json:"status"`
	SaleCode    string            `json:"sale_code"`
	BranchCode  string            `json:"branch_code"`
	Items       []TransactionItem `json:"items,omitempty"`
}

type TransactionListResponse struct {
	Data  []Transaction `json:"data"`
	Total int           `json:"total"`
	Page  int           `json:"page"`
	Size  int           `json:"size"`
}

// --- Write models ---

type CreateTransactionRequest struct {
	DocNo      string                     `json:"doc_no" binding:"required"`
	DocDate    string                     `json:"doc_date" binding:"required"` // YYYY-MM-DD
	TransFlag  int                        `json:"trans_flag" binding:"required"`
	CustCode   string                     `json:"cust_code"`
	VatType    int                        `json:"vat_type"` // 0=แยกนอก 1=รวมใน 2=ศูนย์%
	VatRate    float64                    `json:"vat_rate"` // เช่น 7
	SaleCode   string                     `json:"sale_code"`
	BranchCode string                     `json:"branch_code"`
	WHCode     string                     `json:"wh_code"`
	ShelfCode  string                     `json:"shelf_code"`
	Remark     string                     `json:"remark"`
	DocTime    string                     `json:"doc_time"` // HH:MM
	Items      []CreateTransactionItemReq `json:"items" binding:"required,min=1"`
}

type CreateTransactionItemReq struct {
	ItemCode  string  `json:"item_code" binding:"required"`
	ItemName  string  `json:"item_name"`
	UnitCode  string  `json:"unit_code" binding:"required"`
	Qty       float64 `json:"qty" binding:"required,gt=0"`
	Price     float64 `json:"price" binding:"required,gte=0"`
	WHCode    string  `json:"wh_code"`
	ShelfCode string  `json:"shelf_code"`
}
