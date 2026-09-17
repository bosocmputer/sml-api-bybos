package handlers

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// erpLogDiff turns SML's erp_logs data_old/data_new payloads into a list of
// human-readable field changes.
//
// The payload is SML's own "what the user saw on screen" snapshot
// (screentop/screenmore/screenbottom/screendetail/screenshipment), NOT a raw
// table row. That is exactly why this replaced the source-revision hash:
// the hash covered every column including SML's internal bookkeeping
// (used_status, guid_code, stock recalculation), which produced repeated
// false "the user edited this document" reports on documents nobody touched.
// Everything in this payload is a field a person actually typed.
//
// Two kinds of noise still exist in real production payloads and are
// normalized away here, both confirmed against drh (Damrong) production data:
//
//  1. Numeric formatting. The same value appears as "0.00000000000000" in one
//     snapshot and "0" in the next, or 635.73000000000000 becomes 635.74 from
//     a rounding pass. Compared as strings these look like edits; compared as
//     numbers rounded to 2 decimals they are correctly identical/different.
//     (2 decimals matches the tolerance already used for amount comparison
//     elsewhere in this system.)
//
//  2. Row position. SML writes detail rows positionally, so inserting or
//     removing one line shifts every subsequent row and a single added line
//     shows up as "78 fields changed". Rows are therefore matched by identity
//     (item_code + line_number), not by array index.
//
// The goal is that an empty diff genuinely means "nothing a user cares about
// changed" — that is what lets a re-save with no edits stop blocking a
// document, which was the original complaint.

// erpFieldChange is one normalized before/after difference.
type erpFieldChange struct {
	// Section is the SML screen area the field belongs to, e.g. "screentop"
	// or "screendetail" — used to group changes in the UI.
	Section string `json:"section"`
	// Field is the SML field name, e.g. "remark" or "qty".
	Field string `json:"field"`
	// Label is the Thai display label when one is known, else Field.
	Label string `json:"label"`
	// RowKey identifies which detail row changed (item_code), empty for
	// header sections.
	RowKey string `json:"rowKey,omitempty"`
	// RowLabel is the human-facing name of that row (item_name), empty for
	// header sections.
	RowLabel string `json:"rowLabel,omitempty"`
	Old      string `json:"old"`
	New      string `json:"new"`
}

// erpLogPayload is the decoded shape of erp_logs.data_old / data_new.
// Every value is decoded as `any` because SML writes numbers as JSON strings
// in detail rows but as real JSON numbers in header sections.
type erpLogPayload struct {
	ScreenTop      map[string]any   `json:"screentop"`
	ScreenMore     map[string]any   `json:"screenmore"`
	ScreenBottom   map[string]any   `json:"screenbottom"`
	ScreenDetail   []map[string]any `json:"screendetail"`
	ScreenShipment map[string]any   `json:"screenshipment"`
}

// maxERPLogPayloadBytes caps how large a single data_old/data_new blob may be
// before diffing is skipped. Production payloads average ~14KB but reach
// 330KB; a document with many edit rounds would otherwise hold tens of MB in
// memory just to render a change list. Past this size the caller reports
// "edited, details too large to display" rather than attempting a diff.
const maxERPLogPayloadBytes = 1 << 20 // 1MB

// erpFieldLabels maps SML field names to the Thai wording a user recognizes
// from the ERP screen. Unmapped fields fall back to the raw name rather than
// being hidden, so a field SML adds later still surfaces (just untranslated)
// instead of silently disappearing from the diff.
var erpFieldLabels = map[string]string{
	"doc_date":         "วันที่เอกสาร",
	"doc_no":           "เลขที่เอกสาร",
	"cust_code":        "รหัสผู้ติดต่อ",
	"doc_format_code":  "รูปแบบเอกสาร",
	"vat_type":         "ประเภทภาษี",
	"vat_rate":         "อัตราภาษี",
	"total_value":      "มูลค่ารวม",
	"total_amount":     "ยอดรวมทั้งสิ้น",
	"total_before_vat": "มูลค่าก่อนภาษี",
	"total_vat_value":  "ภาษีมูลค่าเพิ่ม",
	"total_after_vat":  "มูลค่าหลังภาษี",
	"total_discount":   "ส่วนลดรวม",
	"credit_day":       "เครดิต (วัน)",
	"credit_date":      "วันครบกำหนด",
	"remark":           "หมายเหตุ",
	"remark_2":         "หมายเหตุ 2",
	"remark_3":         "หมายเหตุ 3",
	"remark_4":         "หมายเหตุ 4",
	"remark_5":         "หมายเหตุ 5",
	"department_code":  "แผนก",
	"branch_code":      "สาขา",
	"job_code":         "job",
	"item_code":        "รหัสสินค้า",
	"item_name":        "ชื่อสินค้า",
	"qty":              "จำนวน",
	"price":            "ราคา",
	"discount":         "ส่วนลด",
	"sum_amount":       "จำนวนเงิน",
	"unit_code":        "หน่วยนับ",
	"wh_code":          "คลัง",
	"shelf_code":       "ที่เก็บ",
	"barcode":          "บาร์โค้ด",
}

