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
