package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/middleware"
)

const companyProfileTimeout = 8 * time.Second

type CompanyProfileHandler struct {
	dbm *db.Manager
}

func NewCompanyProfileHandler(dbm *db.Manager) *CompanyProfileHandler {
	return &CompanyProfileHandler{dbm: dbm}
}

type CompanyProfile struct {
	CompanyName1    string `json:"company_name_1"`
	CompanyName2    string `json:"company_name_2"`
	BusinessName1   string `json:"business_name_1"`
	BusinessName2   string `json:"business_name_2"`
	DisplayName     string `json:"display_name"`
	Address1        string `json:"address_1"`
	Address2        string `json:"address_2"`
	TelephoneNumber string `json:"telephone_number"`
	FaxNumber       string `json:"fax_number"`
	TaxNumber       string `json:"tax_number"`
	BranchStatus    int    `json:"branch_status"`
	BranchType      int    `json:"branch_type"`
	BranchCode      string `json:"branch_code"`
}

// Get returns the single company profile for the tenant selected by middleware.
func (h *CompanyProfileHandler) Get(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), companyProfileTimeout)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "company_profile_database_unavailable", "cannot load company profile right now", nil)
		return
	}

	rows, err := pool.Query(ctx, `
SELECT COALESCE(company_name_1, ''), COALESCE(company_name_2, ''),
       COALESCE(business_name_1, ''), COALESCE(business_name_2, ''),
       COALESCE(address_1, ''), COALESCE(address_2, ''),
       COALESCE(telephone_number, ''), COALESCE(fax_number, ''),
       COALESCE(tax_number, ''), COALESCE(branch_status, 0),
       COALESCE(branch_type, 0), COALESCE(branch_code, '')
FROM public.erp_company_profile
ORDER BY roworder
LIMIT 2`)
	if err != nil {
		writeCompanyProfileQueryError(c, err)
		return
	}
	defer rows.Close()

	profiles := make([]CompanyProfile, 0, 2)
	for rows.Next() {
		var item CompanyProfile
		if err := rows.Scan(
			&item.CompanyName1, &item.CompanyName2,
			&item.BusinessName1, &item.BusinessName2,
			&item.Address1, &item.Address2,
			&item.TelephoneNumber, &item.FaxNumber,
			&item.TaxNumber, &item.BranchStatus,
			&item.BranchType, &item.BranchCode,
		); err != nil {
			api.Internal(c, "company_profile_read_failed", "cannot read company profile right now", nil)
			return
		}
		item = normalizeCompanyProfile(item)
		profiles = append(profiles, item)
	}
	if err := rows.Err(); err != nil {
		writeCompanyProfileQueryError(c, err)
		return
	}
	if len(profiles) == 0 {
		api.NotFound(c, "company_profile_missing", "company profile was not found in SML")
		return
	}
	if len(profiles) > 1 {
		api.Conflict(c, "company_profile_ambiguous", "more than one company profile exists in SML", nil)
		return
	}
	if profiles[0].DisplayName == "" {
		api.Conflict(c, "company_profile_name_missing", "company profile does not contain a company name", nil)
		return
	}

	api.OK(c, profiles[0])
}

func normalizeCompanyProfile(item CompanyProfile) CompanyProfile {
	item.CompanyName1 = strings.TrimSpace(item.CompanyName1)
	item.CompanyName2 = strings.TrimSpace(item.CompanyName2)
	item.BusinessName1 = strings.TrimSpace(item.BusinessName1)
	item.BusinessName2 = strings.TrimSpace(item.BusinessName2)
	item.Address1 = strings.TrimSpace(item.Address1)
	item.Address2 = strings.TrimSpace(item.Address2)
	item.TelephoneNumber = strings.TrimSpace(item.TelephoneNumber)
	item.FaxNumber = strings.TrimSpace(item.FaxNumber)
	item.TaxNumber = strings.TrimSpace(item.TaxNumber)
	item.BranchCode = strings.TrimSpace(item.BranchCode)
	item.DisplayName = item.CompanyName1
	if item.DisplayName == "" {
		item.DisplayName = item.BusinessName1
	}
	return item
}

func writeCompanyProfileQueryError(c *gin.Context, err error) {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(c.Request.Context().Err(), context.DeadlineExceeded) {
		api.Error(c, http.StatusGatewayTimeout, "company_profile_timeout", "company profile query timed out", nil)
		return
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
		api.NotFound(c, "company_profile_table_missing", "public.erp_company_profile was not found in SML")
		return
	}
	api.Internal(c, "company_profile_query_failed", "cannot load company profile right now", nil)
}
