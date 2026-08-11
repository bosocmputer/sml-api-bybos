package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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
	// SourceRevision is only populated for exact and batch lookups. It is an
	// opaque fingerprint of the active SML header/detail data, not raw SML data.
	SourceRevision string `json:"source_revision,omitempty"`
}

type documentCandidateRow struct {
	DocumentCandidate
	actualTable string
	transType   int
	arName      string
	apName      string
}

type documentCandidateBatchRequest struct {
	DocFormatCode string   `json:"docFormatCode"`
	DocNos        []string `json:"docNos"`
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
	revisions, err := candidateSourceRevisions(ctx, pool, docFormatCode, []string{item.DocNo})
	if err != nil {
		api.Internal(c, "document_candidate_revision_failed", "could not verify document revision", err.Error())
		return
	}
	item.SourceRevision = revisions[candidateRevisionKey(item)]
	api.OK(c, item)
}

func (h *DocumentCandidateHandler) Batch(c *gin.Context) {
	var req documentCandidateBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, "invalid_request", "request body must be valid JSON", nil)
		return
	}
	docFormatCode := strings.ToUpper(strings.TrimSpace(req.DocFormatCode))
	if docFormatCode == "" || len(docFormatCode) > 25 {
		api.BadRequest(c, "doc_format_code_invalid", "docFormatCode is required and must not exceed 25 characters", nil)
		return
	}
	docNos, err := normalizeBatchDocumentNumbers(req.DocNos)
	if err != nil {
		api.BadRequest(c, "doc_nos_invalid", err.Error(), nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "could not get tenant database", err.Error())
		return
	}
	rows, err := pool.Query(ctx, candidateBatchQuery(), pgx.NamedArgs{
		"doc_format_code": docFormatCode,
		"doc_nos":         docNos,
	})
	if err != nil {
		api.Internal(c, "document_candidates_batch_failed", "could not load documents", err.Error())
		return
	}
	defer rows.Close()

	data := make([]DocumentCandidate, 0, len(docNos))
	found := make(map[string]bool, len(docNos))
	for rows.Next() {
		item, scanErr := scanDocumentCandidate(rows)
		if scanErr != nil {
			api.Internal(c, "document_candidates_batch_scan_failed", "could not read documents", scanErr.Error())
			return
		}
		key := strings.ToUpper(strings.TrimSpace(item.DocNo))
		if found[key] {
			continue
		}
		data = append(data, item)
		found[key] = true
	}
	if err := rows.Err(); err != nil {
		api.Internal(c, "document_candidates_batch_rows_failed", "could not read documents", err.Error())
		return
	}
	revisions, err := candidateSourceRevisions(ctx, pool, docFormatCode, docNos)
	if err != nil {
		api.Internal(c, "document_candidates_batch_revision_failed", "could not verify document revisions", err.Error())
		return
	}
	for index := range data {
		data[index].SourceRevision = revisions[candidateRevisionKey(data[index])]
	}
	missing := make([]string, 0)
	for _, docNo := range docNos {
		if !found[docNo] {
			missing = append(missing, docNo)
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"data":          data,
		"missingDocNos": missing,
	})
}

func candidateSourceRevisions(ctx context.Context, pool *pgxpool.Pool, docFormatCode string, docNos []string) (map[string]string, error) {
	rows, err := pool.Query(ctx, candidateSourceRevisionBatchQuery(), pgx.NamedArgs{
		"doc_format_code": strings.ToUpper(strings.TrimSpace(docFormatCode)),
		"doc_nos":         docNos,
	})
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]string, len(docNos))
	for rows.Next() {
		var docNo, tableName, revision string
		if err := rows.Scan(&docNo, &tableName, &revision); err != nil {
			return nil, err
		}
		result[candidateRevisionMapKey(tableName, docNo)] = revision
	}
	return result, rows.Err()
}

func candidateRevisionKey(candidate DocumentCandidate) string {
	return candidateRevisionMapKey(candidate.Table, candidate.DocNo)
}

