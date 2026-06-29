package models

type Customer struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	Name1      string `json:"name_1"`
	Name2      string `json:"name_2"`
	NameEng1   string `json:"name_eng_1"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	Telephone  string `json:"telephone"`
	Email      string `json:"email"`
	Address    string `json:"address"`
	Remark     string `json:"remark"`
	TaxID      string `json:"tax_id"`
	CardID     string `json:"card_id"`
	BranchType int    `json:"branch_type"`
	BranchCode string `json:"branch_code"`
	Status     int    `json:"status"`
	ARStatus   int    `json:"ar_status"`
}

type CustomerListResponse struct {
	Data  []Customer `json:"data"`
	Total int        `json:"total"`
	Page  int        `json:"page"`
	Size  int        `json:"size"`
}
