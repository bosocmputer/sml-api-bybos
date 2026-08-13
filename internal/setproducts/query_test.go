package setproducts

import (
	"os"
	"strings"
	"testing"
)

func TestDefinitionQueryFallsBackToMatchingInventoryStandardUnit(t *testing.T) {
	content, err := os.ReadFile("query.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(content))
	for _, fragment := range []string{
		"component.unit_standard_stand_value",
		"component.unit_standard_divide_value",
		"component.unit_standard, '')) = trim(coalesce(sd.unit_code",
		"order by candidate.source_priority",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("set definition query is missing standard-unit fallback fragment %q", fragment)
		}
	}
}
