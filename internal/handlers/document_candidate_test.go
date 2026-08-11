package handlers

import (
	"strings"
	"testing"
)

func TestResolveCandidateSourceUsesTransFlagCatalog(t *testing.T) {
	table, partyType := resolveCandidateSource("RC", 239, 0, "ic_trans")
	if table != "ap_ar_trans" || partyType != "AR" {
		t.Fatalf("RC source = %s/%s, want ap_ar_trans/AR", table, partyType)
	}

	table, partyType = resolveCandidateSource("PV", 19, 0, "ic_trans")
	if table != "ap_ar_trans" || partyType != "AP" {
		t.Fatalf("PV source = %s/%s, want ap_ar_trans/AP", table, partyType)
	}
}

func TestResolveCandidateSourceFallsBackToTransType(t *testing.T) {
	table, partyType := resolveCandidateSource("ZZ", 999999, 2, "ap_ar_trans")
	if table != "ap_ar_trans" || partyType != "AR" {
		t.Fatalf("fallback source = %s/%s, want ap_ar_trans/AR", table, partyType)
	}

	_, partyType = resolveCandidateSource("XP", 999999, 1, "ic_trans")
	if partyType != "AP" {
		t.Fatalf("trans_type=1 party = %s, want AP", partyType)
	}
}

func TestCandidatePartyNameUsesResolvedPartyType(t *testing.T) {
	if got := candidatePartyName("AR", "ลูกค้า", "เจ้าหนี้"); got != "ลูกค้า" {
		t.Fatalf("AR party name = %q", got)
	}
	if got := candidatePartyName("AP", "ลูกค้า", "เจ้าหนี้"); got != "เจ้าหนี้" {
		t.Fatalf("AP party name = %q", got)
	}
	if got := candidatePartyName("AP", "ลูกค้า", ""); got != "ลูกค้า" {
		t.Fatalf("AP fallback party name = %q", got)
	}
}

func TestCandidateSearchEscapesSQLLikeWildcards(t *testing.T) {
	search := "%" + escapeSQLLike(`RC_%\NES`) + "%"
	if search != `%RC\_\%\\NES%` {
		t.Fatalf("search = %q", search)
	}
}

func TestCandidateListQuerySearchesBothTablesAndParties(t *testing.T) {
	query := candidateListQuery(`WHERE upper(COALESCE(doc_format_code,'')) = @doc_format_code AND (
    doc_no ILIKE @search ESCAPE '\'
    OR party_code ILIKE @search ESCAPE '\'
    OR ar_name ILIKE @search ESCAPE '\'
    OR ap_name ILIKE @search ESCAPE '\'
)`)

	for _, want := range []string{"FROM ic_trans", "FROM ap_ar_trans", "FROM ap_ar_trans_detail", "LEFT JOIN ar_customer", "LEFT JOIN ap_supplier", "ar_name ILIKE", "ap_name ILIKE", "UNION ALL"} {
		if !strings.Contains(query, want) {
			t.Fatalf("candidate query missing %q:\n%s", want, query)
		}
	}
}

func TestTruncateCandidateSearchCapsRunes(t *testing.T) {
	input := strings.Repeat("ก", 130)
	got := truncateCandidateSearch(input)
	if len([]rune(got)) != 120 {
		t.Fatalf("rune length = %d, want 120", len([]rune(got)))
	}
}

