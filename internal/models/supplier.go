package models

type Supplier struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	Name1      string `json:"name_1"`
	Name2      string `json:"name_2"`
	NameEng1   string `json:"name_eng_1"`
	Firstname  string `json:"firstname"`
	Lastname   string `json:"lastname"`
	Telephone  string `json:"telephone"`
	Email      string `json:"email"`
	Address    string `json:"address"`
	Remark     string `json:"remark"`
	TaxID      string `json:"tax_id"`
	CardID     string `json:"card_id"`
	BranchType int    `json:"branch_type"`
	BranchCode string `json:"branch_code"`
	Status     int    `json:"status"`
	APStatus   int    `json:"ap_status"`
}

type SupplierListResponse struct {
	Data  []Supplier `json:"data"`
	Total int        `json:"total"`
	Page  int        `json:"page"`
	Size  int        `json:"size"`
}
