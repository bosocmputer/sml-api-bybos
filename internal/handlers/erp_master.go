package handlers

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/middleware"
)

type erpMasterQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type ERPMasterHandler struct {
	getPool func(context.Context, string) (erpMasterQuerier, error)
}

func NewERPMasterHandler(dbm *db.Manager) *ERPMasterHandler {
	return &ERPMasterHandler{
		getPool: func(ctx context.Context, tenant string) (erpMasterQuerier, error) {
			return dbm.Get(ctx, tenant)
		},
	}
}

type ERPMasterItem struct {
	Code       string `json:"code"`
	Name1      string `json:"name_1"`
	BankCode   string `json:"bank_code,omitempty"`
	BankBranch string `json:"bank_branch,omitempty"`
	BookNumber string `json:"book_number,omitempty"`
}

type erpMasterSource struct {
	table string
	label string
}

var (
	erpBranchSource  = erpMasterSource{table: "erp_branch_list", label: "branches"}
	erpUserSource    = erpMasterSource{table: "erp_user", label: "users"}
	erpExpenseSource = erpMasterSource{table: "erp_expenses_list", label: "expenses"}
	erpIncomeSource  = erpMasterSource{table: "erp_income_list", label: "incomes"}
)

// GET /api/v1/erp/branches?search=&page=&size=
func (h *ERPMasterHandler) ListBranches(c *gin.Context) {
	h.list(c, erpBranchSource)
}

// GET /api/v1/erp/users?search=&page=&size=
func (h *ERPMasterHandler) ListUsers(c *gin.Context) {
	h.list(c, erpUserSource)
}

// GET /api/v1/erp/expenses?search=&page=&size=
func (h *ERPMasterHandler) ListExpenses(c *gin.Context) {
	h.list(c, erpExpenseSource)
}

// GET /api/v1/erp/incomes?search=&page=&size=
func (h *ERPMasterHandler) ListIncomes(c *gin.Context) {
	h.list(c, erpIncomeSource)
}

// GET /api/v1/erp/passbooks?search=&page=&size=
func (h *ERPMasterHandler) ListPassbooks(c *gin.Context) {
	search := strings.TrimSpace(c.Query("search"))
	page, size := masterPageParams(c)
	offset := (page - 1) * size

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	pool, err := h.getPool(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "connect to SML database failed", err.Error())
		return
	}

	where := "WHERE COALESCE(code,'') <> '' AND COALESCE(status,0)=0"
	args := pgx.NamedArgs{}
	if search != "" {
		where += " AND (code ILIKE @search OR name_1 ILIKE @search OR bank_code ILIKE @search OR bank_branch ILIKE @search OR book_number ILIKE @search)"
		args["search"] = "%" + search + "%"
	}

	var total int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM erp_pass_book "+where, args).Scan(&total); err != nil {
		api.Internal(c, "passbooks_count_failed", "count passbooks failed", err.Error())
		return
	}

	args["size"] = size
	args["offset"] = offset
	rows, err := pool.Query(ctx, `
		SELECT COALESCE(code,''), COALESCE(name_1,''), COALESCE(bank_code,''), COALESCE(bank_branch,''), COALESCE(book_number,'')
		  FROM erp_pass_book
		`+where+`
		 ORDER BY code
		 LIMIT @size OFFSET @offset`, args)
	if err != nil {
		api.Internal(c, "passbooks_list_failed", "list passbooks failed", err.Error())
		return
	}
	defer rows.Close()

	items := make([]ERPMasterItem, 0)
	for rows.Next() {
		var item ERPMasterItem
		if err := rows.Scan(&item.Code, &item.Name1, &item.BankCode, &item.BankBranch, &item.BookNumber); err != nil {
			api.Internal(c, "passbooks_scan_failed", "read passbook row failed", err.Error())
			return
		}
		item.Code = strings.TrimSpace(item.Code)
		item.Name1 = strings.TrimSpace(item.Name1)
		item.BankCode = strings.TrimSpace(item.BankCode)
		item.BankBranch = strings.TrimSpace(item.BankBranch)
		item.BookNumber = strings.TrimSpace(item.BookNumber)
		if item.Code == "" {
			continue
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		api.Internal(c, "passbooks_rows_failed", "read passbook rows failed", err.Error())
		return
	}

	api.OKPage(c, items, total, page, size)
}

