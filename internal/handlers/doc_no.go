package handlers

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/middleware"
	"sml-api-bybos/internal/models"
)

type DocNoHandler struct {
	dbm *db.Manager
}

func NewDocNoHandler(dbm *db.Manager) *DocNoHandler {
	return &DocNoHandler{dbm: dbm}
}

type docNoRoute struct {
	name      string
	transFlag int
	table     string
}

type nextDocNoResult struct {
	Route     string `json:"route"`
	TransFlag int    `json:"trans_flag"`
	Prefix    string `json:"prefix"`
	Format    string `json:"format"`
	DocDate   string `json:"doc_date"`
	LastDocNo string `json:"last_doc_no"`
	LastSeq   int    `json:"last_seq"`
	NextDocNo string `json:"next_doc_no"`
	NextSeq   int    `json:"next_seq"`
}

var docNoRoutes = map[string]docNoRoute{
	"saleorder":        {name: "saleorder", transFlag: models.TransFlagSaleOrder, table: "ic_trans"},
	"sale_order":       {name: "saleorder", transFlag: models.TransFlagSaleOrder, table: "ic_trans"},
	"so":               {name: "saleorder", transFlag: models.TransFlagSaleOrder, table: "ic_trans"},
	"saleinvoice":      {name: "saleinvoice", transFlag: models.TransFlagSaleInvoice, table: "ic_trans"},
	"sale_invoice":     {name: "saleinvoice", transFlag: models.TransFlagSaleInvoice, table: "ic_trans"},
	"si":               {name: "saleinvoice", transFlag: models.TransFlagSaleInvoice, table: "ic_trans"},
	"creditnote":       {name: "creditnote", transFlag: models.TransFlagCreditNote, table: "ic_trans"},
	"credit_note":      {name: "creditnote", transFlag: models.TransFlagCreditNote, table: "ic_trans"},
	"cn":               {name: "creditnote", transFlag: models.TransFlagCreditNote, table: "ic_trans"},
	"sale_cancel":      {name: "creditnote", transFlag: models.TransFlagCreditNote, table: "ic_trans"},
	"purchaseorder":    {name: "purchaseorder", transFlag: models.TransFlagPurchaseOrder, table: "ic_trans"},
	"purchase_order":   {name: "purchaseorder", transFlag: models.TransFlagPurchaseOrder, table: "ic_trans"},
	"po":               {name: "purchaseorder", transFlag: models.TransFlagPurchaseOrder, table: "ic_trans"},
	"purchaseinvoice":  {name: "purchaseinvoice", transFlag: models.TransFlagPurchaseInvoice, table: "ic_trans"},
	"purchase_invoice": {name: "purchaseinvoice", transFlag: models.TransFlagPurchaseInvoice, table: "ic_trans"},
	"pa":               {name: "purchaseinvoice", transFlag: models.TransFlagPurchaseInvoice, table: "ic_trans"},
	"billreceive":      {name: "billreceive", transFlag: models.TransFlagBillReceive, table: "ap_ar_trans"},
	"bill_receive":     {name: "billreceive", transFlag: models.TransFlagBillReceive, table: "ap_ar_trans"},
	"pb":               {name: "billreceive", transFlag: models.TransFlagBillReceive, table: "ap_ar_trans"},
	"payment":          {name: "payment", transFlag: models.TransFlagPayment, table: "ap_ar_trans"},
	"pv":               {name: "payment", transFlag: models.TransFlagPayment, table: "ap_ar_trans"},
	"receipt":          {name: "receipt", transFlag: models.TransFlagARReceipt, table: "ap_ar_trans"},
	"ar_receipt":       {name: "receipt", transFlag: models.TransFlagARReceipt, table: "ap_ar_trans"},
	"rc":               {name: "receipt", transFlag: models.TransFlagARReceipt, table: "ap_ar_trans"},
}

// GET /api/v1/ic/doc-no/next?route=purchaseorder&prefix=BF-PO&format=YYMM####&doc_date=2026-05-25
func (h *DocNoHandler) Next(c *gin.Context) {
	route, ok := resolveDocNoRoute(c.Query("route"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "route must be saleorder, saleinvoice, creditnote, purchaseorder, or receipt"})
		return
	}
	prefix := strings.TrimSpace(c.DefaultQuery("prefix", "BF"))
	format := strings.TrimSpace(c.DefaultQuery("format", "YYMM####"))
	docDate := strings.TrimSpace(c.Query("doc_date"))
	now := time.Now()
	if docDate != "" {
		parsed, err := time.Parse("2006-01-02", docDate)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "doc_date format must be YYYY-MM-DD"})
			return
		}
		now = parsed
	}

	queryPrefix, err := docNoStaticPrefix(prefix, format, now)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	pool, err := h.dbm.Get(ctx, c.GetString(middleware.TenantKey))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	rows, err := pool.Query(ctx,
		`SELECT COALESCE(doc_no, '')
		   FROM `+route.table+`
		  WHERE trans_flag = $1
		    AND last_status = 0
		    AND doc_no LIKE $2 ESCAPE '\'
		  ORDER BY doc_no DESC`,
		route.transFlag,
		escapeSQLLike(queryPrefix)+"%",
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	defer rows.Close()

	var existing []string
	for rows.Next() {
		var docNo string
		if err := rows.Scan(&docNo); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
			return
		}
		existing = append(existing, docNo)
	}
	result, err := nextDocNoFromExisting(route, prefix, format, now, existing)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

