package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/middleware"
)

type DocumentCandidateHandler struct {
	dbm *db.Manager
}

func NewDocumentCandidateHandler(dbm *db.Manager) *DocumentCandidateHandler {
	return &DocumentCandidateHandler{dbm: dbm}
}

type DocumentCandidate struct {
	DocNo         string  `json:"doc_no"`
	DocDate       string  `json:"doc_date"`
	DocFormatCode string  `json:"doc_format_code"`
	TransFlag     int     `json:"trans_flag"`
	Table         string  `json:"table"`
	PartyCode     string  `json:"party_code"`
	PartyName     string  `json:"party_name"`
	PartyType     string  `json:"party_type"`
	TotalAmount   float64 `json:"total_amount"`
	IsLockRecord  int     `json:"is_lock_record"`
}

func (h *DocumentCandidateHandler) List(c *gin.Context) {
	docFormatCode := strings.ToUpper(strings.TrimSpace(c.Query("doc_format_code")))
	if docFormatCode == "" {
		api.BadRequest(c, "doc_format_code_required", "doc_format_code is required", nil)
		return
	}
	search := strings.TrimSpace(c.Query("search"))
	page, size := pageParams(c)
	table, partyType := candidateSource(docFormatCode)
	if table == "" {
		api.BadRequest(c, "unsupported_doc_format_code", "doc_format_code is not supported for document search", gin.H{"doc_format_code": docFormatCode})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "could not get tenant database", err.Error())
		return
	}

	where := "WHERE COALESCE(t.last_status,0)=0 AND upper(COALESCE(t.doc_format_code,'')) = @doc_format_code"
	args := pgx.NamedArgs{"doc_format_code": docFormatCode}
	if search != "" {
		where += " AND t.doc_no ILIKE @search"
		args["search"] = search + "%"
	}

	var total int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+table+" t "+where, args).Scan(&total); err != nil {
		api.Internal(c, "document_candidates_count_failed", "could not count documents", err.Error())
		return
	}

	args["limit"] = size
	args["offset"] = (page - 1) * size
	rows, err := pool.Query(ctx, candidateQuery(table, partyType, where), args)
	if err != nil {
		api.Internal(c, "document_candidates_failed", "could not search documents", err.Error())
		return
	}
	defer rows.Close()

	data := []DocumentCandidate{}
	for rows.Next() {
		item, err := scanDocumentCandidate(rows)
		if err != nil {
			api.Internal(c, "document_candidates_scan_failed", "could not read documents", err.Error())
			return
		}
		data = append(data, item)
	}
	if err := rows.Err(); err != nil {
		api.Internal(c, "document_candidates_rows_failed", "could not read documents", err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    data,
		"page":    page,
		"size":    size,
		"total":   total,
		"hasMore": page*size < total,
	})
}

func (h *DocumentCandidateHandler) Get(c *gin.Context) {
	docNo := strings.TrimSpace(c.Param("doc_no"))
	docFormatCode := strings.ToUpper(strings.TrimSpace(c.Query("doc_format_code")))
	if docNo == "" {
		api.BadRequest(c, "doc_no_required", "doc_no is required", nil)
		return
	}
	if docFormatCode == "" {
		api.BadRequest(c, "doc_format_code_required", "doc_format_code is required", nil)
		return
	}
	table, partyType := candidateSource(docFormatCode)
	if table == "" {
		api.BadRequest(c, "unsupported_doc_format_code", "doc_format_code is not supported for document search", gin.H{"doc_format_code": docFormatCode})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "could not get tenant database", err.Error())
		return
	}

	where := "WHERE COALESCE(t.last_status,0)=0 AND upper(COALESCE(t.doc_format_code,'')) = @doc_format_code AND t.doc_no = @doc_no"
	args := pgx.NamedArgs{"doc_format_code": docFormatCode, "doc_no": docNo, "limit": 1, "offset": 0}
	item, err := scanDocumentCandidate(pool.QueryRow(ctx, candidateQuery(table, partyType, where), args))
	if err != nil {
		if err == pgx.ErrNoRows {
			api.NotFound(c, "document_not_found", "document was not found")
			return
		}
		api.Internal(c, "document_candidate_failed", "could not load document", err.Error())
		return
	}
	api.OK(c, item)
}

func pageParams(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	if size > 50 {
		size = 50
	}
	return page, size
}

func candidateSource(docFormatCode string) (table, partyType string) {
	switch strings.ToUpper(strings.TrimSpace(docFormatCode)) {
	case "PO", "PA", "PUP":
		return "ic_trans", "AP"
	case "PB", "PV", "PBV", "PVV":
		return "ap_ar_trans", "AP"
	case "SO", "SI", "SR", "INV":
		return "ic_trans", "AR"
	default:
		if strings.HasPrefix(docFormatCode, "P") {
			return "ic_trans", "AP"
		}
		return "ic_trans", "AR"
	}
}

func candidateQuery(table, partyType, where string) string {
	partyJoin := "LEFT JOIN ar_customer p ON p.code = t.cust_code"
	if partyType == "AP" {
		partyJoin = "LEFT JOIN ap_supplier p ON p.code = t.cust_code"
	}
	totalField := "COALESCE(t.total_amount,0)"
	if table == "ap_ar_trans" {
		totalField = "COALESCE(t.total_after_vat, t.total_net_value, t.amount, 0)"
	}
	return `SELECT t.doc_no,
       t.doc_date,
       COALESCE(t.doc_format_code,''),
       t.trans_flag,
       COALESCE(t.cust_code,''),
       COALESCE(p.name_1,''),
       ` + totalField + `,
       COALESCE(t.is_lock_record,0)
FROM ` + table + ` t
` + partyJoin + `
` + where + `
ORDER BY t.doc_date DESC, t.doc_no DESC
LIMIT @limit OFFSET @offset`
}

func scanDocumentCandidate(row interface{ Scan(dest ...any) error }) (DocumentCandidate, error) {
	var item DocumentCandidate
	var docDate time.Time
	err := row.Scan(
		&item.DocNo,
		&docDate,
		&item.DocFormatCode,
		&item.TransFlag,
		&item.PartyCode,
		&item.PartyName,
		&item.TotalAmount,
		&item.IsLockRecord,
	)
	if err != nil {
		return item, err
	}
	item.DocDate = docDate.Format("2006-01-02")
	item.Table, item.PartyType = candidateSource(item.DocFormatCode)
	return item, nil
}