// erpIgnoredFields are fields present in the payload that never represent a
// user-visible edit. These are SML-internal bookkeeping that changes on its
// own — exactly the category of noise that made the old hash unusable — so
// including them here would reintroduce the same false positives.
var erpIgnoredFields = map[string]struct{}{
	"ref_guid":      {},
	"price_guid":    {},
	"doc_no_guid":   {},
	"period_guid":   {},
	"is_lock_cost":  {},
	"is_get_price":  {},
	"line_number":   {},
	"roworder":      {},
	"guid_code":     {},
	"last_status":   {},
	"create_time":   {},
	"lastedit_time": {},
}

// erpLabelFor returns the Thai label for a field, falling back to the raw
// field name so unknown fields remain visible rather than unnamed.
func erpLabelFor(field string) string {
	if label, ok := erpFieldLabels[field]; ok {
		return label
	}
	return field
}

// normalizeERPValue reduces a payload value to a canonical comparable string.
//
// It exists because SML represents the same value inconsistently between
// snapshots: numbers appear both as JSON numbers and as strings with 14
// trailing zeros, and "no value" appears as JSON null, the literal string
// "null", or an empty string. Without this, a re-save with zero real edits
// produces a long list of phantom changes.
func normalizeERPValue(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case bool:
		return strconv.FormatBool(val)
	case float64:
		return normalizeERPNumber(val)
	case json.Number:
		if f, err := val.Float64(); err == nil {
			return normalizeERPNumber(f)
		}
		return strings.TrimSpace(val.String())
	case string:
		s := strings.TrimSpace(val)
		// SML writes the four-character string "null" for empty fields in
		// detail rows, which must compare equal to a real JSON null.
		if s == "" || strings.EqualFold(s, "null") {
			return ""
		}
		// Detail-row numbers arrive as strings ("12.00000000000000").
		// Normalize only when the whole string parses as a number, so text
		// that merely starts with digits (an item code, a date) is left alone.
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return normalizeERPNumber(f)
		}
		return s
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", val))
	}
}

