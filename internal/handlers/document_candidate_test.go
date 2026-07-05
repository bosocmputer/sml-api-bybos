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
