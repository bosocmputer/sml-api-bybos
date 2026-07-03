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

func TestAssignRelatedSourceDocNosPrefersImmediateSource(t *testing.T) {
	root := RelatedDocumentNode{DocNo: "PV26060001", DocFormatCode: "PV"}
	nodes := []RelatedDocumentNode{
		{DocNo: "PO26060001", DocFormatCode: "PO"},
		{DocNo: "PA26060001", DocFormatCode: "PA"},
		{DocNo: "PB26060001", DocFormatCode: "PB"},
		{DocNo: "PV26060001", DocFormatCode: "PV"},
	}
	edges := []RelatedDocumentEdge{
		{FromDocNo: "PO26060001", ToDocNo: "PA26060001", SourceTable: "ic_trans_detail", SourceColumn: "ref_doc_no"},
		{FromDocNo: "PA26060001", ToDocNo: "PB26060001", SourceTable: "ap_ar_trans_detail", SourceColumn: "billing_no"},
		{FromDocNo: "PA26060001", ToDocNo: "PV26060001", SourceTable: "ap_ar_trans_detail", SourceColumn: "billing_no"},
		{FromDocNo: "PB26060001", ToDocNo: "PV26060001", SourceTable: "ap_ar_trans_detail", SourceColumn: "doc_ref"},
	}

	root, nodes = assignRelatedSourceDocNos(root, nodes, edges)
	if root.SourceDocNo != "PB26060001" {
		t.Fatalf("root SourceDocNo = %q, want PB26060001", root.SourceDocNo)
	}

	got := map[string]string{}
	for _, node := range nodes {
		got[node.DocNo] = node.SourceDocNo
	}
	want := map[string]string{
		"PO26060001": "PO26060001",
		"PA26060001": "PO26060001",
		"PB26060001": "PA26060001",
		"PV26060001": "PB26060001",
	}
	for docNo, sourceDocNo := range want {
		if got[docNo] != sourceDocNo {
			t.Fatalf("%s SourceDocNo = %q, want %q", docNo, got[docNo], sourceDocNo)
		}
	}
}

func TestLookupTransFlagCatalogForPaperlessFlow(t *testing.T) {
	tests := []struct {
		flag  int
		table string
		want  string
	}{
		{flag: 6, table: "ic_trans", want: "บันทึกใบสั่งซื้อ"},
		{flag: 12, table: "ic_trans", want: "ซื้อสินค้า"},
		{flag: 213, table: "ap_ar_trans", want: "ใบรับวางบิล(เจ้าหนี้)"},
		{flag: 19, table: "ap_ar_trans", want: "จ่ายชำระหนี้(เจ้าหนี้)"},
	}
	for _, tc := range tests {
		got, ok := lookupTransFlagCatalog(tc.flag, tc.table)
		if !ok {
			t.Fatalf("lookupTransFlagCatalog(%d, %q) not found", tc.flag, tc.table)
		}
		if got.NameTH != tc.want {
			t.Fatalf("lookupTransFlagCatalog(%d, %q).NameTH = %q, want %q", tc.flag, tc.table, got.NameTH, tc.want)
		}
	}
}
