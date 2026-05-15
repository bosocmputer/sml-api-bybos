package models

type Customer struct {
	Code      string `json:"code"`
	Name1     string `json:"name_1"`
	Name2     string `json:"name_2"`
	Telephone string `json:"telephone"`
	Email     string `json:"email"`
	Address   string `json:"address"`
	TaxID     string `json:"tax_id"`
	Status    int    `json:"status"`
}

type CustomerListResponse struct {
	Data  []Customer `json:"data"`
	Total int        `json:"total"`
	Page  int        `json:"page"`
	Size  int        `json:"size"`
}
