package handlers

import (
	"strings"
	"testing"
)

// Every fixture here mirrors a shape confirmed in Damrong (drh_logs)
// production data, so a regression means real documents start behaving
// differently, not just that a synthetic case broke.

func TestNormalizeERPValueCollapsesFormattingNoise(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"trailing zero string", "0.00000000000000", "0"},
		{"plain zero string", "0", "0"},
		{"real zero number", float64(0), "0"},
		{"quantity with 14 decimals", "12.00000000000000", "12"},
		{"price with decimals", "475.62000000000000", "475.62"},
		{"literal null string", "null", ""},
		{"uppercase null string", "NULL", ""},
		{"json null", nil, ""},
		{"empty string", "   ", ""},
		{"text value", " หมายเหตุ ", "หมายเหตุ"},
		{"item code is not a number", "35-102195", "35-102195"},
		{"date stays text", "2026-09-15", "2026-09-15"},
		{"rounds to 2 decimals", "635.7349", "635.73"},
		{"large amount avoids scientific notation", float64(39000.22), "39000.22"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeERPValue(tc.in); got != tc.want {
				t.Fatalf("normalizeERPValue(%#v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A no-op re-save must produce an empty diff. Over half of production edit
// rows are exactly this, and blocking documents on them is what made the
// previous hash-based check unusable.
func TestDiffERPLogPayloadsIgnoresFormattingOnlyRewrite(t *testing.T) {
	oldRaw := []byte(`{
		"screentop":{"doc_no":"2PUV2609-00085","doc_date":"2026-09-15"},
		"screenmore":{"total_amount":39000.22},
		"screendetail":[
			{"line_number":"0","item_code":"35-102195","item_name":"สินค้า A","qty":"30.00000000000000","price":"1300.00000000000000","discount_amount":"0.00000000000000"}
		]
	}`)
	newRaw := []byte(`{
		"screentop":{"doc_no":"2PUV2609-00085","doc_date":"2026-09-15"},
		"screenmore":{"total_amount":39000.22},
		"screendetail":[
			{"line_number":"0","item_code":"35-102195","item_name":"สินค้า A","qty":"30.00","price":"1300.00","discount_amount":"0"}
		]
	}`)

	changes, err := diffERPLogPayloads(oldRaw, newRaw)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("formatting-only rewrite must produce no changes, got %d: %+v", len(changes), changes)
	}
}

// The real edit from 2POV2609-00014: a user swapped the product on one line.
func TestDiffERPLogPayloadsReportsRealEdit(t *testing.T) {
	oldRaw := []byte(`{"screendetail":[
		{"line_number":"10","item_code":"27-201042","item_name":"สีน้ำอะคริลิก","qty":"16.00000000000000","price":"475.62000000000000"}
	]}`)
	newRaw := []byte(`{"screendetail":[
		{"line_number":"10","item_code":"27-201042","item_name":"ยูรีเทน Bull","qty":"12.00000000000000","price":"126.83000000000000"}
	]}`)

	changes, err := diffERPLogPayloads(oldRaw, newRaw)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}

	got := map[string][2]string{}
	for _, c := range changes {
		got[c.Field] = [2]string{c.Old, c.New}
	}
	for field, want := range map[string][2]string{
		"item_name": {"สีน้ำอะคริลิก", "ยูรีเทน Bull"},
		"qty":       {"16", "12"},
		"price":     {"475.62", "126.83"},
	} {
		if got[field] != want {
			t.Errorf("%s = %v, want %v", field, got[field], want)
		}
	}
	for _, c := range changes {
		if c.RowLabel == "" {
			t.Errorf("detail change %s must carry a row label so the user can tell which line changed", c.Field)
		}
	}
}

// Inserting a line must not report every row below it as edited. Production
// payloads showed single insertions surfacing as "78 fields changed" when
// rows were matched by array position.
func TestDiffERPLogPayloadsMatchesRowsByIdentityNotPosition(t *testing.T) {
	oldRaw := []byte(`{"screendetail":[
		{"line_number":"0","item_code":"AAA","item_name":"สินค้า A","qty":"1","price":"10"},
		{"line_number":"1","item_code":"BBB","item_name":"สินค้า B","qty":"2","price":"20"}
	]}`)
	// A new first line shifts both existing rows down one position.
	newRaw := []byte(`{"screendetail":[
		{"line_number":"2","item_code":"CCC","item_name":"สินค้า C","qty":"5","price":"50"},
		{"line_number":"0","item_code":"AAA","item_name":"สินค้า A","qty":"1","price":"10"},
		{"line_number":"1","item_code":"BBB","item_name":"สินค้า B","qty":"2","price":"20"}
	]}`)

	changes, err := diffERPLogPayloads(oldRaw, newRaw)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("inserting one row must report exactly one change, got %d: %+v", len(changes), changes)
	}
	if changes[0].Field != "row_added" || changes[0].RowKey != "CCC" {
		t.Fatalf("change = %+v, want row_added for CCC", changes[0])
	}
	if !strings.Contains(changes[0].New, "สินค้า C") {
		t.Fatalf("added-row summary %q should name the item", changes[0].New)
	}
}

