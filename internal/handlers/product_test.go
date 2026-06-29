package handlers

import (
	"strings"
	"testing"
)

func TestProductImageDatabaseName(t *testing.T) {
	got := productImageDatabaseName(" SML1_2026 ")
	if got != "sml1_2026_images" {
		t.Fatalf("image db = %q, want sml1_2026_images", got)
	}
}

func TestSniffImageContentType(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "jpeg", data: []byte{0xff, 0xd8, 0xff, 0xe0}, want: "image/jpeg"},
		{name: "png", data: []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}, want: "image/png"},
		{name: "gif", data: []byte("GIF89a..."), want: "image/gif"},
		{name: "webp", data: []byte("RIFFxxxxWEBP"), want: "image/webp"},
		{name: "unknown", data: []byte("not-an-image"), want: "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sniffImageContentType(tt.data); got != tt.want {
				t.Fatalf("content type = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProductImageMetadataQueryDoesNotSelectBinaryPayload(t *testing.T) {
	sql := strings.ToLower(productImageMetadataSQL)
	if strings.Contains(sql, "select image_file") {
		t.Fatal("metadata query must not select raw image_file")
	}
	if !strings.Contains(sql, "octet_length(image_file)") {
		t.Fatal("metadata query should only use image_file for byte length metadata")
	}
	if !strings.Contains(sql, "trim(image_id)") {
		t.Fatal("metadata query must trim image_id before matching product codes")
	}
}

func TestProductImageListQueryDoesNotSelectBinaryPayload(t *testing.T) {
	sql := strings.ToLower(productImageListSQL)
	if strings.Contains(sql, "select image_file") {
		t.Fatal("image list query must not select raw image_file")
	}
	if !strings.Contains(sql, "octet_length(image_file)") {
		t.Fatal("image list query should only use image_file for byte length metadata")
	}
	if !strings.Contains(sql, "trim(image_id)") {
		t.Fatal("image list query must trim image_id before matching product codes")
	}
	if !strings.Contains(sql, "order by coalesce(image_order, 0), roworder") {
		t.Fatal("image list query must preserve SML image order")
	}
}

func TestUnitListQueryReadsOnlyActiveUnitMaster(t *testing.T) {
	sql := strings.ToLower(unitListSQL)
	if strings.Contains(sql, "select *") {
		t.Fatal("unit list query must not use SELECT *")
	}
	if !strings.Contains(sql, "from public.ic_unit") {
		t.Fatal("unit list query must read ic_unit")
	}
	if !strings.Contains(sql, "coalesce(status, 0) = 0") {
		t.Fatal("unit list query must filter inactive units")
	}
	if !strings.Contains(sql, "limit @size") {
		t.Fatal("unit list query must enforce a limit")
	}
}

func TestProductUnitsQueryReadsProductUnitTableAndUnitNames(t *testing.T) {
	sql := strings.ToLower(productUnitsSQL)
	if strings.Contains(sql, "select *") {
		t.Fatal("product units query must not use SELECT *")
	}
	if !strings.Contains(sql, "from public.ic_unit_use") {
		t.Fatal("product units query must read ic_unit_use")
	}
	if !strings.Contains(sql, "left join public.ic_unit") {
		t.Fatal("product units query must join ic_unit for display names")
	}
	if !strings.Contains(sql, "where uu.ic_code = $1") {
		t.Fatal("product units query must scope rows to the requested product code")
	}
	if !strings.Contains(sql, "coalesce(uu.status, 0) = 0") {
		t.Fatal("product units query must filter inactive product-unit rows")
	}
}

func TestFirstNonEmptyTrimsValues(t *testing.T) {
	if got := firstNonEmpty("", "  ", " ชิ้น ", "ถุง"); got != "ชิ้น" {
		t.Fatalf("firstNonEmpty = %q, want ชิ้น", got)
	}
}
