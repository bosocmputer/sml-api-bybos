package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/middleware"
)

const (
	relatedDefaultDepth = 3
	relatedMaxDepth     = 4
	relatedMaxNodes     = 30
	referenceMaxItems   = 100
)

type RelatedDocumentHandler struct {
	dbm *db.Manager
}

func NewRelatedDocumentHandler(dbm *db.Manager) *RelatedDocumentHandler {
	return &RelatedDocumentHandler{dbm: dbm}
}

type RelatedDocumentGraph struct {
	Root      RelatedDocumentNode      `json:"root"`
	Nodes     []RelatedDocumentNode    `json:"nodes"`
	Edges     []RelatedDocumentEdge    `json:"edges"`
	Warnings  []RelatedDocumentWarning `json:"warnings,omitempty"`
	Depth     int                      `json:"depth"`
	Truncated bool                     `json:"truncated"`
}

type RelatedDocumentNode struct {
	DocNo           string  `json:"doc_no"`
	DocDate         string  `json:"doc_date"`
	DocTime         string  `json:"doc_time,omitempty"`
	DocFormatCode   string  `json:"doc_format_code"`
	DocFormatName   string  `json:"doc_format_name,omitempty"`
	TransFlag       int     `json:"trans_flag"`
	TransFlagMenu   string  `json:"trans_flag_menu,omitempty"`
	TransFlagNameTH string  `json:"trans_flag_name_th,omitempty"`
	TransFlagNameEN string  `json:"trans_flag_name_en,omitempty"`
	TransType       int     `json:"trans_type,omitempty"`
	Table           string  `json:"table"`
	PartyCode       string  `json:"party_code"`
	PartyName       string  `json:"party_name"`
	PartyType       string  `json:"party_type"`
	TotalAmount     float64 `json:"total_amount"`
	SourceDocNo     string  `json:"source_doc_no,omitempty"`
	IsLockRecord    int     `json:"is_lock_record"`
}

type RelatedDocumentEdge struct {
	FromDocNo    string `json:"from_doc_no"`
	ToDocNo      string `json:"to_doc_no"`
	Relation     string `json:"relation"`
	SourceTable  string `json:"source_table"`
	SourceColumn string `json:"source_column"`
}

type RelatedDocumentWarning struct {
	Code    string `json:"code"`
	DocNo   string `json:"doc_no,omitempty"`
	Message string `json:"message"`
}

type DocumentReferences struct {
	Document  RelatedDocumentNode      `json:"document"`
	Items     []DocumentReferenceItem  `json:"items"`
	Warnings  []RelatedDocumentWarning `json:"warnings,omitempty"`
	Total     int                      `json:"total"`
	Truncated bool                     `json:"truncated"`
}

type DocumentReferenceItem struct {
	DocNo           string  `json:"doc_no"`
	DocDate         string  `json:"doc_date,omitempty"`
	DocTime         string  `json:"doc_time,omitempty"`
	DocFormatCode   string  `json:"doc_format_code,omitempty"`
	DocFormatName   string  `json:"doc_format_name,omitempty"`
	TransFlag       int     `json:"trans_flag,omitempty"`
	TransFlagMenu   string  `json:"trans_flag_menu,omitempty"`
	TransFlagNameTH string  `json:"trans_flag_name_th,omitempty"`
	TransFlagNameEN string  `json:"trans_flag_name_en,omitempty"`
	TransType       int     `json:"trans_type,omitempty"`
	Table           string  `json:"table,omitempty"`
	PartyCode       string  `json:"party_code,omitempty"`
	PartyName       string  `json:"party_name,omitempty"`
	PartyType       string  `json:"party_type,omitempty"`
	TotalAmount     float64 `json:"total_amount,omitempty"`
	IsLockRecord    int     `json:"is_lock_record,omitempty"`
	SourceTable     string  `json:"source_table"`
	SourceColumn    string  `json:"source_column"`
}

