package handlers

import "testing"

func TestNormalizeCompanyProfilePrefersCompanyName(t *testing.T) {
	got := normalizeCompanyProfile(CompanyProfile{
		CompanyName1:  "  บริษัท ทดสอบ จำกัด  ",
		BusinessName1: "ชื่อสำรอง",
		Address1:      "  กรุงเทพมหานคร ",
	})
	if got.DisplayName != "บริษัท ทดสอบ จำกัด" {
		t.Fatalf("DisplayName = %q", got.DisplayName)
	}
	if got.Address1 != "กรุงเทพมหานคร" {
		t.Fatalf("Address1 = %q", got.Address1)
	}
}

func TestNormalizeCompanyProfileFallsBackToBusinessName(t *testing.T) {
	got := normalizeCompanyProfile(CompanyProfile{BusinessName1: "  ร้านตัวอย่าง "})
	if got.DisplayName != "ร้านตัวอย่าง" {
		t.Fatalf("DisplayName = %q", got.DisplayName)
	}
}
