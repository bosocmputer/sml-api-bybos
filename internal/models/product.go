package models

type Product struct {
	Code                 string  `json:"code"`
	Name1                string  `json:"name_1"`
	Name2                string  `json:"name_2"`
	UnitStd              string  `json:"unit_standard"`
	UnitStdName          string  `json:"unit_standard_name"`
	GroupCode            string  `json:"group_code"`
	BalanceQty           float64 `json:"balance_qty"`
	AverageCost          float64 `json:"average_cost"`
	Price                float64 `json:"price"`
	ItemStatus           int     `json:"item_status"`
	Status               int     `json:"status"`
	ImageCount           int     `json:"image_count"`
	PrimaryImageRoworder *int    `json:"primary_image_roworder,omitempty"`
	PrimaryImageGuid     string  `json:"primary_image_guid,omitempty"`
	PrimaryImageBytes    int64   `json:"primary_image_bytes,omitempty"`
}

type ProductListResponse struct {
	Data  []Product `json:"data"`
	Total int       `json:"total"`
	Page  int       `json:"page"`
	Size  int       `json:"size"`
}

type Unit struct {
	Code  string `json:"code"`
	Name1 string `json:"name_1"`
	Name2 string `json:"name_2"`
}

type ProductUnit struct {
	Code        string  `json:"code"`
	Name1       string  `json:"name_1"`
	Name2       string  `json:"name_2"`
	StandValue  float64 `json:"stand_value"`
	DivideValue float64 `json:"divide_value"`
	IsDefault   bool    `json:"is_default"`
}