func (h *ERPMasterHandler) list(c *gin.Context, source erpMasterSource) {
	search := strings.TrimSpace(c.Query("search"))
	page, size := masterPageParams(c)
	offset := (page - 1) * size

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	pool, err := h.getPool(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "connect to SML database failed", err.Error())
		return
	}

	where, args := erpMasterWhere(search)

	var total int
	countSQL := "SELECT COUNT(*) FROM " + source.table + " " + where
	if err := pool.QueryRow(ctx, countSQL, args).Scan(&total); err != nil {
		api.Internal(c, source.label+"_count_failed", "count "+source.label+" failed", err.Error())
		return
	}

	args["size"] = size
	args["offset"] = offset
	querySQL := "SELECT COALESCE(code,''), COALESCE(name_1,'') FROM " + source.table + " " + where + " ORDER BY code LIMIT @size OFFSET @offset"
	rows, err := pool.Query(ctx, querySQL, args)
	if err != nil {
		api.Internal(c, source.label+"_list_failed", "list "+source.label+" failed", err.Error())
		return
	}
	defer rows.Close()

	items := make([]ERPMasterItem, 0)
	for rows.Next() {
		var item ERPMasterItem
		if err := rows.Scan(&item.Code, &item.Name1); err != nil {
			api.Internal(c, source.label+"_scan_failed", "read "+source.label+" row failed", err.Error())
			return
		}
		item.Code = strings.TrimSpace(item.Code)
		item.Name1 = strings.TrimSpace(item.Name1)
		if item.Code == "" {
			continue
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		api.Internal(c, source.label+"_rows_failed", "read "+source.label+" rows failed", err.Error())
		return
	}

	api.OKPage(c, items, total, page, size)
}

func masterPageParams(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 20
	}
	return page, size
}

func erpMasterWhere(search string) (string, pgx.NamedArgs) {
	where := "WHERE COALESCE(code,'') <> ''"
	args := pgx.NamedArgs{}
	if search != "" {
		where += " AND (code ILIKE @search OR name_1 ILIKE @search)"
		args["search"] = "%" + search + "%"
	}
	return where, args
}

// GET /api/v1/erp/sml-user-list?search=&page=&size=
// Queries smlerpmaindata.sml_user_list regardless of x-tenant header.
func (h *ERPMasterHandler) ListSMLUserList(c *gin.Context) {
	search := strings.TrimSpace(c.Query("search"))
	page, size := masterPageParams(c)
	offset := (page - 1) * size

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	pool, err := h.getPool(ctx, "smlerpmaindata")
	if err != nil {
		api.Internal(c, "db_pool_error", "connect to smlerpmaindata failed", err.Error())
		return
	}

	where := "WHERE active_status = 1 AND COALESCE(user_code,'') <> ''"
	args := pgx.NamedArgs{}
	if search != "" {
		where += " AND (user_code ILIKE @search OR user_name ILIKE @search)"
		args["search"] = "%" + search + "%"
	}

	var total int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM sml_user_list "+where, args).Scan(&total); err != nil {
		api.Internal(c, "sml_user_list_count_failed", "count sml_user_list failed", err.Error())
		return
	}

	args["size"] = size
	args["offset"] = offset
	rows, err := pool.Query(ctx, `
		SELECT COALESCE(user_code,''), COALESCE(user_name,'')
		  FROM sml_user_list
		`+where+`
		 ORDER BY user_code
		 LIMIT @size OFFSET @offset`, args)
	if err != nil {
		api.Internal(c, "sml_user_list_failed", "list sml_user_list failed", err.Error())
		return
	}
	defer rows.Close()

	items := make([]ERPMasterItem, 0)
	for rows.Next() {
		var item ERPMasterItem
		if err := rows.Scan(&item.Code, &item.Name1); err != nil {
			api.Internal(c, "sml_user_list_scan_failed", "read sml_user_list row failed", err.Error())
			return
		}
		item.Code = strings.TrimSpace(item.Code)
		item.Name1 = strings.TrimSpace(item.Name1)
		if item.Code == "" {
			continue
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		api.Internal(c, "sml_user_list_rows_failed", "read sml_user_list rows failed", err.Error())
		return
	}

	api.OKPage(c, items, total, page, size)
}
