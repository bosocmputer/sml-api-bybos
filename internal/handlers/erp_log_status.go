package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/middleware"
)

// This file answers one question for PaperLess: "has anyone touched this SML
// document since we started the signing job?" — using SML's own audit trail
// (erp_logs), which is the same source the SML ERP "ประวัติ" screen reads.
//
// It replaces a content hash of the raw ic_trans/ap_ar_trans rows. The hash
// was correct in the narrow sense (the bytes really did differ) but wrong in
// practice: it also covered columns SML rewrites on its own — used_status
// when a PO gets consumed, stock recalculation fields, transient edit markers
// — so documents nobody had opened were reported to users as "edited". That
// produced three separate false-positive incidents and destroyed user trust
// in the warning.
//
// Two things erp_logs alone does NOT cover, both verified against Damrong
// production data, and both handled by the caller in paperless-v2 rather than
// here:
//
//  1. Deletion. 143 of 143 documents with last_status<>0 had NO erp_logs
//     delete row. Detecting a deleted document stays with the existing
//     candidate lookup, whose WHERE clause filters last_status<>0 and so
//     reports the document as missing.
//
//  2. Recreation under the same doc_no. 33 documents in six weeks had two
//     function_code=1 (create) rows for one doc_no with different amounts
//     (e.g. 743.00 then 12,469.00, by two different users) and no edit row
//     between them. A check that only looked for function_code=2 would let
//     that through, so CreatedAfterBaseline below is reported separately and
//     the caller treats it as a blocking change.

const (
	erpLogFunctionCreate = 1
	erpLogFunctionEdit   = 2
	erpLogFunctionDelete = 3
)

// erpLogStatusTimeout bounds the status check. PaperLess calls this on every
// signature step, where a slow answer is worse than no answer — its caller
// treats a timeout as "could not verify" rather than blocking the signer.
const erpLogStatusTimeout = 3 * time.Second

// erpLogHistoryTimeout is longer than the status check because it decodes
// payloads (14KB typical, 330KB worst case) for display. It only runs when a
// user explicitly opens the change list, never on the signing hot path.
const erpLogHistoryTimeout = 10 * time.Second

// maxERPLogHistoryEntries caps how many edit rounds are decoded for display.
// A heavily-revised document can carry dozens of rows; rendering all of them
// would mean tens of MB of payload for a list nobody reads past the first few
// entries. The response reports the true total so the UI can say "showing N
// of M".
const maxERPLogHistoryEntries = 20

// ERPLogStatusHandler serves the audit-trail checks over the tenant's
// <tenant>_logs database.
type ERPLogStatusHandler struct {
	dbm *db.Manager
}

func NewERPLogStatusHandler(dbm *db.Manager) *ERPLogStatusHandler {
	return &ERPLogStatusHandler{dbm: dbm}
}

// ERPLogBaseline is the marker PaperLess stores when a signing job starts.
//
// It is a roworder (the erp_logs primary key), deliberately NOT a timestamp.
// erp_logs.date_time is `timestamp without time zone` written by the SML
// server in local time, while PaperLess stores UTC. Comparing across those
// two clocks needs a timezone assumption that is wrong the moment a shop's
// server drifts or DST-like adjustments happen, and the failure mode is
// silent: an edit lands inside the skew window and is never reported.
// roworder is monotonic within the table and needs no clock at all.
type ERPLogBaseline struct {
	// Roworder is the highest erp_logs.roworder for this document at the
	// moment the signing job was created. Zero means the document had no log
	// rows yet, which is normal for a document created outside the ERP UI.
	Roworder int64 `json:"roworder"`
	// LogCount is how many rows existed at baseline time. It is informational
	// and lets a caller detect a truncated/rebuilt log table (count shrinking
	// while roworder grows).
	LogCount int `json:"logCount"`
	// CapturedAt is the SML-side timestamp of the baseline row, for display
	// only. Never used for comparison — see the roworder rationale above.
	CapturedAt string `json:"capturedAt,omitempty"`
}

