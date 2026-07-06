package handlers

import (
	"strings"
	"testing"
)

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

func TestSortRelatedNodesByFlowUsesSMLEdgesBeforeRankOrDate(t *testing.T) {
	nodes := []RelatedDocumentNode{
		{DocNo: "PV26060001", DocFormatCode: "PV", DocDate: "2026-06-04"},
		{DocNo: "PB26060001", DocFormatCode: "PB", DocDate: "2026-06-03"},
		{DocNo: "PO26060002", DocFormatCode: "PO", DocDate: "2026-06-01"},
		{DocNo: "PA26060001", DocFormatCode: "PA", DocDate: "2026-06-02"},
		{DocNo: "PO26060001", DocFormatCode: "PO", DocDate: "2026-06-01"},
	}
	edges := []RelatedDocumentEdge{
		{FromDocNo: "PO26060001", ToDocNo: "PA26060001", SourceTable: "ic_trans_detail", SourceColumn: "ref_doc_no"},
		{FromDocNo: "PA26060001", ToDocNo: "PB26060001", SourceTable: "ap_ar_trans_detail", SourceColumn: "billing_no"},
		{FromDocNo: "PO26060002", ToDocNo: "PB26060001", SourceTable: "ap_ar_trans_detail", SourceColumn: "billing_no"},
		{FromDocNo: "PB26060001", ToDocNo: "PV26060001", SourceTable: "ap_ar_trans_detail", SourceColumn: "doc_ref"},
	}

	sortRelatedNodesByFlow(nodes, edges)

	got := make([]string, 0, len(nodes))
	for _, node := range nodes {
		got = append(got, node.DocNo)
	}
	want := []string{"PO26060001", "PO26060002", "PA26060001", "PB26060001", "PV26060001"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("flow order = %v, want %v", got, want)
	}
}

func TestNormalizeDirectReferenceCandidates(t *testing.T) {
	items, truncated := normalizeDirectReferenceCandidates([]directReferenceCandidate{
		{DocNo: "PB26060001", SourceTable: "ap_ar_trans_detail", SourceColumn: "billing_no"},
		{DocNo: "pb26060001", SourceTable: "ap_ar_trans_detail", SourceColumn: "doc_ref"},
		{DocNo: "PO26060001", SourceTable: "ic_trans_detail", SourceColumn: "ref_doc_no"},
		{DocNo: "  ", SourceTable: "ic_trans_detail", SourceColumn: "ref_doc_no"},
	}, 10)
	if truncated {
		t.Fatalf("normalizeDirectReferenceCandidates truncated = true, want false")
	}
	if len(items) != 2 {
		t.Fatalf("normalizeDirectReferenceCandidates len = %d, want 2", len(items))
	}
	byDocNo := map[string]directReferenceCandidate{}
	for _, item := range items {
		byDocNo[strings.ToUpper(item.DocNo)] = item
	}
	ref := byDocNo["PB26060001"]
	if ref.SourceColumn != "doc_ref" {
		t.Fatalf("PB26060001 SourceColumn = %q, want doc_ref", ref.SourceColumn)
	}
}

func TestNormalizeDirectReferenceCandidatesCap(t *testing.T) {
	items, truncated := normalizeDirectReferenceCandidates([]directReferenceCandidate{
		{DocNo: "DOC3", SourceTable: "ic_trans_detail", SourceColumn: "ref_doc_no"},
		{DocNo: "DOC1", SourceTable: "ic_trans_detail", SourceColumn: "ref_doc_no"},
		{DocNo: "DOC2", SourceTable: "ic_trans_detail", SourceColumn: "ref_doc_no"},
	}, 2)
	if !truncated {
		t.Fatalf("normalizeDirectReferenceCandidates truncated = false, want true")
	}
	if len(items) != 2 {
		t.Fatalf("normalizeDirectReferenceCandidates len = %d, want 2", len(items))
	}
	if items[0].DocNo != "DOC1" || items[1].DocNo != "DOC2" {
		t.Fatalf("normalizeDirectReferenceCandidates order = %v, want DOC1,DOC2", items)
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
