package handlers

import "testing"

func TestParseRelatedDepth(t *testing.T) {
	tests := []struct {
		value string
		want  int
	}{
		{value: "", want: relatedDefaultDepth},
		{value: "0", want: relatedDefaultDepth},
		{value: "2", want: 2},
		{value: "99", want: relatedMaxDepth},
		{value: "nope", want: relatedDefaultDepth},
	}
	for _, tc := range tests {
		if got := parseRelatedDepth(tc.value); got != tc.want {
			t.Fatalf("parseRelatedDepth(%q) = %d, want %d", tc.value, got, tc.want)
		}
	}
}

func TestRelatedDocumentRank(t *testing.T) {
	tests := []struct {
		node RelatedDocumentNode
		want int
	}{
		{node: RelatedDocumentNode{DocFormatCode: "PO", TransFlag: 999}, want: 10},
		{node: RelatedDocumentNode{DocFormatCode: "PA", TransFlag: 999}, want: 20},
		{node: RelatedDocumentNode{DocFormatCode: "PB", TransFlag: 999}, want: 30},
		{node: RelatedDocumentNode{DocFormatCode: "PV", TransFlag: 999}, want: 40},
		{node: RelatedDocumentNode{DocFormatCode: "", TransFlag: 6}, want: 10},
		{node: RelatedDocumentNode{DocFormatCode: "", TransFlag: 213}, want: 30},
	}
	for _, tc := range tests {
		if got := relatedDocumentRank(tc.node); got != tc.want {
			t.Fatalf("relatedDocumentRank(%+v) = %d, want %d", tc.node, got, tc.want)
		}
	}
}

func TestRelatedEdgeAndWarningDedupe(t *testing.T) {
	edge := RelatedDocumentEdge{
		FromDocNo:    "PO26060001",
		ToDocNo:      "PA26060001",
		SourceTable:  "ic_trans_detail",
		SourceColumn: "ref_doc_no",
	}
	if got, want := relatedEdgeKey(edge), "PO26060001>PA26060001:ic_trans_detail.ref_doc_no"; got != want {
		t.Fatalf("relatedEdgeKey = %q, want %q", got, want)
	}

	warnings := dedupeRelatedWarnings([]RelatedDocumentWarning{
		{Code: "related_header_missing", DocNo: "DOC1"},
		{Code: "related_header_missing", DocNo: "doc1"},
		{Code: "related_header_missing", DocNo: "DOC2"},
	})
	if len(warnings) != 2 {
		t.Fatalf("dedupeRelatedWarnings len = %d, want 2", len(warnings))
	}
}