// ERPLogStatus is the answer to "did anything happen after the baseline?".
type ERPLogStatus struct {
	DocNo     string `json:"docNo"`
	TransFlag int    `json:"transFlag"`
	// Baseline echoes back what was compared against, so a caller can log
	// exactly what it asked.
	Baseline int64 `json:"baseline"`
	// CurrentRoworder is the newest log row now, which the caller stores as
	// the new baseline after an accepted re-check.
	CurrentRoworder int64 `json:"currentRoworder"`
	// EditedAfterBaseline is true when at least one function_code=2 row
	// exists after the baseline. It does NOT mean a user-visible field
	// changed — a large share of edit rows in production are plain re-saves
	// with an identical payload. Use HasMeaningfulChange for that.
	EditedAfterBaseline bool `json:"editedAfterBaseline"`
	// CreatedAfterBaseline is true when a create row appeared after the
	// baseline, meaning the document number was reused for a different
	// document. Always treat this as blocking: the payload is a fresh
	// document, so there is no meaningful before/after to diff.
	CreatedAfterBaseline bool `json:"createdAfterBaseline"`
	// DeletedAfterBaseline reports a function_code=3 row. SML does not write
	// one for the common "cancel a document" path (it sets last_status
	// instead), so this is a supplementary signal, never the primary
	// deletion check.
	DeletedAfterBaseline bool `json:"deletedAfterBaseline"`
	// HasMeaningfulChange is true only when a normalized diff of some edit
	// after the baseline is non-empty. This is the signal that should gate
	// blocking a document.
	HasMeaningfulChange bool `json:"hasMeaningfulChange"`
	// ChangeCount is how many normalized field changes were found across all
	// edits after the baseline.
	ChangeCount int `json:"changeCount"`
	// DiffUnavailable is set when edits exist but could not be diffed (a
	// payload over the size cap, or malformed JSON). The caller must treat
	// this as "changed" and fall back to a generic message rather than
	// silently passing the document.
	DiffUnavailable bool `json:"diffUnavailable,omitempty"`
	// LatestEditor / LatestEditedAt describe the most recent edit, for the
	// message shown to the user.
	LatestEditor   string `json:"latestEditor,omitempty"`
	LatestComputer string `json:"latestComputer,omitempty"`
	LatestMenu     string `json:"latestMenu,omitempty"`
	LatestEditedAt string `json:"latestEditedAt,omitempty"`
	// Changes is a bounded preview of what changed, enough for the blocking
	// message without a second request.
	Changes []erpFieldChange `json:"changes,omitempty"`
}

// ERPLogHistoryEntry is one audit-trail row with its decoded diff.
type ERPLogHistoryEntry struct {
	Roworder     int64            `json:"roworder"`
	FunctionCode int              `json:"functionCode"`
	Action       string           `json:"action"`
	UserCode     string           `json:"userCode"`
	ComputerName string           `json:"computerName"`
	MenuName     string           `json:"menuName"`
	DateTime     string           `json:"dateTime"`
	DocAmount    float64          `json:"docAmount"`
	OldDocAmount float64          `json:"oldDocAmount"`
	Changes      []erpFieldChange `json:"changes,omitempty"`
	// DiffSkipped explains why Changes is empty despite this being an edit —
	// either the payload exceeded the size cap or failed to decode. Without
	// it, "no changes" and "could not read changes" look identical.
	DiffSkipped string `json:"diffSkipped,omitempty"`
}

// erpLogRow is the raw scan target shared by the status and history queries.
type erpLogRow struct {
	roworder     int64
	functionCode int
	userCode     string
	computerName string
	menuName     string
	dateTime     *time.Time
	docAmount    float64
	oldDocAmount float64
	dataOld      []byte
	dataNew      []byte
}

// paperlessLogFilter excludes rows PaperLess itself wrote through the compat
// write path (see handlers/compat/write.go, which stamps user_code=BILLFLOW
// or a BillFlow% menu name). Without this, PaperLess pushing a document into
// SML would register as "a user edited this document" on the next check and
// block the very document it just wrote.
const paperlessLogFilter = `
	  AND COALESCE(user_code,'') <> 'BILLFLOW'
	  AND COALESCE(menu_name,'') NOT LIKE 'BillFlow%'`

// logsDBName derives the audit database for a tenant. SML's convention is a
// sibling database suffixed _logs (drh -> drh_logs), already relied on by the
// compat write path.
func logsDBName(tenant string) string {
	return strings.TrimSpace(tenant) + "_logs"
}

// errNoLogsDatabase marks the case where the tenant's _logs database is
// unreachable, so callers can distinguish "cannot verify" from "verified, no
// changes" — collapsing those two would silently pass unverified documents.
var errNoLogsDatabase = errors.New("logs database unavailable")

// errLogsDatabaseAbsent means the tenant has no audit database at all, which
// is different from one that exists but cannot be reached right now.
//
// This is a real, permanent configuration for some tenants: a database
// restored from a backup arrives without its sibling _logs database, and SML
// never recreates one (confirmed on the stpt tenant, 622k documents and no
// stpt_logs). A tenant like that can never answer "who edited this", so
// treating it as a transient outage would block every one of its documents
// forever. Callers use this to fall back to the pre-existing verification
// path instead, keeping those shops working exactly as they do today.
var errLogsDatabaseAbsent = errors.New("logs database does not exist")