func resolveDocNoRoute(route string) (docNoRoute, bool) {
	key := strings.ToLower(strings.TrimSpace(route))
	r, ok := docNoRoutes[key]
	return r, ok
}

func nextDocNoFromExisting(route docNoRoute, prefix, format string, now time.Time, existing []string) (nextDocNoResult, error) {
	if prefix == "" {
		prefix = "BF"
	}
	if format == "" {
		format = "YYMM####"
	}
	re, _, _, err := docNoRegex(prefix, format, now)
	if err != nil {
		return nextDocNoResult{}, err
	}
	lastSeq := 0
	lastDocNo := ""
	for _, docNo := range existing {
		m := re.FindStringSubmatch(strings.TrimSpace(docNo))
		if len(m) != 2 {
			continue
		}
		seq, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if seq > lastSeq {
			lastSeq = seq
			lastDocNo = strings.TrimSpace(docNo)
		}
	}
	nextSeq := lastSeq + 1
	return nextDocNoResult{
		Route:     route.name,
		TransFlag: route.transFlag,
		Prefix:    prefix,
		Format:    format,
		DocDate:   now.Format("2006-01-02"),
		LastDocNo: lastDocNo,
		LastSeq:   lastSeq,
		NextDocNo: renderDocNo(prefix, format, now, nextSeq),
		NextSeq:   nextSeq,
	}, nil
}

func docNoStaticPrefix(prefix, format string, now time.Time) (string, error) {
	_, staticPrefix, _, err := docNoRegex(prefix, format, now)
	return staticPrefix, err
}

func docNoRegex(prefix, format string, now time.Time) (*regexp.Regexp, string, int, error) {
	if prefix == "" {
		prefix = "BF"
	}
	if format == "" {
		format = "YYMM####"
	}
	var b strings.Builder
	b.WriteString("^")
	b.WriteString(regexp.QuoteMeta(prefix))
	staticPrefix := prefix
	seenSeq := false
	width := 0

	for i := 0; i < len(format); {
		switch {
		case strings.HasPrefix(format[i:], "YYYY"):
			token := fmt.Sprintf("%04d", now.Year())
			b.WriteString(regexp.QuoteMeta(token))
			if !seenSeq {
				staticPrefix += token
			}
			i += 4
		case strings.HasPrefix(format[i:], "YY"):
			token := fmt.Sprintf("%02d", now.Year()%100)
			b.WriteString(regexp.QuoteMeta(token))
			if !seenSeq {
				staticPrefix += token
			}
			i += 2
		case strings.HasPrefix(format[i:], "MM"):
			token := fmt.Sprintf("%02d", int(now.Month()))
			b.WriteString(regexp.QuoteMeta(token))
			if !seenSeq {
				staticPrefix += token
			}
			i += 2
		case strings.HasPrefix(format[i:], "DD"):
			token := fmt.Sprintf("%02d", now.Day())
			b.WriteString(regexp.QuoteMeta(token))
			if !seenSeq {
				staticPrefix += token
			}
			i += 2
		case format[i] == '#':
			j := i
			for j < len(format) && format[j] == '#' {
				j++
			}
			if seenSeq {
				return nil, "", 0, fmt.Errorf("format can contain only one # sequence block")
			}
			width = j - i
			b.WriteString("(\\d{")
			b.WriteString(strconv.Itoa(width))
			b.WriteString(",})")
			seenSeq = true
			i = j
		default:
			ch := format[i : i+1]
			b.WriteString(regexp.QuoteMeta(ch))
			if !seenSeq {
				staticPrefix += ch
			}
			i++
		}
	}
	if !seenSeq {
		return nil, "", 0, fmt.Errorf("format must contain a # sequence block")
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, "", 0, err
	}
	return re, staticPrefix, width, nil
}

func renderDocNo(prefix, format string, now time.Time, seq int) string {
	_, _, width, err := docNoRegex(prefix, format, now)
	if err != nil || width <= 0 {
		width = 4
	}
	out := format
	out = strings.ReplaceAll(out, "YYYY", fmt.Sprintf("%04d", now.Year()))
	out = strings.ReplaceAll(out, "YY", fmt.Sprintf("%02d", now.Year()%100))
	out = strings.ReplaceAll(out, "MM", fmt.Sprintf("%02d", int(now.Month())))
	out = strings.ReplaceAll(out, "DD", fmt.Sprintf("%02d", now.Day()))
	hashRe := regexp.MustCompile(`#+`)
	out = hashRe.ReplaceAllString(out, fmt.Sprintf("%0*d", width, seq))
	return prefix + out
}

func escapeSQLLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
