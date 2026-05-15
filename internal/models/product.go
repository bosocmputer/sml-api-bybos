package models

type Product struct {
	Code        string   `json:"code"`
	Name1       string   `json:"name_1"`
	Name2       string   `json:"name_2"`
	UnitStd     string   `json:"unit_standard"`
	UnitStdName string   `json:"unit_standard_name"`
	BalanceQty  float64  `json:"balance_qty"`
	AverageCost float64  `json:"average_cost"`
	ItemStatus  int      `json:"item_status"`
	Status      int      `json:"status"`
}

type ProductListResponse struct {
	Data  []Product `json:"data"`
	Total int       `json:"total"`
	Page  int       `json:"page"`
	Size  int       `json:"size"`
}
