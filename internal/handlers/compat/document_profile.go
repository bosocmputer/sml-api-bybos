package compat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"math/big"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

var exactDecimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,15})(\.[0-9]{1,6})?$`)

const smlSaleVATRegisterType = 0

func smlVATEffectivePeriod(docDate time.Time) (int, int) {
	return int(docDate.Month()), docDate.Year() + 543
}

func normalizeAndValidateProfile(p *docPayload, items []docItem, route docRoute) error {
	if p.DocumentProfileVersion == "" {
		return nil
	}
	if route.name != routeSaleInvoice.name {
		return fmt.Errorf("document_profile_version is supported only for sale invoices")
	}
	p.CreatorCode = strings.TrimSpace(p.CreatorCode)
	p.CashierCode = strings.TrimSpace(p.CashierCode)
	p.CurrencyCode = strings.TrimSpace(p.CurrencyCode)
	p.UserRequest = strings.TrimSpace(p.UserRequest)
	for _, field := range []struct {
		name     string
		value    *string
		required string
	}{
		{"creator_code", &p.CreatorCode, "BILLFLOW"},
		{"cashier_code", &p.CashierCode, "BILLFLOW"},
		{"currency_code", &p.CurrencyCode, "THB"},
		{"user_request", &p.UserRequest, "NEXFLOW"},
	} {
		if *field.value == "" {
			*field.value = field.required
		}
		if *field.value != field.required {
			return fmt.Errorf("%s must be %s for %s", field.name, field.required, documentProfileV1)
		}
	}
	if strings.TrimSpace(p.ExchangeRateDecimal) == "" {
		p.ExchangeRateDecimal = "1"
	}
	var err error
	p.ExchangeRateDecimal, _, err = normalizeExactDecimal("exchange_rate_decimal", p.ExchangeRateDecimal, false)
	if err != nil || p.ExchangeRateDecimal != "1" {
		return fmt.Errorf("exchange_rate_decimal must be 1 for %s", documentProfileV1)
	}
	if err := validateBoundedLiteral("remark_5", p.Remark5, 255); err != nil {
		return err
	}
	if !strings.HasPrefix(p.Remark5, "NEXFLOW|") {
		return fmt.Errorf("remark_5 must use NEXFLOW|<channel>|<order-or-bill>")
	}

	p.ShipmentApplicability = strings.TrimSpace(p.ShipmentApplicability)
	if p.MarketplacePhysicalGoods && p.ShipmentApplicability != "required" {
		return fmt.Errorf("marketplace physical-goods documents require shipment_applicability=required")
	}
	switch p.ShipmentApplicability {
	case "required":
		if p.Shipment == nil {
			return fmt.Errorf("shipment is required before the core document can be created")
		}
		p.Shipment.TransportName = strings.TrimSpace(p.Shipment.TransportName)
		p.Shipment.TransportAddress = strings.TrimSpace(p.Shipment.TransportAddress)
		p.Shipment.TransportTelephone = strings.TrimSpace(p.Shipment.TransportTelephone)
		for _, field := range []struct {
			name, value string
			max         int
		}{
			{"shipment.transport_name", p.Shipment.TransportName, 255},
			{"shipment.transport_address", p.Shipment.TransportAddress, 1000},
			{"shipment.transport_telephone", p.Shipment.TransportTelephone, 100},
		} {
			if field.value == "" {
				return fmt.Errorf("%s is required", field.name)
			}
			if err := validateBoundedLiteral(field.name, field.value, field.max); err != nil {
				return err
			}
		}
	case "not_applicable":
		if p.MarketplacePhysicalGoods {
			return fmt.Errorf("shipment cannot be not_applicable for marketplace physical goods")
		}
		if p.Shipment != nil {
			return fmt.Errorf("shipment must be omitted when shipment_applicability=not_applicable")
		}
	default:
		return fmt.Errorf("shipment_applicability must be required or not_applicable")
	}

	bindings := []struct {
		name     string
		exact    *string
		numeric  *float64
		positive bool
	}{
		{"vat_rate_decimal", &p.VATRateDecimal, &p.VATRate, false},
		{"total_value_decimal", &p.TotalValueDecimal, &p.TotalValue, false},
		{"total_discount_decimal", &p.TotalDiscountDecimal, &p.TotalDiscount, false},
		{"total_before_vat_decimal", &p.TotalBeforeVATDecimal, &p.TotalBeforeVAT, false},
		{"total_vat_value_decimal", &p.TotalVATValueDecimal, &p.TotalVATValue, false},
		{"total_except_vat_decimal", &p.TotalExceptVATDecimal, &p.TotalExceptVAT, false},
		{"total_after_vat_decimal", &p.TotalAfterVATDecimal, &p.TotalAfterVAT, false},
		{"total_amount_decimal", &p.TotalAmountDecimal, &p.TotalAmount, false},
	}
	for _, binding := range bindings {
		if err := bindExactDecimal(binding.name, binding.exact, binding.numeric, binding.positive); err != nil {
			return err
		}
	}

	lineNumbers := make(map[int]struct{}, len(items))
	for i := range items {
		if items[i].LineNumber < 0 {
			return fmt.Errorf("item %d line_number must be >= 0", i)
		}
		if _, duplicate := lineNumbers[items[i].LineNumber]; duplicate {
			return fmt.Errorf("item %d duplicates line_number %d", i, items[i].LineNumber)
		}
		lineNumbers[items[i].LineNumber] = struct{}{}
		for _, binding := range []struct {
			name     string
			exact    *string
			numeric  *float64
			positive bool
		}{
			{fmt.Sprintf("item %d qty_decimal", i), &items[i].QtyDecimal, &items[i].Qty, true},
			{fmt.Sprintf("item %d price_decimal", i), &items[i].PriceDecimal, &items[i].Price, false},
			{fmt.Sprintf("item %d price_exclude_vat_decimal", i), &items[i].PriceExcludeVATDecimal, &items[i].PriceExcludeVAT, false},
			{fmt.Sprintf("item %d discount_amount_decimal", i), &items[i].DiscountAmountDecimal, &items[i].DiscountAmount, false},
			{fmt.Sprintf("item %d sum_amount_decimal", i), &items[i].SumAmountDecimal, &items[i].SumAmount, false},
			{fmt.Sprintf("item %d vat_amount_decimal", i), &items[i].VATAmountDecimal, &items[i].VATAmount, false},
			{fmt.Sprintf("item %d sum_amount_exclude_vat_decimal", i), &items[i].SumAmountExclVATDecimal, &items[i].SumAmountExclVAT, false},
		} {
			if err := bindExactDecimal(binding.name, binding.exact, binding.numeric, binding.positive); err != nil {
				return err
			}
		}
		items[i].TotalVATValue = items[i].VATAmount
	}
	if err := requireDetailTotal(p.TotalValueDecimal, items); err != nil {
		return err
	}
	if err := validateProfileVATTotals(*p, items, route); err != nil {
		return err
	}
	return nil
}

func validateProfileVATTotals(p docPayload, items []docItem, route docRoute) error {
	if route.name != routeSaleInvoice.name {
		return nil
	}
	value, _ := new(big.Rat).SetString(p.TotalValueDecimal)
	discount, _ := new(big.Rat).SetString(p.TotalDiscountDecimal)
	net := new(big.Rat).Sub(value, discount)
	if net.Sign() < 0 {
		return fmt.Errorf("total_discount_decimal must not exceed total_value_decimal")
	}
	rate, _ := new(big.Rat).SetString(p.VATRateDecimal)
	expectedBefore := new(big.Rat)
	expectedVAT := new(big.Rat)
	expectedAfter := new(big.Rat)
	expectedAmount := new(big.Rat)
	switch p.VATType {
	case 0:
		expectedBefore.Set(net)
		expectedVAT = roundMoneyRat(new(big.Rat).Quo(new(big.Rat).Mul(net, rate), big.NewRat(100, 1)))
		expectedAfter.Add(net, expectedVAT)
		expectedAmount.Set(expectedAfter)
	case 1:
		divisor := new(big.Rat).Add(big.NewRat(1, 1), new(big.Rat).Quo(rate, big.NewRat(100, 1)))
		expectedBefore = roundMoneyRat(new(big.Rat).Quo(net, divisor))
		expectedVAT.Sub(net, expectedBefore)
		expectedAfter.Set(net)
		expectedAmount.Set(net)
	case 2:
		expectedAmount.Set(net)
		for i, item := range items {
			itemVAT, _ := new(big.Rat).SetString(item.VATAmountDecimal)
			if itemVAT.Sign() != 0 {
				return fmt.Errorf("vat_type 2 item %d vat_amount_decimal must be zero", i)
			}
		}
	}
	checks := []struct {
		name     string
		actual   string
		expected *big.Rat
	}{
		{"total_before_vat_decimal", p.TotalBeforeVATDecimal, expectedBefore},
		{"total_vat_value_decimal", p.TotalVATValueDecimal, expectedVAT},
		{"total_after_vat_decimal", p.TotalAfterVATDecimal, expectedAfter},
		{"total_amount_decimal", p.TotalAmountDecimal, expectedAmount},
		{"total_except_vat_decimal", p.TotalExceptVATDecimal, new(big.Rat)},
	}
	for _, check := range checks {
		actual, _ := new(big.Rat).SetString(check.actual)
		diff := new(big.Rat).Sub(actual, check.expected)
		if diff.Sign() < 0 {
			diff.Neg(diff)
		}
		if diff.Cmp(big.NewRat(1, 100)) > 0 {
			return fmt.Errorf("vat_type %d %s does not match the SML VAT contract", p.VATType, check.name)
		}
	}
	return nil
}

func roundMoneyRat(value *big.Rat) *big.Rat {
	scaled := new(big.Rat).Mul(value, big.NewRat(100, 1))
	quotient := new(big.Int).Quo(scaled.Num(), scaled.Denom())
	remainder := new(big.Int).Rem(scaled.Num(), scaled.Denom())
	if new(big.Int).Mul(remainder, big.NewInt(2)).Cmp(scaled.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return new(big.Rat).SetFrac(quotient, big.NewInt(100))
}

func normalizeExactDecimal(field, raw string, positive bool) (string, float64, error) {
	raw = strings.TrimSpace(raw)
	if !exactDecimalPattern.MatchString(raw) {
		return "", 0, fmt.Errorf("%s must be a base-10 decimal string with at most 6 fractional digits", field)
	}
	normalized := raw
	if strings.Contains(normalized, ".") {
		normalized = strings.TrimRight(normalized, "0")
		normalized = strings.TrimRight(normalized, ".")
	}
	value, err := strconv.ParseFloat(normalized, 64)
	if err != nil {
		return "", 0, fmt.Errorf("%s is invalid", field)
	}
	if positive && value <= 0 {
		return "", 0, fmt.Errorf("%s must be greater than zero", field)
	}
	return normalized, value, nil
}

func bindExactDecimal(field string, exact *string, numeric *float64, positive bool) error {
	normalized, value, err := normalizeExactDecimal(field, *exact, positive)
	if err != nil {
		return err
	}
	if diff := value - *numeric; diff > 0.01 || diff < -0.01 {
		return fmt.Errorf("%s does not match its numeric compatibility field", field)
	}
	*exact = normalized
	*numeric = value
	return nil
}

func requireDetailTotal(header string, items []docItem) error {
	headerRat, ok := new(big.Rat).SetString(header)
	if !ok {
		return fmt.Errorf("total_value_decimal is invalid")
	}
	sum := new(big.Rat)
	for _, item := range items {
		value, ok := new(big.Rat).SetString(item.SumAmountDecimal)
		if !ok {
			return fmt.Errorf("item sum_amount_decimal is invalid")
		}
		sum.Add(sum, value)
	}
	diff := new(big.Rat).Sub(headerRat, sum)
	if diff.Sign() < 0 {
		diff.Neg(diff)
	}
	if diff.Cmp(big.NewRat(1, 100)) > 0 {
		return fmt.Errorf("document header total_value_decimal does not match detail sum within 0.01")
	}
	return nil
}

func validateBoundedLiteral(field, value string, maxRunes int) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", field)
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return fmt.Errorf("%s must not exceed %d Unicode characters", field, maxRunes)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s must not contain control characters", field)
		}
	}
	return nil
}

type canonicalProfileItem struct {
	LineNumber      int    `json:"line_number"`
	ItemCode        string `json:"item_code"`
	ItemName        string `json:"item_name"`
	UnitCode        string `json:"unit_code"`
	WHCode          string `json:"wh_code"`
	ShelfCode       string `json:"shelf_code"`
	Qty             string `json:"qty"`
	Price           string `json:"price"`
	PriceExcludeVAT string `json:"price_exclude_vat"`
	Discount        string `json:"discount"`
	Sum             string `json:"sum"`
	VAT             string `json:"vat"`
	BeforeVAT       string `json:"before_vat"`
}

func canonicalProfileHash(tenant string, p docPayload, items []docItem, route docRoute) (string, error) {
	ordered := append([]docItem(nil), items...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].LineNumber != ordered[j].LineNumber {
			return ordered[i].LineNumber < ordered[j].LineNumber
		}
		if ordered[i].ItemCode != ordered[j].ItemCode {
			return ordered[i].ItemCode < ordered[j].ItemCode
		}
		if ordered[i].UnitCode != ordered[j].UnitCode {
			return ordered[i].UnitCode < ordered[j].UnitCode
		}
		if ordered[i].WHCode != ordered[j].WHCode {
			return ordered[i].WHCode < ordered[j].WHCode
		}
		return ordered[i].ShelfCode < ordered[j].ShelfCode
	})
	canonicalItems := make([]canonicalProfileItem, 0, len(ordered))
	for _, item := range ordered {
		canonicalItems = append(canonicalItems, canonicalProfileItem{
			LineNumber: item.LineNumber, ItemCode: strings.TrimSpace(item.ItemCode), ItemName: item.ItemName,
			UnitCode: strings.TrimSpace(item.UnitCode), WHCode: firstNonEmpty(item.WHCode, p.WHCode),
			ShelfCode: firstNonEmpty(item.ShelfCode, p.ShelfCode), Qty: item.QtyDecimal,
			Price: item.PriceDecimal, PriceExcludeVAT: item.PriceExcludeVATDecimal,
			Discount: item.DiscountAmountDecimal, Sum: item.SumAmountDecimal,
			VAT: item.VATAmountDecimal, BeforeVAT: item.SumAmountExclVATDecimal,
		})
	}
	canonical := struct {
		Version               string                 `json:"version"`
		Tenant                string                 `json:"tenant"`
		TransFlag             int                    `json:"trans_flag"`
		DocNo                 string                 `json:"doc_no"`
		DocDate               string                 `json:"doc_date"`
		DocTime               string                 `json:"doc_time"`
		DocFormatCode         string                 `json:"doc_format_code"`
		CustCode              string                 `json:"cust_code"`
		BranchCode            string                 `json:"branch_code"`
		SaleCode              string                 `json:"sale_code"`
		WHCode                string                 `json:"wh_code"`
		ShelfCode             string                 `json:"shelf_code"`
		VATType               int                    `json:"vat_type"`
		VATRate               string                 `json:"vat_rate"`
		Totals                []string               `json:"totals"`
		InquiryType           int                    `json:"inquiry_type"`
		Remark                string                 `json:"remark"`
		Remark2               string                 `json:"remark_2"`
		Remark5               string                 `json:"remark_5"`
		ShipmentApplicability string                 `json:"shipment_applicability"`
		Shipment              *docShipment           `json:"shipment"`
		Items                 []canonicalProfileItem `json:"items"`
	}{
		Version: documentProfileV1, Tenant: strings.TrimSpace(tenant), TransFlag: route.transFlag,
		DocNo: p.DocNo, DocDate: p.DocDate, DocTime: p.DocTime, DocFormatCode: p.DocFormatCode,
		CustCode: p.CustCode, BranchCode: p.BranchCode, SaleCode: p.SaleCode,
		WHCode: p.WHCode, ShelfCode: p.ShelfCode, VATType: p.VATType, VATRate: p.VATRateDecimal,
		Totals: []string{p.TotalValueDecimal, p.TotalDiscountDecimal, p.TotalBeforeVATDecimal,
			p.TotalVATValueDecimal, p.TotalExceptVATDecimal, p.TotalAfterVATDecimal, p.TotalAmountDecimal},
		InquiryType: p.InquiryType, Remark: p.Remark, Remark2: p.Remark2, Remark5: p.Remark5,
		ShipmentApplicability: p.ShipmentApplicability, Shipment: p.Shipment, Items: canonicalItems,
	}
	body, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func writeProfileRelations(ctx context.Context, tx pgx.Tx, p docPayload, route docRoute, payloadHash string) (int, error) {
	rows := 0
	docDate, _ := time.Parse("2006-01-02", p.DocDate)
	if saleVATRegisterApplicable(p, route) {
		vatEffectivePeriod, vatEffectiveYear := smlVATEffectivePeriod(docDate)
		tag, err := tx.Exec(ctx, `INSERT INTO gl_journal_vat_sale (
			doc_date,doc_no,line_number,vat_number,base_caltax_amount,tax_rate,amount,
			vat_date,trans_type,trans_flag,ar_code,vat_calc,vat_effective_period,vat_effective_year,
			branch_code,vat_type
		) SELECT $1,$2,0,$3,$4,$5,$6,$7,$8,$9,$10,1,$11,$12,$13,$14
		WHERE NOT EXISTS (
			SELECT 1 FROM gl_journal_vat_sale
			 WHERE doc_no=$15 AND trans_flag=$16 AND line_number=0
		)`,
			docDate, p.DocNo, p.DocNo, p.TotalBeforeVATDecimal, p.VATRateDecimal,
			p.TotalVATValueDecimal, docDate, route.transType, route.transFlag, p.CustCode,
			vatEffectivePeriod, vatEffectiveYear, p.BranchCode, smlSaleVATRegisterType,
			p.DocNo, route.transFlag)
		if err != nil {
			return rows, fmt.Errorf("insert VAT profile: %w", err)
		}
		rows += int(tag.RowsAffected())
	}
	if p.ShipmentApplicability == "required" {
		tag, err := tx.Exec(ctx, `INSERT INTO ic_trans_shipment (
			doc_no,doc_date,trans_flag,cust_code,transport_name,transport_address,transport_telephone,
			remark,remark_2
		) SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9
		WHERE NOT EXISTS (
			SELECT 1 FROM ic_trans_shipment WHERE doc_no=$10 AND trans_flag=$11
		)`,
			p.DocNo, docDate, route.transFlag, p.CustCode, p.Shipment.TransportName,
			p.Shipment.TransportAddress, p.Shipment.TransportTelephone, p.Remark, p.Remark2,
			p.DocNo, route.transFlag)
		if err != nil {
			return rows, fmt.Errorf("insert shipment profile: %w", err)
		}
		rows += int(tag.RowsAffected())
	}
	guid, err := newProfileLogGUID()
	if err != nil {
		return rows, err
	}
	docQty := "0"
	qty := new(big.Rat)
	for _, item := range p.Details {
		value, _ := new(big.Rat).SetString(item.QtyDecimal)
		if value != nil {
			qty.Add(qty, value)
		}
	}
	docQty = qty.FloatString(6)
	profileMarker := "NEXFLOW_PROFILE_V1:" + payloadHash
	tag, err := tx.Exec(ctx, `INSERT INTO logs (
		function_code,data1,data2,user_code,date_time,screen_code,guid,doc_date,doc_no,
		doc_amount,function_type,computer_name,menu_name,doc_qty
	) SELECT 1,$1,$2,'BILLFLOW',NOW(),$3,$4,$5,$6,$7,2,'NEXFLOW','menu_so_invoice',$8
	WHERE NOT EXISTS (
		SELECT 1 FROM logs WHERE doc_no=$9 AND screen_code=$10
		  AND data2=$11 AND function_code=1
	)`,
		buildMainLogData1(p), profileMarker, route.transFlag, guid, p.DocDate, p.DocNo,
		p.TotalAmountDecimal, docQty, p.DocNo, route.transFlag, profileMarker)
	if err != nil {
		return rows, fmt.Errorf("insert main SML log profile: %w", err)
	}
	return rows + int(tag.RowsAffected()), nil
}

func saleVATRegisterApplicable(p docPayload, route docRoute) bool {
	return p.DocumentProfileVersion == documentProfileV1 && route.name == routeSaleInvoice.name && p.VATType >= 0 && p.VATType <= 2
}

func storedProfileHash(ctx context.Context, tx pgx.Tx, docNo string, transFlag int) (string, error) {
	var marker string
	err := tx.QueryRow(ctx, `SELECT COALESCE((
		SELECT data2 FROM logs
		 WHERE doc_no=$1 AND screen_code=$2 AND function_code=1
		   AND data2 LIKE 'NEXFLOW_PROFILE_V1:%'
		 ORDER BY roworder DESC LIMIT 1
	), '')`, docNo, transFlag).Scan(&marker)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(marker, "NEXFLOW_PROFILE_V1:"), nil
}

func validateStoredProfileHash(stored, requested, docNo string) error {
	if stored == "" || stored == requested {
		return nil
	}
	return newAppError(http.StatusConflict, "doc_no_payload_mismatch",
		"doc_no already exists with a different canonical payload hash", gin.H{"doc_no": docNo})
}

func buildMainLogData1(p docPayload) string {
	docDate, _ := time.Parse("2006-01-02", p.DocDate)
	thaiDate := fmt.Sprintf("%d/%d/%d", docDate.Day(), int(docDate.Month()), docDate.Year()+543)
	xml := `<?xml version="1.0" encoding="utf-8"?><top>` +
		`<d t=2 f=doc_date>` + thaiDate + `</d>` +
		`<d t=1 f=doc_time>` + html.EscapeString(p.DocTime) + `</d>` +
		`<d t=1 f=doc_no>` + html.EscapeString(p.DocNo) + `</d>` +
		`<d t=1 f=doc_format_code>` + html.EscapeString(p.DocFormatCode) + `</d>` +
		`<d t=1 f=cust_code>` + html.EscapeString(p.CustCode) + `</d>` +
		`<d t=1 f=contactor></d>` +
		`<d t=2 f=tax_doc_date>` + thaiDate + `</d>` +
		`<d t=1 f=tax_doc_no>` + html.EscapeString(p.DocNo) + `</d>` +
		`<d t=1 f=doc_ref>` + html.EscapeString(p.DocRef) + `</d>` +
		`<d t=2 f=doc_ref_date></d>` +
		`<d t=5 f=inquiry_type>` + strconv.Itoa(p.InquiryType) + `</d>` +
		`<d t=5 f=vat_type>` + strconv.Itoa(p.VATType) + `</d>` +
		`<d t=1 f=sale_code>` + html.EscapeString(p.SaleCode) + `</d>` +
		`<d t=1 f=sale_group></d></top>`
	return html.EscapeString(xml)
}

func newProfileLogGUID() (string, error) {
	guid, err := newRefGUID()
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(guid, "-", ""), nil
}

func profileDecimalArg(version, exact string, numeric float64) any {
	if version == documentProfileV1 {
		return exact
	}
	return numeric
}

func ensurePreparedProfileDecimals(items []preparedDocItem) {
	for i := range items {
		item := &items[i].docItem
		for _, binding := range []struct {
			exact *string
			value float64
		}{
			{&item.QtyDecimal, item.Qty}, {&item.PriceDecimal, item.Price},
			{&item.PriceExcludeVATDecimal, item.PriceExcludeVAT},
			{&item.DiscountAmountDecimal, item.DiscountAmount},
			{&item.SumAmountDecimal, item.SumAmount}, {&item.VATAmountDecimal, item.TotalVATValue},
			{&item.SumAmountExclVATDecimal, item.SumAmountExclVAT},
		} {
			if *binding.exact == "" {
				*binding.exact = strconv.FormatFloat(binding.value, 'f', -1, 64)
			}
		}
	}
}

func buildERPLogDataNew(p docPayload) ([]byte, error) {
	docDate, err := time.Parse("2006-01-02", p.DocDate)
	if err != nil {
		return nil, fmt.Errorf("parse doc_date for SML VAT audit: %w", err)
	}
	vatEffectivePeriod, vatEffectiveYear := smlVATEffectivePeriod(docDate)
	details := make([]map[string]any, 0, len(p.Details))
	for _, item := range p.Details {
		details = append(details, map[string]any{
			"barcode": "", "date_expire": nil, "discount": item.DiscountAmountDecimal,
			"discount_amount": item.DiscountAmountDecimal, "divide_value": "1", "doc_ref_type": 0,
			"hidden_cost_1": "0", "hidden_cost_1_exclude_vat": "0", "is_get_price": item.IsGetPrice,
			"is_lock_cost": 0, "is_permium": item.IsPremium, "is_serial_number": 0,
			"item_code": item.ItemCode, "item_code_main": "", "item_name": item.ItemName,
			"item_type": 0, "line_number": item.LineNumber, "lot_number_1": "", "mfd_date": nil,
			"mfn_name": "", "price": item.PriceDecimal, "price_exclude_vat": item.PriceExcludeVATDecimal,
			"price_guid": "", "price_mode": 0, "price_set_ratio": "0", "price_type": 0,
			"qty": item.QtyDecimal, "ref_doc_no": "", "ref_guid": "", "ref_row": 0,
			"remark": "", "set_ref_line": "", "set_ref_price": "0", "set_ref_qty": "0",
			"shelf_code": firstNonEmpty(item.ShelfCode, p.ShelfCode), "stand_value": "1",
			"sum_amount": item.SumAmountDecimal, "sum_amount_exclude_vat": item.SumAmountExclVATDecimal,
			"sum_of_cost_fix": "0", "tax_type": item.TaxType, "total_vat_value": item.VATAmountDecimal,
			"unit_code": item.UnitCode, "user_approve": "", "wh_code": firstNonEmpty(item.WHCode, p.WHCode),
		})
	}
	shipment := map[string]any{
		"destination": "", "latitude": "0", "logistic_area": "", "longitude": "0", "ship_code": "",
		"transport_address": "", "transport_amper": "", "transport_code": "", "transport_country": "",
		"transport_fax": "", "transport_name": "", "transport_province": "", "transport_tambon": "",
		"transport_telephone": "", "zipcode": "",
	}
	if p.Shipment != nil {
		shipment["transport_name"] = p.Shipment.TransportName
		shipment["transport_address"] = p.Shipment.TransportAddress
		shipment["transport_telephone"] = p.Shipment.TransportTelephone
	}
	vatRows := []map[string]any{}
	if saleVATRegisterApplicable(p, routeSaleInvoice) {
		vatRows = append(vatRows, map[string]any{
			"amount": p.TotalVATValueDecimal, "ar_name": "", "base_caltax_amount": p.TotalBeforeVATDecimal,
			"branch_code": p.BranchCode, "branch_type": 0, "description": "", "except_tax_amount": "0",
			"is_add": 0, "line_number": 0, "manual_add": 0, "ref_doc_date": nil, "ref_doc_no": "",
			"ref_vat_date": nil, "ref_vat_no": "", "tax_group": "", "tax_no": "",
			"tax_rate": p.VATRateDecimal, "vat_date": p.DocDate, "vat_effective_period": vatEffectivePeriod,
			"vat_effective_year": vatEffectiveYear, "vat_number": p.DocNo, "vat_type": smlSaleVATRegisterType,
		})
	}
	data := map[string]any{
		"screenbottom":         map[string]any{"cashier_code": p.CashierCode, "remark_2": p.Remark2, "remark_3": "", "remark_4": "", "remark_5": p.Remark5, "user_approve": ""},
		"screendetail":         details,
		"screengldetail":       []any{},
		"screengltop":          map[string]any{"account_year": 0, "ap_ar_code": p.CustCode, "ap_ar_originate_from": 0, "book_code": "", "description": p.Remark, "doc_date": p.DocDate, "doc_format_code": p.DocFormatCode, "doc_no": p.DocNo, "journal_type": 0, "period_number": 0, "ref_date": nil, "ref_no": p.DocRef, "trans_direct": 0},
		"screenmore":           map[string]any{"advance_amount": "0", "credit_date": p.DocDate, "credit_day": 0, "discount_word": headerDiscountWord(p.TotalDiscount), "remark": p.Remark, "send_date": p.DocDate, "send_day": 0, "send_type": 0, "total_after_vat": p.TotalAfterVATDecimal, "total_amount": p.TotalAmountDecimal, "total_before_vat": p.TotalBeforeVATDecimal, "total_discount": p.TotalDiscountDecimal, "total_except_vat": p.TotalExceptVATDecimal, "total_value": p.TotalValueDecimal, "total_vat_value": p.TotalVATValueDecimal, "vat_rate": p.VATRateDecimal},
		"screenpay":            map[string]any{"expenseothers": []any{}, "incomeothers": []any{}, "payCheques": []any{}, "paycoupons": []any{}, "paycreditcards": []any{}, "paydeposits": []any{}, "paydetail": []any{}, "paypettycashs": []any{}, "paytransfers": []any{}},
		"screenpaydeposit":     []any{},
		"screenshipment":       shipment,
		"screentop":            map[string]any{"contactor": "", "cust_code": p.CustCode, "doc_date": p.DocDate, "doc_format_code": p.DocFormatCode, "doc_no": p.DocNo, "doc_ref": p.DocRef, "doc_ref_date": p.DocRefDate, "doc_time": p.DocTime, "inquiry_type": p.InquiryType, "sale_code": p.SaleCode, "sale_group": "", "tax_doc_date": p.DocDate, "tax_doc_no": p.DocNo, "vat_type": p.VATType},
		"screenvatsale":        vatRows,
		"screenwithholdingtax": map[string]any{},
	}
	return json.Marshal(data)
}