// logsDatabaseAbsent recognizes Postgres's "database ... does not exist"
// from a connection attempt. Matching on message text is unpleasant but is
// the same approach the compat write path already uses for this condition,
// and the alternative (probing pg_database on every request) costs a round
// trip on the signing hot path to learn something that essentially never
// changes for a tenant.
func logsDatabaseAbsent(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "does not exist")
}

func (h *ERPLogStatusHandler) logsPool(ctx context.Context, c *gin.Context) (*pgxpool.Pool, string, error) {
	tenant := strings.TrimSpace(c.GetString(middleware.TenantKey))
	if tenant == "" {
		return nil, "", fmt.Errorf("%w: tenant missing", errNoLogsDatabase)
	}
	logDB := logsDBName(tenant)
	pool, err := h.dbm.Get(ctx, logDB)
	if err != nil {
		if logsDatabaseAbsent(err) {
			return nil, logDB, fmt.Errorf("%w: %s", errLogsDatabaseAbsent, logDB)
		}
		return nil, logDB, fmt.Errorf("%w: %s: %v", errNoLogsDatabase, logDB, err)
	}
	return pool, logDB, nil
}

// writeLogsPoolError turns a pool failure into the right HTTP response.
//
// A tenant with no audit database gets 501 Not Implemented with a stable
// code, so the caller can recognize "this shop cannot be checked this way"
// and fall back, rather than retrying a condition that will never clear. A
// reachable-but-failing database keeps 503, which is genuinely retryable.
func writeLogsPoolError(c *gin.Context, logDB string, err error) {
	if errors.Is(err, errLogsDatabaseAbsent) {
		api.Error(c, http.StatusNotImplemented, "erp_logs_not_available",
			"ร้านนี้ไม่มีฐานข้อมูลประวัติ ("+logDB+") จึงตรวจสอบการแก้ไขจากประวัติไม่ได้", nil)
		return
	}
	api.Error(c, http.StatusServiceUnavailable, "erp_logs_unavailable",
		"ไม่สามารถเชื่อมต่อฐานข้อมูลประวัติ ("+logDB+") ได้", err.Error())
}

// parseTransFlag reads the required trans_flag query parameter.
//
// It is required rather than optional because doc_no is not unique on its
// own: production data contains doc_no values shared across trans_flag
// values. Defaulting it would make the query silently read another
// document's audit trail.
func parseTransFlag(c *gin.Context) (int, bool) {
	raw := strings.TrimSpace(c.Query("trans_flag"))
	if raw == "" {
		api.BadRequest(c, "trans_flag_required", "trans_flag is required", nil)
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		api.BadRequest(c, "trans_flag_invalid", "trans_flag must be an integer", nil)
		return 0, false
	}
	return value, true
}

func formatLogTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	// SML stores local wall-clock time with no zone. Rendering it as a plain
	// "YYYY-MM-DD HH:MM:SS" keeps it honest: it is displayed exactly as the
	// ERP history screen shows it, with no implied UTC conversion.
	return t.Format("2006-01-02 15:04:05")
}

// Baseline returns the current newest log row for a document, which the
// caller stores when a signing job starts.
//
// GET /v1/ic/documents/:doc_no/erp-log-baseline?trans_flag=12
func (h *ERPLogStatusHandler) Baseline(c *gin.Context) {
	docNo := strings.TrimSpace(c.Param("doc_no"))
	if docNo == "" {
		api.BadRequest(c, "doc_no_required", "doc_no is required", nil)
		return
	}
	transFlag, ok := parseTransFlag(c)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), erpLogStatusTimeout)
	defer cancel()

	pool, logDB, err := h.logsPool(ctx, c)
	if err != nil {
		writeLogsPoolError(c, logDB, err)
		return
	}

	var (
		maxRow   *int64
		count    int
		lastTime *time.Time
	)
	err = pool.QueryRow(ctx, `
		SELECT MAX(roworder), COUNT(*), MAX(date_time)
		  FROM erp_logs
		 WHERE doc_no = @doc_no AND trans_flag = @trans_flag`+paperlessLogFilter,
		pgx.NamedArgs{"doc_no": docNo, "trans_flag": transFlag},
	).Scan(&maxRow, &count, &lastTime)
	if err != nil {
		api.Internal(c, "erp_log_baseline_failed", "could not read document history", err.Error())
		return
	}

	baseline := ERPLogBaseline{LogCount: count, CapturedAt: formatLogTime(lastTime)}
	if maxRow != nil {
		baseline.Roworder = *maxRow
	}
	api.OK(c, baseline)
}

