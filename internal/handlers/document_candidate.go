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

type documentCandidateRow struct {
	DocumentCandidate
	actualTable string
	transType   int
	arName      string
	apName      string
}

func (h *DocumentCandidateHandler) List(c *gin.Context) {
	docFormatCode := strings.ToUpper(strings.TrimSpace(c.Query("doc_format_code")))
	if docFormatCode == "" {
		api.BadRequest(c, "doc_format_code_required", "doc_format_code is required", nil)
		return
	}
	search := truncateCandidateSearch(strings.TrimSpace(c.Query("search")))
	page, size := pageParams(c)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "could not get tenant database", err.Error())
		return
	}

	where := "WHERE upper(COALESCE(doc_format_code,'')) = @doc_format_code"
	args := pgx.NamedArgs{"doc_format_code": docFormatCode}
	if search != "" {
		where += ` AND (
    doc_no ILIKE @search ESCAPE '\'
    OR party_code ILIKE @search ESCAPE '\'
    OR ar_name ILIKE @search ESCAPE '\'
    OR ap_name ILIKE @search ESCAPE '\'
)`
		args["search"] = "%" + escapeSQLLike(search) + "%"
	}

	var total int
	if err := pool.QueryRow(ctx, candidateCountQuery(where), args).Scan(&total); err != nil {
		api.Internal(c, "document_candidates_count_failed", "could not count documents", err.Error())
		return
	}

	args["limit"] = size
	args["offset"] = (page - 1) * size
	rows, err := pool.Query(ctx, candidateListQuery(where), args)
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

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "could not get tenant database", err.Error())
		return
	}

	where := "WHERE upper(COALESCE(doc_format_code,'')) = @doc_format_code AND doc_no = @doc_no"
	args := pgx.NamedArgs{"doc_format_code": docFormatCode, "doc_no": docNo, "limit": 1, "offset": 0}
	item, err := scanDocumentCandidate(pool.QueryRow(ctx, candidateListQuery(where), args))
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

func candidateCountQuery(where string) string {
	return candidateCTE() + `
SELECT COUNT(*)
FROM candidates
` + where
}

func candidateListQuery(where string) string {
	return candidateCTE() + `
SELECT doc_no, doc_date, doc_format_code, trans_flag, table_name, trans_type,
       party_code, ar_name, ap_name, total_amount, is_lock_record
FROM candidates
` + where + `
ORDER BY doc_date DESC, doc_no DESC
LIMIT @limit OFFSET @offset`
}

func candidateCTE() string {
	return `WITH candidates AS (
    SELECT t.doc_no,
           t.doc_date,
           COALESCE(t.doc_format_code,'') AS doc_format_code,
           COALESCE(t.trans_flag,0) AS trans_flag,
           'ic_trans' AS table_name,
           COALESCE(t.trans_type,0) AS trans_type,
           COALESCE(t.cust_code,'') AS party_code,
           COALESCE(ar.name_1,'') AS ar_name,
           COALESCE(ap.name_1,'') AS ap_name,
           COALESCE(t.total_amount, 0)::double precision AS total_amount,
           COALESCE(t.is_lock_record,0) AS is_lock_record
      FROM ic_trans t
      LEFT JOIN ar_customer ar ON ar.code = t.cust_code
      LEFT JOIN ap_supplier ap ON ap.code = t.cust_code
     WHERE COALESCE(t.last_status,0)=0
    UNION ALL
    SELECT t.doc_no,
           t.doc_date,
           COALESCE(t.doc_format_code,'') AS doc_format_code,
           COALESCE(t.trans_flag,0) AS trans_flag,
           'ap_ar_trans' AS table_name,
           COALESCE(t.trans_type,0) AS trans_type,
           COALESCE(t.cust_code,'') AS party_code,
           COALESCE(ar.name_1,'') AS ar_name,
           COALESCE(ap.name_1,'') AS ap_name,
           COALESCE(
               NULLIF(t.total_after_vat, 0),
               NULLIF(t.amount, 0),
               NULLIF((
                   SELECT SUM(COALESCE(d.sum_debt_amount, 0))
                     FROM ap_ar_trans_detail d
                    WHERE d.doc_no = t.doc_no
                      AND COALESCE(d.last_status,0)=0
               ), 0),
               NULLIF((
                   SELECT SUM(COALESCE(d.sum_pay_money, 0))
                     FROM ap_ar_trans_detail d
                    WHERE d.doc_no = t.doc_no
                      AND COALESCE(d.last_status,0)=0
               ), 0),
               0
           )::double precision AS total_amount,
           COALESCE(t.is_lock_record,0) AS is_lock_record
      FROM ap_ar_trans t
      LEFT JOIN ar_customer ar ON ar.code = t.cust_code
      LEFT JOIN ap_supplier ap ON ap.code = t.cust_code
     WHERE COALESCE(t.last_status,0)=0
)`
}

func truncateCandidateSearch(value string) string {
	value = strings.TrimSpace(value)
	if len([]rune(value)) <= 120 {
		return value
	}
	return string([]rune(value)[:120])
}

func resolveCandidateSource(docFormatCode string, transFlag, transType int, actualTable string) (table, partyType string) {
	table = strings.TrimSpace(actualTable)
	partyType = partyTypeFromTransType(transType)
	if entry, ok := transFlagCatalog[transFlag]; ok {
		if strings.TrimSpace(entry.Table) != "" {
			table = entry.Table
		}
		if resolved := partyTypeFromTransType(entry.Type); resolved != "" {
			partyType = resolved
		}
	}
	if table == "" {
		table = "ic_trans"
	}
	if partyType == "" {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(docFormatCode)), "P") {
			partyType = "AP"
		} else {
			partyType = "AR"
		}
	}
	return table, partyType
}

func partyTypeFromTransType(transType int) string {
	switch transType {
	case 1, 4:
		return "AP"
	case 2, 5:
		return "AR"
	default:
		return ""
	}
}

func candidatePartyName(partyType, arName, apName string) string {
	if partyType == "AP" {
		if strings.TrimSpace(apName) != "" {
			return apName
		}
		return arName
	}
	if strings.TrimSpace(arName) != "" {
		return arName
	}
	return apName
}

func scanDocumentCandidate(row interface{ Scan(dest ...any) error }) (DocumentCandidate, error) {
	var item documentCandidateRow
	var docDate time.Time
	err := row.Scan(
		&item.DocNo,
		&docDate,
		&item.DocFormatCode,
		&item.TransFlag,
		&item.actualTable,
		&item.transType,
		&item.PartyCode,
		&item.arName,
		&item.apName,
		&item.TotalAmount,
		&item.IsLockRecord,
	)
	if err != nil {
		return item.DocumentCandidate, err
	}
	item.DocDate = docDate.Format("2006-01-02")
	item.Table, item.PartyType = resolveCandidateSource(item.DocFormatCode, item.TransFlag, item.transType, item.actualTable)
	item.PartyName = candidatePartyName(item.PartyType, item.arName, item.apName)
	return item.DocumentCandidate, nil
}