// normalizeERPNumber renders a number rounded to 2 decimals with no trailing
// zeros, so 0, 0.00 and 0.00000000000000 all collapse to "0".
func normalizeERPNumber(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return ""
	}
	rounded := math.Round(f*100) / 100
	// %g would switch to scientific notation on large document totals, so
	// format with fixed precision and strip the trailing zeros by hand.
	s := strconv.FormatFloat(rounded, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

// decodeERPLogPayload parses one data_old/data_new blob.
// An empty blob is valid and yields an empty payload: function_code=1
// (create) rows legitimately carry no data_old.
func decodeERPLogPayload(raw []byte) (erpLogPayload, error) {
	var payload erpLogPayload
	if len(raw) == 0 {
		return payload, nil
	}
	if len(raw) > maxERPLogPayloadBytes {
		return payload, fmt.Errorf("erp log payload too large: %d bytes", len(raw))
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return payload, fmt.Errorf("decode erp log payload: %w", err)
	}
	return payload, nil
}

// diffERPSection compares one header section and appends normalized changes.
func diffERPSection(section string, oldMap, newMap map[string]any, out *[]erpFieldChange) {
	keys := make(map[string]struct{}, len(oldMap)+len(newMap))
	for k := range oldMap {
		keys[k] = struct{}{}
	}
	for k := range newMap {
		keys[k] = struct{}{}
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		if _, skip := erpIgnoredFields[k]; skip {
			continue
		}
		names = append(names, k)
	}
	// Sorted so the same edit always renders in the same order, which keeps
	// the rendered diff stable for a user comparing two screenshots.
	sort.Strings(names)
	for _, name := range names {
		oldVal := normalizeERPValue(oldMap[name])
		newVal := normalizeERPValue(newMap[name])
		if oldVal == newVal {
			continue
		}
		*out = append(*out, erpFieldChange{
			Section: section,
			Field:   name,
			Label:   erpLabelFor(name),
			Old:     oldVal,
			New:     newVal,
		})
	}
}

// erpDetailRowKey builds the identity used to match a detail row across two
// snapshots. item_code alone is not unique (the same product can legitimately
// appear on several lines), so it is paired with line_number. Rows with
// neither fall back to their position, which is the best available identity.
func erpDetailRowKey(row map[string]any, index int) string {
	itemCode := normalizeERPValue(row["item_code"])
	lineNumber := normalizeERPValue(row["line_number"])
	switch {
	case itemCode != "" && lineNumber != "":
		return itemCode + "#" + lineNumber
	case itemCode != "":
		return itemCode
	case lineNumber != "":
		return "#" + lineNumber
	default:
		return "@" + strconv.Itoa(index)
	}
}

// diffERPDetail compares detail rows by identity rather than array position.
//
// Matching positionally would report an entire document as changed when a
// user inserts one line in the middle: every row below it shifts by one and
// every field on those rows appears different. Production data confirms this
// — single-line insertions showed up as "78 fields changed".
func diffERPDetail(oldRows, newRows []map[string]any, out *[]erpFieldChange) {
	oldByKey := make(map[string]map[string]any, len(oldRows))
	oldOrder := make([]string, 0, len(oldRows))
	for i, row := range oldRows {
		key := erpDetailRowKey(row, i)
		if _, exists := oldByKey[key]; exists {
			// Duplicate identity (same item and line number twice) — suffix
			// so both rows stay addressable instead of one overwriting the
			// other and being misreported as removed.
			key = fmt.Sprintf("%s@%d", key, i)
		}
		oldByKey[key] = row
		oldOrder = append(oldOrder, key)
	}

	seen := make(map[string]struct{}, len(newRows))
	for i, newRow := range newRows {
		key := erpDetailRowKey(newRow, i)
		if _, exists := seen[key]; exists {
			key = fmt.Sprintf("%s@%d", key, i)
		}
		seen[key] = struct{}{}
		rowLabel := normalizeERPValue(newRow["item_name"])

		oldRow, existed := oldByKey[key]
		if !existed {
			*out = append(*out, erpFieldChange{
				Section:  "screendetail",
				Field:    "row_added",
				Label:    "เพิ่มรายการ",
				RowKey:   normalizeERPValue(newRow["item_code"]),
				RowLabel: rowLabel,
				Old:      "",
				New:      erpDetailRowSummary(newRow),
			})
			continue
		}

		var rowChanges []erpFieldChange
		diffERPSection("screendetail", oldRow, newRow, &rowChanges)
		for idx := range rowChanges {
			rowChanges[idx].RowKey = normalizeERPValue(newRow["item_code"])
			if rowLabel != "" {
				rowChanges[idx].RowLabel = rowLabel
			} else {
				rowChanges[idx].RowLabel = normalizeERPValue(oldRow["item_name"])
			}
		}
		*out = append(*out, rowChanges...)
	}

	for _, key := range oldOrder {
		if _, stillPresent := seen[key]; stillPresent {
			continue
		}
		oldRow := oldByKey[key]
		*out = append(*out, erpFieldChange{
			Section:  "screendetail",
			Field:    "row_removed",
			Label:    "ลบรายการ",
			RowKey:   normalizeERPValue(oldRow["item_code"]),
			RowLabel: normalizeERPValue(oldRow["item_name"]),
			Old:      erpDetailRowSummary(oldRow),
			New:      "",
		})
	}
}

// erpDetailRowSummary renders a one-line description of an added/removed row
// so the UI can show what the line was without dumping every column.
func erpDetailRowSummary(row map[string]any) string {
	name := normalizeERPValue(row["item_name"])
	if name == "" {
		name = normalizeERPValue(row["item_code"])
	}
	qty := normalizeERPValue(row["qty"])
	price := normalizeERPValue(row["price"])
	switch {
	case qty != "" && price != "":
		return fmt.Sprintf("%s (จำนวน %s x %s)", name, qty, price)
	case qty != "":
		return fmt.Sprintf("%s (จำนวน %s)", name, qty)
	default:
		return name
	}
}

// diffERPLogPayloads produces the full normalized change list between two
// snapshots. An empty result means the save recorded no user-visible change —
// the case that must NOT block a document, since production data shows a
// large share of edit-log rows are plain re-saves.
func diffERPLogPayloads(oldRaw, newRaw []byte) ([]erpFieldChange, error) {
	oldPayload, err := decodeERPLogPayload(oldRaw)
	if err != nil {
		return nil, err
	}
	newPayload, err := decodeERPLogPayload(newRaw)
	if err != nil {
		return nil, err
	}

	changes := make([]erpFieldChange, 0, 8)
	diffERPSection("screentop", oldPayload.ScreenTop, newPayload.ScreenTop, &changes)
	diffERPSection("screenmore", oldPayload.ScreenMore, newPayload.ScreenMore, &changes)
	diffERPSection("screenbottom", oldPayload.ScreenBottom, newPayload.ScreenBottom, &changes)
	diffERPSection("screenshipment", oldPayload.ScreenShipment, newPayload.ScreenShipment, &changes)
	diffERPDetail(oldPayload.ScreenDetail, newPayload.ScreenDetail, &changes)
	return changes, nil
}