// Status reports what happened after a baseline.
//
// GET /v1/ic/documents/:doc_no/erp-log-status?trans_flag=12&baseline=123
func (h *ERPLogStatusHandler) Status(c *gin.Context) {
	docNo := strings.TrimSpace(c.Param("doc_no"))
	if docNo == "" {
		api.BadRequest(c, "doc_no_required", "doc_no is required", nil)
		return
	}
	transFlag, ok := parseTransFlag(c)
	if !ok {
		return
	}
	baseline, err := strconv.ParseInt(strings.TrimSpace(c.DefaultQuery("baseline", "0")), 10, 64)
	if err != nil || baseline < 0 {
		api.BadRequest(c, "baseline_invalid", "baseline must be a non-negative integer", nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), erpLogStatusTimeout)
	defer cancel()

	pool, logDB, err := h.logsPool(ctx, c)
	if err != nil {
		writeLogsPoolError(c, logDB, err)
		return
	}

	status, err := loadERPLogStatus(ctx, pool, docNo, transFlag, baseline)
	if err != nil {
		api.Internal(c, "erp_log_status_failed", "could not read document history", err.Error())
		return
	}
	api.OK(c, status)
}

// erpLogQuerier is the minimal pool surface the query helpers need, so tests
// can drive them without a live database.
type erpLogQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// loadERPLogStatus runs the post-baseline analysis.
//
// Payloads are fetched here even though this is the hot path, because
// distinguishing a real edit from a no-op re-save is the entire point: over
// half of production edit rows change nothing a user would recognize, and
// blocking on those is what made the previous implementation unusable. The
// cost is bounded by only reading rows after the baseline (normally zero, at
// most a handful) rather than a document's full history.
func loadERPLogStatus(ctx context.Context, pool erpLogQuerier, docNo string, transFlag int, baseline int64) (ERPLogStatus, error) {
	status := ERPLogStatus{DocNo: docNo, TransFlag: transFlag, Baseline: baseline, CurrentRoworder: baseline}

	rows, err := pool.Query(ctx, `
		SELECT roworder, COALESCE(function_code,0), COALESCE(user_code,''),
		       COALESCE(computer_name,''), COALESCE(menu_name,''), date_time,
		       COALESCE(doc_amount,0), COALESCE(old_doc_amount,0),
		       data_old, data_new
		  FROM erp_logs
		 WHERE doc_no = @doc_no AND trans_flag = @trans_flag AND roworder > @baseline`+paperlessLogFilter+`
		 ORDER BY roworder`,
		pgx.NamedArgs{"doc_no": docNo, "trans_flag": transFlag, "baseline": baseline},
	)
	if err != nil {
		return status, err
	}
	defer rows.Close()

	var changes []erpFieldChange
	for rows.Next() {
		var r erpLogRow
		if err := rows.Scan(&r.roworder, &r.functionCode, &r.userCode, &r.computerName,
			&r.menuName, &r.dateTime, &r.docAmount, &r.oldDocAmount, &r.dataOld, &r.dataNew); err != nil {
			return status, err
		}
		if r.roworder > status.CurrentRoworder {
			status.CurrentRoworder = r.roworder
		}

		switch r.functionCode {
		case erpLogFunctionCreate:
			// The document number was reused for a different document. There
			// is no before/after to diff, so this is unconditionally a
			// blocking change regardless of payload contents.
			status.CreatedAfterBaseline = true
			status.HasMeaningfulChange = true
		case erpLogFunctionDelete:
			status.DeletedAfterBaseline = true
			status.HasMeaningfulChange = true
		case erpLogFunctionEdit:
			status.EditedAfterBaseline = true
			rowChanges, diffErr := diffERPLogPayloads(r.dataOld, r.dataNew)
			if diffErr != nil {
				// Could not tell whether this edit mattered. Fail safe:
				// report it as a change rather than letting an unreadable
				// payload pass as "nothing happened".
				status.DiffUnavailable = true
				status.HasMeaningfulChange = true
				continue
			}
			if len(rowChanges) > 0 {
				status.HasMeaningfulChange = true
				changes = append(changes, rowChanges...)
			}
		}

		if r.functionCode == erpLogFunctionEdit || r.functionCode == erpLogFunctionCreate {
			status.LatestEditor = r.userCode
			status.LatestComputer = r.computerName
			status.LatestMenu = r.menuName
			status.LatestEditedAt = formatLogTime(r.dateTime)
		}
	}
	if err := rows.Err(); err != nil {
		return status, err
	}

	status.ChangeCount = len(changes)
	if len(changes) > maxERPLogHistoryEntries {
		changes = changes[:maxERPLogHistoryEntries]
	}
	status.Changes = changes
	return status, nil
}