func TestNormalizeBatchDocumentNumbers(t *testing.T) {
	got, err := normalizeBatchDocumentNumbers([]string{" qt26070001 ", "QT26070002", "qt26070001"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "QT26070001" || got[1] != "QT26070002" {
		t.Fatalf("normalized document numbers = %#v", got)
	}
}

func TestNormalizeBatchDocumentNumbersRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name   string
		values []string
	}{
		{name: "empty", values: nil},
		{name: "blank", values: []string{" "}},
		{name: "too long", values: []string{strings.Repeat("A", 26)}},
		{name: "too many", values: append(make([]string, 30), "QT99999999")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeBatchDocumentNumbers(tt.values); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestCandidateBatchQueryFiltersEachSourceBeforeUnion(t *testing.T) {
	query := candidateBatchQuery()
	for _, want := range []string{
		"FROM ic_trans t",
		"FROM ap_ar_trans t",
		"t.doc_format_code = @doc_format_code",
		"t.doc_no = ANY(@doc_nos)",
		"UNION ALL",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("batch query missing %q:\n%s", want, query)
		}
	}
	if strings.Count(query, "t.doc_no = ANY(@doc_nos)") != 2 {
		t.Fatalf("batch query must filter both source tables:\n%s", query)
	}
}

func TestCandidateSourceRevisionBatchQueryHashesBothHeadersAndDetails(t *testing.T) {
	query := candidateSourceRevisionBatchQuery()
	for _, want := range []string{
		"FROM ic_trans t",
		"FROM ap_ar_trans t",
		"FROM ic_trans_detail d",
		"FROM ap_ar_trans_detail d",
		"md5(",
		"- 'is_lock_record' - 'last_status'",
		"COALESCE(d.last_status,0)=0",
		"t.doc_no = ANY(@doc_nos)",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("source revision query missing %q:\n%s", want, query)
		}
	}
	if strings.Count(query, "t.doc_no = ANY(@doc_nos)") != 2 {
		t.Fatalf("source revision must stay batch-scoped for both source tables:\n%s", query)
	}
}

func TestCandidateSourceRevisionBatchQueryNormalizesUnscaledNumericColumns(t *testing.T) {
	query := candidateSourceRevisionBatchQuery()

	// Every unscaled numeric column on all four source/detail tables must be
	// rounded before hashing, or trailing-zero formatting drift in that
	// column can still flip the fingerprint with zero real content change.
	for table, cols := range numericColumnsNeedingScale {
		for _, col := range cols {
			want := "'{" + col + "}'"
			if !strings.Contains(query, want) {
				t.Fatalf("source revision query missing normalization for %s.%s (jsonb_set path %q):\n%s", table, col, want, query)
			}
		}
	}

	if !strings.Contains(query, "ROUND(") {
		t.Fatalf("source revision query must round numeric columns:\n%s", query)
	}
	if strings.Count(query, "jsonb_set(") == 0 {
		t.Fatalf("source revision query must normalize via jsonb_set:\n%s", query)
	}
}

func TestNormalizedRowJSONExprRoundsEveryConfiguredColumn(t *testing.T) {
	expr := normalizedRowJSONExpr("t", "ic_trans")
	if got, want := strings.Count(expr, "jsonb_set("), len(numericColumnsNeedingScale["ic_trans"]); got != want {
		t.Fatalf("normalizedRowJSONExpr(ic_trans) has %d jsonb_set calls, want %d", got, want)
	}
	if !strings.Contains(expr, "to_jsonb(t)") {
		t.Fatalf("normalizedRowJSONExpr must build on to_jsonb(alias): %s", expr)
	}
	// Every column must COALESCE to a real JSON null, not a SQL NULL —
	// jsonb_set collapses its whole result to SQL NULL if the replacement
	// value itself is SQL NULL, which would silently drop every other
	// column's contribution to the hash whenever one numeric field is NULL.
	if got, want := strings.Count(expr, "COALESCE(to_jsonb(ROUND("), len(numericColumnsNeedingScale["ic_trans"]); got != want {
		t.Fatalf("normalizedRowJSONExpr(ic_trans) has %d NULL-safe COALESCE wrappers, want %d", got, want)
	}
	if !strings.Contains(expr, "'null'::jsonb") {
		t.Fatalf("normalizedRowJSONExpr must fall back to JSON null, not SQL NULL: %s", expr)
	}
	// Unknown table: no normalization columns configured, so it must fall
	// back to a bare to_jsonb() with zero jsonb_set wrapping.
	bare := normalizedRowJSONExpr("t", "some_other_table")
	if bare != "to_jsonb(t)" {
		t.Fatalf("normalizedRowJSONExpr(unknown table) = %q, want bare to_jsonb(t)", bare)
	}
}

func TestCandidateSourceRevisionBatchQueryExcludesGuidCode(t *testing.T) {
	query := candidateSourceRevisionBatchQuery()

	// guid_code is a transient "document is open for editing in the SML UI"
	// marker (see the doc comment on candidateSourceRevisionBatchQuery) —
	// it must be excluded from both header hashes together with the
	// existing is_lock_record/last_status exclusions, on the same chain,
	// or a future edit could silently drop it from one branch only.
	want := "- 'is_lock_record' - 'last_status' - 'guid_code'"
	if !strings.Contains(query, want) {
		t.Fatalf("source revision query missing full exclusion chain %q:\n%s", want, query)
	}
	// Exactly 2 occurrences total (one per header branch) also proves
	// guid_code does NOT leak into the detail-row jsonb_agg subqueries —
	// neither ic_trans_detail nor ap_ar_trans_detail has this column
	// (confirmed against production information_schema.columns), so the
	// detail hash must keep excluding only 'last_status'.
	if got := strings.Count(query, "'guid_code'"); got != 2 {
		t.Fatalf("source revision query excludes guid_code %d times, want exactly 2 (ic_trans and ap_ar_trans headers only):\n%s", got, query)
	}
}

func TestCandidateRevisionKeyIsTableAndCaseStable(t *testing.T) {
	key := candidateRevisionKey(DocumentCandidate{Table: "IC_TRANS", DocNo: " po26070001 "})
	if key != "ic_trans\x00PO26070001" {
		t.Fatalf("revision key = %q", key)
	}
}

func TestNormalizeBatchDocumentNumbersDeduplicatesBeforeLimitUse(t *testing.T) {
	values := make([]string, 30)
	for i := range values {
		values[i] = "QT26070001"
	}
	got, err := normalizeBatchDocumentNumbers(values)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "QT26070001" {
		t.Fatalf("normalized document numbers = %#v", got)
	}
}
