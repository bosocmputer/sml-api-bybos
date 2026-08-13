package setproducts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"sort"
	"strconv"
)

type Component struct {
	LineNumber int     `json:"line_number"`
	RowOrder   int     `json:"row_order"`
	ItemCode   string  `json:"item_code"`
	ItemName   string  `json:"item_name"`
	ItemType   int     `json:"item_type"`
	UnitCode   string  `json:"unit_code"`
	Qty        float64 `json:"qty"`
	Price      float64 `json:"price"`
	SumAmount  float64 `json:"sum_amount"`
	PriceRatio float64 `json:"price_ratio"`
	UnitFactor float64 `json:"unit_factor"`
	Active     bool    `json:"active"`
	UnitValid  bool    `json:"unit_valid"`
}

type Definition struct {
	ItemCode       string      `json:"item_code"`
	ComponentCount int         `json:"component_count"`
	DocumentValid  bool        `json:"document_valid"`
	StockValid     bool        `json:"stock_valid"`
	WarningCodes   []string    `json:"warning_codes"`
	WeightMethod   string      `json:"weight_method"`
	Hash           string      `json:"hash"`
	Components     []Component `json:"components"`
}

func BuildDefinition(itemCode string, components []Component) Definition {
	ordered := append([]Component(nil), components...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].LineNumber != ordered[j].LineNumber {
			return ordered[i].LineNumber < ordered[j].LineNumber
		}
		if ordered[i].RowOrder != ordered[j].RowOrder {
			return ordered[i].RowOrder < ordered[j].RowOrder
		}
		return ordered[i].ItemCode < ordered[j].ItemCode
	})
	warnings := []string{}
	documentValid := true
	stockValid := true
	if len(ordered) == 0 {
		warnings = append(warnings, "set_definition_missing")
		documentValid = false
		stockValid = false
	}
	for _, component := range ordered {
		switch {
		case component.ItemType == 3:
			warnings = appendUnique(warnings, "nested_set_not_supported")
			documentValid = false
			stockValid = false
		case !component.Active:
			warnings = appendUnique(warnings, "set_component_inactive")
			documentValid = false
			stockValid = false
		}
		if component.ItemType != 0 {
			warnings = appendUnique(warnings, "set_component_not_stock_item")
			stockValid = false
		}
		if component.Qty <= 0 || math.IsNaN(component.Qty) || math.IsInf(component.Qty, 0) {
			warnings = appendUnique(warnings, "set_component_qty_invalid")
			documentValid = false
			stockValid = false
		}
		if !component.UnitValid || component.UnitFactor < 1 || math.IsNaN(component.UnitFactor) || math.IsInf(component.UnitFactor, 0) {
			warnings = appendUnique(warnings, "set_component_unit_invalid")
			documentValid = false
			stockValid = false
		}
	}
	method := WeightMethod(ordered)
	if method == "" && len(ordered) > 0 {
		warnings = appendUnique(warnings, "set_allocation_invalid")
		documentValid = false
		stockValid = false
	}
	encoded, _ := json.Marshal(struct {
		ItemCode   string      `json:"item_code"`
		Components []Component `json:"components"`
	}{ItemCode: itemCode, Components: ordered})
	sum := sha256.Sum256(encoded)
	return Definition{
		ItemCode: itemCode, ComponentCount: len(ordered), DocumentValid: documentValid,
		StockValid: stockValid, WarningCodes: warnings, WeightMethod: method,
		Hash: hex.EncodeToString(sum[:]), Components: ordered,
	}
}

func WeightMethod(components []Component) string {
	if allPositive(components, func(c Component) float64 { return c.SumAmount }) {
		return "sum_amount"
	}
	if allPositive(components, func(c Component) float64 { return c.Qty * c.Price }) {
		return "qty_price"
	}
	if allPositive(components, func(c Component) float64 { return c.Qty * c.PriceRatio }) {
		return "qty_price_ratio"
	}
	return ""
}

func AllocateCents(total int64, components []Component) ([]int64, error) {
	method := WeightMethod(components)
	if method == "" {
		return nil, errors.New("set allocation weights are invalid")
	}
	weights := make([]*big.Rat, len(components))
	weightTotal := new(big.Rat)
	for i, component := range components {
		switch method {
		case "sum_amount":
			weights[i] = decimalRat(component.SumAmount)
		case "qty_price":
			weights[i] = new(big.Rat).Mul(decimalRat(component.Qty), decimalRat(component.Price))
		default:
			weights[i] = new(big.Rat).Mul(decimalRat(component.Qty), decimalRat(component.PriceRatio))
		}
		if weights[i] == nil || weights[i].Sign() <= 0 {
			return nil, errors.New("set allocation weights are invalid")
		}
		weightTotal.Add(weightTotal, weights[i])
	}
	result := make([]int64, len(components))
	var allocated int64
	for i := range components {
		if i == len(components)-1 {
			result[i] = total - allocated
			break
		}
		share := new(big.Rat).Mul(new(big.Rat).SetInt64(total), weights[i])
		share.Quo(share, weightTotal)
		value, ok := roundRatToInt64(share)
		if !ok {
			return nil, errors.New("set allocation exceeds supported money range")
		}
		result[i] = value
		allocated += result[i]
	}
	return result, nil
}

func decimalRat(value float64) *big.Rat {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	valueText := strconv.FormatFloat(value, 'f', -1, 64)
	result, ok := new(big.Rat).SetString(valueText)
	if !ok {
		return nil
	}
	return result
}

func roundRatToInt64(value *big.Rat) (int64, bool) {
	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(value.Num(), value.Denom(), remainder)
	twiceRemainder := new(big.Int).Lsh(new(big.Int).Abs(remainder), 1)
	if twiceRemainder.Cmp(value.Denom()) >= 0 {
		if value.Sign() < 0 {
			quotient.Sub(quotient, big.NewInt(1))
		} else {
			quotient.Add(quotient, big.NewInt(1))
		}
	}
	return quotient.Int64(), quotient.IsInt64()
}

func MoneyToCents(value float64) int64 { return int64(math.Round(value * 100)) }
func CentsToMoney(value int64) float64 { return float64(value) / 100 }

func HasWarning(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func allPositive(components []Component, value func(Component) float64) bool {
	if len(components) == 0 {
		return false
	}
	for _, component := range components {
		v := value(component)
		if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

func appendUnique(values []string, value string) []string {
	if !HasWarning(values, value) {
		return append(values, value)
	}
	return values
}