func TestDiffERPLogPayloadsReportsRemovedRow(t *testing.T) {
	oldRaw := []byte(`{"screendetail":[
		{"line_number":"0","item_code":"AAA","item_name":"สินค้า A","qty":"1","price":"10"},
		{"line_number":"1","item_code":"BBB","item_name":"สินค้า B","qty":"2","price":"20"}
	]}`)
	newRaw := []byte(`{"screendetail":[
		{"line_number":"0","item_code":"AAA","item_name":"สินค้า A","qty":"1","price":"10"}
	]}`)

	changes, err := diffERPLogPayloads(oldRaw, newRaw)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}
	if len(changes) != 1 || changes[0].Field != "row_removed" || changes[0].RowKey != "BBB" {
		t.Fatalf("changes = %+v, want a single row_removed for BBB", changes)
	}
}

// The same product on two lines must stay addressable; collapsing them by
// item_code alone would report one as removed on every save.
func TestDiffERPLogPayloadsHandlesDuplicateItemCodes(t *testing.T) {
	raw := []byte(`{"screendetail":[
		{"line_number":"0","item_code":"AAA","item_name":"สินค้า A","qty":"1","price":"10"},
		{"line_number":"1","item_code":"AAA","item_name":"สินค้า A","qty":"3","price":"10"}
	]}`)

	changes, err := diffERPLogPayloads(raw, raw)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("identical payload must produce no changes, got %+v", changes)
	}
}

func TestDiffERPLogPayloadsIgnoresInternalMarkers(t *testing.T) {
	oldRaw := []byte(`{"screendetail":[
		{"line_number":"0","item_code":"AAA","ref_guid":"abc-123","is_lock_cost":"0","is_get_price":"1"}
	]}`)
	newRaw := []byte(`{"screendetail":[
		{"line_number":"0","item_code":"AAA","ref_guid":"zzz-999","is_lock_cost":"1","is_get_price":"0"}
	]}`)

	changes, err := diffERPLogPayloads(oldRaw, newRaw)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("SML-internal markers must not count as user edits, got %+v", changes)
	}
}

func TestDiffERPLogPayloadsReportsHeaderEdit(t *testing.T) {
	oldRaw := []byte(`{"screenbottom":{"remark":"ของเดิม"}}`)
	newRaw := []byte(`{"screenbottom":{"remark":"แก้ไขแล้ว"}}`)

	changes, err := diffERPLogPayloads(oldRaw, newRaw)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want exactly one", changes)
	}
	if changes[0].Label != "หมายเหตุ" {
		t.Fatalf("label = %q, want the Thai label a user recognizes", changes[0].Label)
	}
}

// An unmapped field must still surface, just untranslated — otherwise a
// column SML adds later would silently stop being reported.
func TestErpLabelForFallsBackToFieldName(t *testing.T) {
	if got := erpLabelFor("some_future_column"); got != "some_future_column" {
		t.Fatalf("erpLabelFor = %q, want the raw field name as fallback", got)
	}
}

// An oversized payload must error rather than be silently treated as "no
// changes", which would pass an edited document through as unchanged.
func TestDecodeERPLogPayloadRejectsOversizePayload(t *testing.T) {
	oversize := make([]byte, maxERPLogPayloadBytes+1)
	for i := range oversize {
		oversize[i] = ' '
	}
	if _, err := decodeERPLogPayload(oversize); err == nil {
		t.Fatal("oversize payload must return an error so the caller can fail safe")
	}
}

func TestDecodeERPLogPayloadAllowsEmptyBlob(t *testing.T) {
	// function_code=1 (create) rows carry no data_old.
	if _, err := decodeERPLogPayload(nil); err != nil {
		t.Fatalf("empty payload must decode cleanly, got %v", err)
	}
}

func TestDiffERPLogPayloadsRejectsMalformedJSON(t *testing.T) {
	if _, err := diffERPLogPayloads([]byte(`{"screentop":`), []byte(`{}`)); err == nil {
		t.Fatal("malformed payload must return an error, not an empty diff")
	}
}