type relatedQueueItem struct {
	docNo string
	depth int
}

// Related returns a bounded graph of SML documents connected to doc_no. It uses
// only DB relationships verified for PaperLess: ic_trans_detail.ref_doc_no,
// ap_ar_trans_detail.billing_no, and ap_ar_trans_detail.doc_ref.
func (h *RelatedDocumentHandler) Related(c *gin.Context) {
	docNo := strings.TrimSpace(c.Param("doc_no"))
	if docNo == "" {
		api.BadRequest(c, "doc_no_required", "doc_no is required", nil)
		return
	}
	docFormatCode := strings.ToUpper(strings.TrimSpace(c.Query("doc_format_code")))
	depth := parseRelatedDepth(c.Query("depth"))

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "could not get tenant database", err.Error())
		return
	}

	root, err := findRelatedDocumentHeader(ctx, pool, docNo, docFormatCode)
	if errors.Is(err, pgx.ErrNoRows) {
		api.NotFound(c, "document_not_found", "no active document found for doc_no: "+docNo)
		return
	}
	if err != nil {
		api.Internal(c, "related_document_lookup_failed", "could not load document", err.Error())
		return
	}

	graph, err := buildRelatedDocumentGraph(ctx, pool, root, depth)
	if err != nil {
		api.Internal(c, "related_documents_failed", "could not load related documents", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": graph})
}

// References returns the direct predecessor documents referenced by the current
// document detail rows. It intentionally does not recurse; PaperLess uses this
// as a concise "before signing" checklist.
func (h *RelatedDocumentHandler) References(c *gin.Context) {
	docNo := strings.TrimSpace(c.Param("doc_no"))
	if docNo == "" {
		api.BadRequest(c, "doc_no_required", "doc_no is required", nil)
		return
	}
	docFormatCode := strings.ToUpper(strings.TrimSpace(c.Query("doc_format_code")))

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		api.Internal(c, "db_pool_error", "could not get tenant database", err.Error())
		return
	}

	document, err := findRelatedDocumentHeader(ctx, pool, docNo, docFormatCode)
	if errors.Is(err, pgx.ErrNoRows) {
		api.NotFound(c, "document_not_found", "no active document found for doc_no: "+docNo)
		return
	}
	if err != nil {
		api.Internal(c, "reference_document_lookup_failed", "could not load document", err.Error())
		return
	}

	result, err := buildDirectDocumentReferences(ctx, pool, document)
	if err != nil {
		api.Internal(c, "document_references_failed", "could not load document references", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

func parseRelatedDepth(value string) int {
	depth, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || depth < 1 {
		depth = relatedDefaultDepth
	}
	if depth > relatedMaxDepth {
		depth = relatedMaxDepth
	}
	return depth
}

func buildRelatedDocumentGraph(ctx context.Context, q relatedDocumentQuerier, root RelatedDocumentNode, depth int) (RelatedDocumentGraph, error) {
	nodes := map[string]RelatedDocumentNode{strings.ToUpper(root.DocNo): root}
	edges := map[string]RelatedDocumentEdge{}
	warnings := []RelatedDocumentWarning{}
	processed := map[string]int{}
	queue := []relatedQueueItem{{docNo: root.DocNo, depth: 0}}
	truncated := false

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		key := strings.ToUpper(item.docNo)
		if seenDepth, ok := processed[key]; ok && seenDepth <= item.depth {
			continue
		}
		processed[key] = item.depth
		if item.depth >= depth {
			continue
		}

		foundEdges, err := findRelatedDocumentEdges(ctx, q, item.docNo)
		if err != nil {
			return RelatedDocumentGraph{}, err
		}
		for _, edge := range foundEdges {
			if edge.FromDocNo == "" || edge.ToDocNo == "" || strings.EqualFold(edge.FromDocNo, edge.ToDocNo) {
				continue
			}
			edgeKey := relatedEdgeKey(edge)
			edges[edgeKey] = edge

			for _, linkedDocNo := range []string{edge.FromDocNo, edge.ToDocNo} {
				linkedKey := strings.ToUpper(linkedDocNo)
				if _, ok := nodes[linkedKey]; ok {
					continue
				}
				if len(nodes) >= relatedMaxNodes {
					truncated = true
					continue
				}
				node, err := findRelatedDocumentHeader(ctx, q, linkedDocNo, "")
				if errors.Is(err, pgx.ErrNoRows) {
					warnings = append(warnings, RelatedDocumentWarning{
						Code:    "related_header_missing",
						DocNo:   linkedDocNo,
						Message: fmt.Sprintf("reference %s was found but header document was not found", linkedDocNo),
					})
					continue
				}
				if err != nil {
					return RelatedDocumentGraph{}, err
				}
				nodes[linkedKey] = node
				queue = append(queue, relatedQueueItem{docNo: linkedDocNo, depth: item.depth + 1})
			}
		}
	}

	nodeList := make([]RelatedDocumentNode, 0, len(nodes))
	for _, node := range nodes {
		nodeList = append(nodeList, node)
	}
	sortRelatedNodes(nodeList)

	edgeList := make([]RelatedDocumentEdge, 0, len(edges))
	for _, edge := range edges {
		edgeList = append(edgeList, edge)
	}
	sort.Slice(edgeList, func(i, j int) bool {
		if edgeList[i].FromDocNo == edgeList[j].FromDocNo {
			return edgeList[i].ToDocNo < edgeList[j].ToDocNo
		}
		return edgeList[i].FromDocNo < edgeList[j].FromDocNo
	})
	root, nodeList = assignRelatedSourceDocNos(root, nodeList, edgeList)

	return RelatedDocumentGraph{
		Root:      root,
		Nodes:     nodeList,
		Edges:     edgeList,
		Warnings:  dedupeRelatedWarnings(warnings),
		Depth:     depth,
		Truncated: truncated,
	}, nil
}

func buildDirectDocumentReferences(ctx context.Context, q relatedDocumentQuerier, document RelatedDocumentNode) (DocumentReferences, error) {
	candidates, err := findDirectReferenceCandidates(ctx, q, document.DocNo)
	if err != nil {
		return DocumentReferences{}, err
	}
	candidates, truncated := normalizeDirectReferenceCandidates(candidates, referenceMaxItems)

	items := make([]DocumentReferenceItem, 0, len(candidates))
	warnings := []RelatedDocumentWarning{}
	if truncated {
		warnings = append(warnings, RelatedDocumentWarning{
			Code:    "reference_limit_reached",
			DocNo:   document.DocNo,
			Message: fmt.Sprintf("direct references were capped at %d items", referenceMaxItems),
		})
	}
	for _, candidate := range candidates {
		node, err := findRelatedDocumentHeader(ctx, q, candidate.DocNo, "")
		if errors.Is(err, pgx.ErrNoRows) {
			items = append(items, DocumentReferenceItem{
				DocNo:        candidate.DocNo,
				SourceTable:  candidate.SourceTable,
				SourceColumn: candidate.SourceColumn,
			})
			warnings = append(warnings, RelatedDocumentWarning{
				Code:    "reference_header_missing",
				DocNo:   candidate.DocNo,
				Message: fmt.Sprintf("reference %s was found but header document was not found", candidate.DocNo),
			})
			continue
		}
		if err != nil {
			return DocumentReferences{}, err
		}
		items = append(items, documentReferenceItemFromNode(node, candidate))
	}
	sort.SliceStable(items, func(i, j int) bool {
		ri := relatedDocumentRank(RelatedDocumentNode{DocFormatCode: items[i].DocFormatCode, TransFlag: items[i].TransFlag})
		rj := relatedDocumentRank(RelatedDocumentNode{DocFormatCode: items[j].DocFormatCode, TransFlag: items[j].TransFlag})
		if ri != rj {
			return ri < rj
		}
		if items[i].DocDate != items[j].DocDate {
			return items[i].DocDate < items[j].DocDate
		}
		return items[i].DocNo < items[j].DocNo
	})
	return DocumentReferences{
		Document:  document,
		Items:     items,
		Warnings:  dedupeRelatedWarnings(warnings),
		Total:     len(items),
		Truncated: truncated,
	}, nil
}

func assignRelatedSourceDocNos(root RelatedDocumentNode, nodes []RelatedDocumentNode, edges []RelatedDocumentEdge) (RelatedDocumentNode, []RelatedDocumentNode) {
	byDocNo := map[string]RelatedDocumentNode{}
	for _, node := range nodes {
		byDocNo[strings.ToUpper(strings.TrimSpace(node.DocNo))] = node
	}

	assign := func(node RelatedDocumentNode) RelatedDocumentNode {
		node.SourceDocNo = bestRelatedSourceDocNo(node, edges, byDocNo)
		if node.SourceDocNo == "" {
			node.SourceDocNo = node.DocNo
		}
		return node
	}

	root = assign(root)
	for i := range nodes {
		nodes[i] = assign(nodes[i])
	}
	return root, nodes
}

func bestRelatedSourceDocNo(node RelatedDocumentNode, edges []RelatedDocumentEdge, byDocNo map[string]RelatedDocumentNode) string {
	nodeNo := strings.ToUpper(strings.TrimSpace(node.DocNo))
	nodeRank := relatedDocumentRank(node)
	bestDocNo := ""
	bestScore := 1 << 30
	for _, edge := range edges {
		if !strings.EqualFold(strings.TrimSpace(edge.ToDocNo), nodeNo) {
			continue
		}
		fromDocNo := strings.TrimSpace(edge.FromDocNo)
		if fromDocNo == "" || strings.EqualFold(fromDocNo, node.DocNo) {
			continue
		}
		fromNode, ok := byDocNo[strings.ToUpper(fromDocNo)]
		fromRank := 1000
		if ok {
			fromRank = relatedDocumentRank(fromNode)
		}
		rankDistance := nodeRank - fromRank
		if rankDistance < 0 {
			rankDistance = 100 + (-rankDistance)
		}
		score := relatedSourceColumnPriority(edge) + rankDistance*10
		if score < bestScore || (score == bestScore && strings.Compare(fromDocNo, bestDocNo) < 0) {
			bestScore = score
			bestDocNo = fromDocNo
		}
	}
	return bestDocNo
}

func relatedSourceColumnPriority(edge RelatedDocumentEdge) int {
	source := strings.ToLower(strings.TrimSpace(edge.SourceTable + "." + edge.SourceColumn))
	switch source {
	case "ap_ar_trans_detail.doc_ref":
		return 0
	case "ap_ar_trans_detail.billing_no":
		return 1
	case "ic_trans_detail.ref_doc_no":
		return 2
	default:
		return 5
	}
}

func relatedEdgeKey(edge RelatedDocumentEdge) string {
	return strings.ToUpper(edge.FromDocNo) + ">" + strings.ToUpper(edge.ToDocNo) + ":" + edge.SourceTable + "." + edge.SourceColumn
}

func dedupeRelatedWarnings(items []RelatedDocumentWarning) []RelatedDocumentWarning {
	seen := map[string]bool{}
	out := []RelatedDocumentWarning{}
	for _, item := range items {
		key := item.Code + ":" + strings.ToUpper(item.DocNo)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

func sortRelatedNodes(nodes []RelatedDocumentNode) {
	sort.Slice(nodes, func(i, j int) bool {
		ri := relatedDocumentRank(nodes[i])
		rj := relatedDocumentRank(nodes[j])
		if ri != rj {
			return ri < rj
		}
		if nodes[i].DocDate != nodes[j].DocDate {
			return nodes[i].DocDate < nodes[j].DocDate
		}
		return nodes[i].DocNo < nodes[j].DocNo
	})
}

func relatedDocumentRank(node RelatedDocumentNode) int {
	switch strings.ToUpper(strings.TrimSpace(node.DocFormatCode)) {
	case "PO", "POP":
		return 10
	case "PA", "PUP":
		return 20
	case "PB", "PBV":
		return 30
	case "PV", "PVV":
		return 40
	}
	switch node.TransFlag {
	case 6:
		return 10
	case 12:
		return 20
	case 213:
		return 30
	case 19:
		return 40
	default:
		return 100
	}
}

func findRelatedDocumentHeader(ctx context.Context, q relatedDocumentQuerier, docNo, docFormatCode string) (RelatedDocumentNode, error) {
	docNo = strings.TrimSpace(docNo)
	docFormatCode = strings.ToUpper(strings.TrimSpace(docFormatCode))
	args := []any{docNo}
	filter := ""
	if docFormatCode != "" {
		filter = " AND upper(COALESCE(t.doc_format_code,'')) = $2"
		args = append(args, docFormatCode)
	}
	query := `
WITH candidates AS (
    SELECT t.doc_no, t.doc_date, COALESCE(t.doc_time,'') AS doc_time,
           COALESCE(t.doc_format_code,'') AS doc_format_code,
           COALESCE((
               SELECT NULLIF(f.name_1,'')
                 FROM erp_doc_format f
                WHERE lower(f.code) = lower(COALESCE(t.doc_format_code,''))
                ORDER BY f.screen_code, f.code
                LIMIT 1
           ), '') AS doc_format_name,
           t.trans_flag,
           'ic_trans' AS table_name,
           COALESCE(t.cust_code,'') AS party_code,
           CASE WHEN upper(COALESCE(t.doc_format_code,'')) LIKE 'P%' THEN 'AP' ELSE 'AR' END AS party_type,
           COALESCE(ap.name_1, ar.name_1, '') AS party_name,
           COALESCE(t.total_amount,0)::double precision AS total_amount,
           COALESCE(t.is_lock_record,0) AS is_lock_record
      FROM ic_trans t
      LEFT JOIN ap_supplier ap ON ap.code = t.cust_code
      LEFT JOIN ar_customer ar ON ar.code = t.cust_code
     WHERE t.doc_no = $1 AND COALESCE(t.last_status,0)=0` + filter + `
    UNION ALL
    SELECT t.doc_no, t.doc_date, COALESCE(t.doc_time,'') AS doc_time,
           COALESCE(t.doc_format_code,'') AS doc_format_code,
           COALESCE((
               SELECT NULLIF(f.name_1,'')
                 FROM erp_doc_format f
                WHERE lower(f.code) = lower(COALESCE(t.doc_format_code,''))
                ORDER BY f.screen_code, f.code
                LIMIT 1
           ), '') AS doc_format_name,
           t.trans_flag,
           'ap_ar_trans' AS table_name,
           COALESCE(t.cust_code,'') AS party_code,
           CASE WHEN upper(COALESCE(t.doc_format_code,'')) LIKE 'P%' THEN 'AP' ELSE 'AR' END AS party_type,
           COALESCE(ap.name_1, ar.name_1, '') AS party_name,
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
      LEFT JOIN ap_supplier ap ON ap.code = t.cust_code
      LEFT JOIN ar_customer ar ON ar.code = t.cust_code
     WHERE t.doc_no = $1 AND COALESCE(t.last_status,0)=0` + filter + `
)
SELECT doc_no, doc_date, doc_time, doc_format_code, doc_format_name, trans_flag, table_name, party_code, party_type, party_name, total_amount, is_lock_record
  FROM candidates
 ORDER BY CASE table_name WHEN 'ic_trans' THEN 1 ELSE 2 END, doc_date DESC
 LIMIT 1`

	var node RelatedDocumentNode
	var docDate time.Time
	err := q.QueryRow(ctx, query, args...).Scan(
		&node.DocNo,
		&docDate,
		&node.DocTime,
		&node.DocFormatCode,
		&node.DocFormatName,
		&node.TransFlag,
		&node.Table,
		&node.PartyCode,
		&node.PartyType,
		&node.PartyName,
		&node.TotalAmount,
		&node.IsLockRecord,
	)
	if err != nil {
		return node, err
	}
	node.DocDate = docDate.Format("2006-01-02")
	if meta, ok := lookupTransFlagCatalog(node.TransFlag, node.Table); ok {
		node.TransFlagMenu = meta.MenuCode
		node.TransFlagNameTH = meta.NameTH
		node.TransFlagNameEN = meta.NameEN
		node.TransType = meta.Type
	}
	return node, nil
}

func findRelatedDocumentEdges(ctx context.Context, q relatedDocumentQuerier, docNo string) ([]RelatedDocumentEdge, error) {
	rows, err := q.Query(ctx, `
SELECT from_doc_no, to_doc_no, relation, source_table, source_column
FROM (
    SELECT trim(ref_doc_no) AS from_doc_no,
           trim(doc_no) AS to_doc_no,
           'เอกสารอ้างอิงสินค้า/ซื้อ' AS relation,
           'ic_trans_detail' AS source_table,
           'ref_doc_no' AS source_column
      FROM ic_trans_detail
     WHERE COALESCE(last_status,0)=0
       AND COALESCE(ref_doc_no,'') <> ''
       AND (doc_no = $1 OR ref_doc_no = $1)
     GROUP BY trim(ref_doc_no), trim(doc_no)
    UNION ALL
    SELECT trim(billing_no) AS from_doc_no,
           trim(doc_no) AS to_doc_no,
           'เอกสารรับวางบิล/ชำระอ้างอิงเอกสารซื้อ' AS relation,
           'ap_ar_trans_detail' AS source_table,
           'billing_no' AS source_column
      FROM ap_ar_trans_detail
     WHERE COALESCE(last_status,0)=0
       AND COALESCE(billing_no,'') <> ''
       AND (doc_no = $1 OR billing_no = $1)
     GROUP BY trim(billing_no), trim(doc_no)
    UNION ALL
    SELECT trim(doc_ref) AS from_doc_no,
           trim(doc_no) AS to_doc_no,
           'เอกสารจ่ายชำระอ้างอิงใบรับวางบิล' AS relation,
           'ap_ar_trans_detail' AS source_table,
           'doc_ref' AS source_column
      FROM ap_ar_trans_detail
     WHERE COALESCE(last_status,0)=0
       AND COALESCE(doc_ref,'') <> ''
       AND (doc_no = $1 OR doc_ref = $1)
     GROUP BY trim(doc_ref), trim(doc_no)
) edges
WHERE from_doc_no <> '' AND to_doc_no <> '' AND from_doc_no <> to_doc_no
ORDER BY from_doc_no, to_doc_no, source_table, source_column`, docNo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	edges := []RelatedDocumentEdge{}
	for rows.Next() {
		var edge RelatedDocumentEdge
		if err := rows.Scan(&edge.FromDocNo, &edge.ToDocNo, &edge.Relation, &edge.SourceTable, &edge.SourceColumn); err != nil {
			return nil, err
		}
		edges = append(edges, edge)
	}
	return edges, rows.Err()
}

type directReferenceCandidate struct {
	DocNo        string
	SourceTable  string
	SourceColumn string
}

func findDirectReferenceCandidates(ctx context.Context, q relatedDocumentQuerier, docNo string) ([]directReferenceCandidate, error) {
	rows, err := q.Query(ctx, `
SELECT ref_doc_no, source_table, source_column
FROM (
    SELECT trim(COALESCE(ref_doc_no,'')) AS ref_doc_no,
           'ic_trans_detail' AS source_table,
           'ref_doc_no' AS source_column
     FROM ic_trans_detail
     WHERE COALESCE(last_status,0)=0
       AND doc_no = $1
       AND trim(COALESCE(ref_doc_no,'')) <> ''
     GROUP BY trim(COALESCE(ref_doc_no,''))
    UNION ALL
    SELECT trim(COALESCE(billing_no,'')) AS ref_doc_no,
           'ap_ar_trans_detail' AS source_table,
           'billing_no' AS source_column
     FROM ap_ar_trans_detail
     WHERE COALESCE(last_status,0)=0
       AND doc_no = $1
       AND trim(COALESCE(billing_no,'')) <> ''
     GROUP BY trim(COALESCE(billing_no,''))
    UNION ALL
    SELECT trim(COALESCE(doc_ref,'')) AS ref_doc_no,
           'ap_ar_trans_detail' AS source_table,
           'doc_ref' AS source_column
     FROM ap_ar_trans_detail
     WHERE COALESCE(last_status,0)=0
       AND doc_no = $1
       AND trim(COALESCE(doc_ref,'')) <> ''
     GROUP BY trim(COALESCE(doc_ref,''))
) refs
WHERE ref_doc_no <> ''
ORDER BY ref_doc_no, source_table, source_column`, strings.TrimSpace(docNo))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []directReferenceCandidate{}
	for rows.Next() {
		var item directReferenceCandidate
		if err := rows.Scan(&item.DocNo, &item.SourceTable, &item.SourceColumn); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func normalizeDirectReferenceCandidates(items []directReferenceCandidate, limit int) ([]directReferenceCandidate, bool) {
	byDocNo := map[string]directReferenceCandidate{}
	for _, item := range items {
		item.DocNo = strings.TrimSpace(item.DocNo)
		item.SourceTable = strings.TrimSpace(item.SourceTable)
		item.SourceColumn = strings.TrimSpace(item.SourceColumn)
		key := strings.ToUpper(item.DocNo)
		if item.DocNo == "" {
			continue
		}
		current, ok := byDocNo[key]
		if !ok || directReferencePriority(item) < directReferencePriority(current) {
			byDocNo[key] = item
		}
	}
	out := make([]directReferenceCandidate, 0, len(byDocNo))
	for _, item := range byDocNo {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DocNo == out[j].DocNo {
			return directReferencePriority(out[i]) < directReferencePriority(out[j])
		}
		return out[i].DocNo < out[j].DocNo
	})
	truncated := false
	if limit > 0 && len(out) > limit {
		out = out[:limit]
		truncated = true
	}
	return out, truncated
}

func directReferencePriority(item directReferenceCandidate) int {
	return relatedSourceColumnPriority(RelatedDocumentEdge{
		SourceTable:  item.SourceTable,
		SourceColumn: item.SourceColumn,
	})
}

func documentReferenceItemFromNode(node RelatedDocumentNode, source directReferenceCandidate) DocumentReferenceItem {
	return DocumentReferenceItem{
		DocNo:           node.DocNo,
		DocDate:         node.DocDate,
		DocTime:         node.DocTime,
		DocFormatCode:   node.DocFormatCode,
		DocFormatName:   node.DocFormatName,
		TransFlag:       node.TransFlag,
		TransFlagMenu:   node.TransFlagMenu,
		TransFlagNameTH: node.TransFlagNameTH,
		TransFlagNameEN: node.TransFlagNameEN,
		TransType:       node.TransType,
		Table:           node.Table,
		PartyCode:       node.PartyCode,
		PartyName:       node.PartyName,
		PartyType:       node.PartyType,
		TotalAmount:     node.TotalAmount,
		IsLockRecord:    node.IsLockRecord,
		SourceTable:     source.SourceTable,
		SourceColumn:    source.SourceColumn,
	}
}

type relatedDocumentQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}
