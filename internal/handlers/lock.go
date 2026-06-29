package handlers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/middleware"
)

// LockHandler sets is_lock_record=1 on a transaction document so PaperLess can
// freeze a document in SML once it has been fully signed. Confirm and Lock are
// the same operation in SML (see docs/sml-questions.md Q1/Q2): a single integer
// flag, no approve/user fields.
type LockHandler struct {
	dbm *db.Manager
}

func NewLockHandler(dbm *db.Manager) *LockHandler {
	return &LockHandler{dbm: dbm}
}

// lockableTables lists the two tables that carry is_lock_record, in the order we
// probe them to discover where a doc_no lives. ic_trans holds purchase/sale docs
// (PO, PA, SO, SI…); ap_ar_trans holds AP/AR docs (PB bill-receive, PV payment,
// AR receipt). A doc_no is unique within SML, so the first table that has it wins.
var lockableTables = []string{"ic_trans", "ap_ar_trans"}

type lockResult struct {
	DocNo         string `json:"doc_no"`
	Table         string `json:"table"`
	TransFlag     int    `json:"trans_flag"`
	IsLockRecord  int    `json:"is_lock_record"`
	AlreadyLocked bool   `json:"already_locked"`
}

// Lock godoc: POST /api/v1/documents/:doc_no/lock
//
// Idempotent: locking an already-locked document is a no-op success (returns
// already_locked=true) — PaperLess retries safely after a timeout, and a timeout
// is never treated as success on the PaperLess side. Locks ONLY the doc_no given
// (per SML: "lock แค่ใบที่เซ็น" — never the whole chain).
func (h *LockHandler) Lock(c *gin.Context) {
	docNo := strings.TrimSpace(c.Param("doc_no"))
	if docNo == "" {
		api.BadRequest(c, "validation_failed", "doc_no is required", nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "could not get tenant database", err.Error())
		return
	}

	// Find which table holds this doc_no (active row only). last_status=0 is SML's
	// "current, non-cancelled" marker — the same filter the read/write handlers use.
	table, transFlag, currentLock, found, err := locateDocForLock(ctx, pool, docNo)
	if err != nil {
		api.Internal(c, "lock_lookup_failed", "could not look up document", err.Error())
		return
	}
	if !found {
		api.NotFound(c, "document_not_found", "no active document found for doc_no: "+docNo)
		return
	}

	// Idempotent: already locked → success without writing.
	if currentLock == 1 {
		api.OK(c, lockResult{
			DocNo: docNo, Table: table, TransFlag: transFlag,
			IsLockRecord: 1, AlreadyLocked: true,
		})
		return
	}

	// is_lock_record is nullable in SML, so NULL or 0 both become 1 here.
	tag, err := pool.Exec(ctx,
		"UPDATE "+table+" SET is_lock_record=1 WHERE doc_no=$1 AND trans_flag=$2 AND last_status=0",
		docNo, transFlag)
	if err != nil {
		api.Internal(c, "lock_update_failed", "could not lock document", err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		// The row vanished between lookup and update (cancelled concurrently).
		api.Conflict(c, "lock_no_rows", "document was not lockable (it may have been cancelled)", gin.H{"doc_no": docNo})
		return
	}

	api.OK(c, lockResult{
		DocNo: docNo, Table: table, TransFlag: transFlag,
		IsLockRecord: 1, AlreadyLocked: false,
	})
}

// locateDocForLock probes the lockable tables for an active row with this doc_no
// and returns the table, its trans_flag, and the current is_lock_record (0 when
// NULL). found=false means the doc_no exists in neither table as an active row.
func locateDocForLock(ctx context.Context, pool lockQuerier, docNo string) (table string, transFlag, currentLock int, found bool, err error) {
	for _, t := range lockableTables {
		var tf int
		var lock *int
		qErr := pool.QueryRow(ctx,
			"SELECT trans_flag, is_lock_record FROM "+t+" WHERE doc_no=$1 AND last_status=0 LIMIT 1",
			docNo,
		).Scan(&tf, &lock)
		if qErr == nil {
			cl := 0
			if lock != nil {
				cl = *lock
			}
			return t, tf, cl, true, nil
		}
		if !errors.Is(qErr, pgx.ErrNoRows) {
			return "", 0, 0, false, qErr
		}
	}
	return "", 0, 0, false, nil
}

// lockQuerier is the subset of pgxpool.Pool used here, so the locate logic can be
// unit-tested with a fake.
type lockQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