// History returns the decoded change list for a document, for the screen a
// user opens after being told the document changed.
//
// GET /v1/ic/documents/:doc_no/erp-log-history?trans_flag=12&baseline=123
func (h *ERPLogStatusHandler) History(c *gin.Context) {
	docNo := strings.TrimSpace(c.Param("doc_no"))
	if docNo == "" {
		api.BadRequest(c, "doc_no_required", "doc_no is required", nil)
		return
	}
	transFlag, ok := parseTransFlag(c)
	if !ok {
		return
	}
	baseline, err := strconv.ParseInt(strings.TrimSpace(c.DefaultQuery("baseline", "0")), 10, 64)
	if err != nil || baseline < 0 {
		api.BadRequest(c, "baseline_invalid", "baseline must be a non-negative integer", nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), erpLogHistoryTimeout)
	defer cancel()

	pool, logDB, err := h.logsPool(ctx, c)
	if err != nil {
		writeLogsPoolError(c, logDB, err)
		return
	}

	// One extra row is requested so the response can report whether more
	// exist without running a second COUNT over a 900MB table.
	rows, err := pool.Query(ctx, `
		SELECT roworder, COALESCE(function_code,0), COALESCE(user_code,''),
		       COALESCE(computer_name,''), COALESCE(menu_name,''), date_time,
		       COALESCE(doc_amount,0), COALESCE(old_doc_amount,0),
		       data_old, data_new
		  FROM erp_logs
		 WHERE doc_no = @doc_no AND trans_flag = @trans_flag AND roworder > @baseline`+paperlessLogFilter+`
		 ORDER BY roworder DESC
		 LIMIT @limit`,
		pgx.NamedArgs{
			"doc_no": docNo, "trans_flag": transFlag, "baseline": baseline,
			"limit": maxERPLogHistoryEntries + 1,
		},
	)
	if err != nil {
		api.Internal(c, "erp_log_history_failed", "could not read document history", err.Error())
		return
	}
	defer rows.Close()

	entries := make([]ERPLogHistoryEntry, 0, maxERPLogHistoryEntries)
	truncated := false
	for rows.Next() {
		if len(entries) == maxERPLogHistoryEntries {
			truncated = true
			break
		}
		var r erpLogRow
		if err := rows.Scan(&r.roworder, &r.functionCode, &r.userCode, &r.computerName,
			&r.menuName, &r.dateTime, &r.docAmount, &r.oldDocAmount, &r.dataOld, &r.dataNew); err != nil {
			api.Internal(c, "erp_log_history_scan_failed", "could not read document history", err.Error())
			return
		}
		entries = append(entries, buildHistoryEntry(r))
	}
	if err := rows.Err(); err != nil {
		api.Internal(c, "erp_log_history_rows_failed", "could not read document history", err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      entries,
		"truncated": truncated,
		"limit":     maxERPLogHistoryEntries,
	})
}

func buildHistoryEntry(r erpLogRow) ERPLogHistoryEntry {
	entry := ERPLogHistoryEntry{
		Roworder:     r.roworder,
		FunctionCode: r.functionCode,
		Action:       erpLogActionName(r.functionCode),
		UserCode:     r.userCode,
		ComputerName: r.computerName,
		MenuName:     r.menuName,
		DateTime:     formatLogTime(r.dateTime),
		DocAmount:    r.docAmount,
		OldDocAmount: r.oldDocAmount,
	}
	if r.functionCode != erpLogFunctionEdit {
		return entry
	}
	changes, err := diffERPLogPayloads(r.dataOld, r.dataNew)
	if err != nil {
		// Surfaced rather than swallowed: "could not read the changes" and
		// "there were no changes" must never look the same to a user
		// deciding whether to cancel a document.
		entry.DiffSkipped = "ไม่สามารถอ่านรายละเอียดการแก้ไขได้"
		return entry
	}
	entry.Changes = changes
	return entry
}

func erpLogActionName(functionCode int) string {
	switch functionCode {
	case erpLogFunctionCreate:
		return "created"
	case erpLogFunctionEdit:
		return "edited"
	case erpLogFunctionDelete:
		return "deleted"
	default:
		return "unknown"
	}
}