func candidateRevisionMapKey(tableName, docNo string) string {
	return strings.ToLower(strings.TrimSpace(tableName)) + "\x00" + strings.ToUpper(strings.TrimSpace(docNo))
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

func candidateBatchQuery() string {
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
       AND t.doc_format_code = @doc_format_code
       AND t.doc_no = ANY(@doc_nos)
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
       AND t.doc_format_code = @doc_format_code
       AND t.doc_no = ANY(@doc_nos)
)
SELECT doc_no, doc_date, doc_format_code, trans_flag, table_name, trans_type,
       party_code, ar_name, ap_name, total_amount, is_lock_record
  FROM candidates
 ORDER BY doc_date DESC, doc_no DESC`
}

// numericColumnsNeedingScale lists, per source table, every `numeric` column
// observed in production without a fixed scale (information_schema.columns,
// numeric_scale IS NULL). Postgres preserves the exact literal text of an
// unscaled numeric, so an SML-side rewrite that changes only trailing-zero
// formatting (e.g. 65245.00 -> 65245.0000) flips to_jsonb()'s text output
// with zero real change to the value. Rounding these columns to a fixed
// scale before hashing removes that noise while still hashing every other
// column (including any SML adds later) unchanged, since this is applied on
// top of the whole row rather than replacing it with a curated subset.
var numericColumnsNeedingScale = map[string][]string{
	"ic_trans": {
		"advance_amount", "balance_amount", "currency_money", "exchange_rate",
		"lost_profit_exchange_amount", "money", "pay_amount", "recheck_count_day",
		"ref_amount", "ref_diff", "ref_new_amount", "sum_pay_money_diff", "sum_point",
		"sum_point_2", "total_after_vat", "total_amount", "total_amount_2",
		"total_amout_old", "total_before_vat", "total_cost", "total_debt_amount",
		"total_discount", "total_discount_2", "total_except_vat", "total_service_charge",
		"total_value", "total_value_2", "total_vat_value", "transport_value", "vat_rate",
	},
	"ic_trans_detail": {
		"average_cost", "average_cost_1", "cancel_qty", "discount_amount",
		"discount_amount_2", "divide_value", "exchange_rate", "exchange_rate_old",
		"fee_amount", "hidden_cost_1", "hidden_cost_1_exclude_vat", "hidden_cost_2",
		"hidden_cost_2_exclude_vat", "price", "price_2", "price_base", "price_default",
		"price_exclude_vat", "price_set_ratio", "profit_lost_cost_amount", "qty", "qty_2",
		"ratio", "set_ref_price", "set_ref_qty", "stand_value", "sum_amount",
		"sum_amount_2", "sum_amount_exclude_vat", "sum_of_cost", "sum_of_cost_1",
		"sum_of_cost_fix", "temp_float_1", "temp_float_2", "total_qty", "total_size",
		"total_vat_value", "transfer_amount", "unit_size",
	},
	"ap_ar_trans": {
		"amount", "currency_money", "exchange_rate", "lost_profit_exchange_amount",
		"money_balance", "sum_pay_money_cash", "sum_pay_money_chq", "sum_pay_money_credit",
		"sum_pay_money_diff", "sum_pay_money_transfer", "total_after_discount",
		"total_after_vat", "total_before_vat", "total_debt_balance", "total_debt_value",
		"total_discount", "total_net_value", "total_net_value_2", "total_pay_money",
		"total_pay_tax", "total_value", "total_vat_value", "vat_rate",
	},
	"ap_ar_trans_detail": {
		"balance_ref", "balance_ref_2", "exchange_rate", "exchange_rate_old",
		"final_amount", "lost_profit_exchange_amount", "sum_after_discount",
		"sum_before_vat", "sum_debt_amount", "sum_debt_amount_2", "sum_debt_balance",
		"sum_debt_value", "sum_discount", "sum_pay_money", "sum_pay_money_2",
		"sum_pay_money_cash", "sum_pay_money_chq", "sum_pay_money_credit",
		"sum_pay_money_transfer", "sum_tax_value", "sum_value", "vat_rate",
	},
}

// normalizedRowJSONExpr wraps a to_jsonb(alias) expression with jsonb_set
// calls that round every unscaled numeric column of table to 2 decimal
// places, so trailing-zero formatting differences no longer change the
// resulting JSON text. 2 decimal places matches the amount-comparison
// tolerance already used by legacySMLSourceMatchesDocument in paperless-v2.
// NULL values are rounded to SQL NULL, not coalesced to 0 — a real edit
// between NULL and 0 must still change the hash, since those are distinct
// states even though both are "no meaningful amount" in most SML columns.
func normalizedRowJSONExpr(alias, table string) string {
	expr := fmt.Sprintf("to_jsonb(%s)", alias)
	for _, col := range numericColumnsNeedingScale[table] {
		// jsonb_set returns SQL NULL for the WHOLE expression if the
		// replacement value itself is SQL NULL (as opposed to a JSON
		// null) — to_jsonb(ROUND(NULL::numeric,2)) is exactly that trap.
		// COALESCE to a real JSON 'null' so a NULL column normalizes to
		// {"col": null} instead of collapsing the entire row to NULL.
		expr = fmt.Sprintf(
			"jsonb_set(%s, '{%s}', COALESCE(to_jsonb(ROUND(%s.%s::numeric, 2)), 'null'::jsonb), true)",
			expr, col, alias, col,
		)
	}
	return expr
}

// candidateSourceRevisionBatchQuery deliberately runs as one bounded query for
// the full batch. The fingerprint ignores SML's own lock/status bookkeeping so
// a PaperLess lock does not make the source appear user-edited afterwards. All
// unscaled `numeric` columns are rounded to 2 decimal places before hashing
// (see numericColumnsNeedingScale) so SML-side rewrites that only change
// numeric text formatting, with no real value change, do not flip the hash.
// It also excludes `guid_code` (ic_trans/ap_ar_trans headers only — confirmed
// against production that neither detail table has this column) — a
// transient marker SML's web service writes the instant a user opens the
// document for editing, before Save, and clears on both Save and Cancel.
// Including it would false-positive on merely viewing/starting to edit an
// in-flight document, not just on an actual committed change.
//
// Runbook for finding the next transient/session marker like this one, if a
// future false-positive report shows no real business-field difference in
// the diff metadata (paperless-v2 already surfaces this per-mismatch):
//
//	-- 1. List a table's columns to spot candidates (guid/session/token-like
//	--    names are a good starting filter, but check anything unfamiliar):
//	SELECT column_name FROM information_schema.columns WHERE table_name = '<table>';
//
//	-- 2. For a suspect column, check how rarely it's populated:
//	SELECT count(*) FILTER (WHERE <col> IS NOT NULL AND <col> <> '') AS non_empty,
//	       count(*) AS total
//	FROM <table>;
//	-- If non_empty is a tiny fraction of total, and the non-empty row(s)
//	-- match a document someone currently has open for edit in the SML UI at
//	-- that moment (not a document that was edited and saved earlier), it's
//	-- likely a transient marker — exclude it the same way as guid_code.
//
// Columns already checked (found while investigating the guid_code case):
// excluded — guid_code (ic_trans, ap_ar_trans headers). Checked and left
// in the hash on purpose — doc_no_guid, period_guid (ic_trans),
// price_guid, ref_guid, is_lock_cost (ic_trans_detail): all stayed empty
// even on the one document confirmed open for edit at investigation time,
// so they don't follow the transient-marker pattern.
func candidateSourceRevisionBatchQuery() string {
	icHeader := normalizedRowJSONExpr("t", "ic_trans")
	icDetail := normalizedRowJSONExpr("d", "ic_trans_detail")
	apHeader := normalizedRowJSONExpr("t", "ap_ar_trans")
	apDetail := normalizedRowJSONExpr("d", "ap_ar_trans_detail")
	return `SELECT t.doc_no,
       'ic_trans' AS table_name,
       md5(
           (` + icHeader + ` - 'is_lock_record' - 'last_status' - 'guid_code')::text || '|' ||
           COALESCE((
               SELECT jsonb_agg((` + icDetail + `) - 'last_status' ORDER BY ((` + icDetail + `) - 'last_status')::text)::text
               FROM ic_trans_detail d
               WHERE d.doc_no = t.doc_no AND COALESCE(d.last_status,0)=0
           ), '[]')
       ) AS source_revision
  FROM ic_trans t
 WHERE COALESCE(t.last_status,0)=0
   AND t.doc_format_code = @doc_format_code
   AND t.doc_no = ANY(@doc_nos)
UNION ALL
SELECT t.doc_no,
       'ap_ar_trans' AS table_name,
       md5(
           (` + apHeader + ` - 'is_lock_record' - 'last_status' - 'guid_code')::text || '|' ||
           COALESCE((
               SELECT jsonb_agg((` + apDetail + `) - 'last_status' ORDER BY ((` + apDetail + `) - 'last_status')::text)::text
               FROM ap_ar_trans_detail d
               WHERE d.doc_no = t.doc_no AND COALESCE(d.last_status,0)=0
           ), '[]')
       ) AS source_revision
  FROM ap_ar_trans t
 WHERE COALESCE(t.last_status,0)=0
   AND t.doc_format_code = @doc_format_code
   AND t.doc_no = ANY(@doc_nos)`
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

func normalizeBatchDocumentNumbers(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > 30 {
		return nil, fmt.Errorf("docNos must contain between 1 and 30 items")
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, raw := range values {
		value := strings.ToUpper(strings.TrimSpace(raw))
		if value == "" {
			return nil, fmt.Errorf("docNos must not contain blank values")
		}
		if len([]rune(value)) > 25 {
			return nil, fmt.Errorf("docNos values must not exceed 25 characters")
		}
		if strings.ContainsAny(value, `/\\`) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return nil, fmt.Errorf("docNos contains invalid characters")
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result, nil
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
