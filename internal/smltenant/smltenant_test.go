package smltenant

import (
	"strings"
	"testing"
)

func TestImageDatabaseName(t *testing.T) {
	got := ImageDatabaseName(" STPT ")
	if got != "stpt_images" {
		t.Fatalf("image db = %q, want stpt_images", got)
	}
}

func TestBuildProvisionStatements(t *testing.T) {
	varchar50 := 50
	varchar10 := 10
	varchar100 := 100
	schema := tableSchema{
		HasSequence: true,
		Columns: []columnSchema{
			{Ordinal: 1, Name: "roworder", DataType: "integer", UDTName: "int4", Nullable: false, Default: "nextval('sml_doc_images_roworder_seq'::regclass)"},
			{Ordinal: 2, Name: "image_id", DataType: "character varying", UDTName: "varchar", CharMax: &varchar50, Nullable: false, Default: "''::character varying"},
			{Ordinal: 3, Name: "image_file", DataType: "bytea", UDTName: "bytea", Nullable: true},
			{Ordinal: 4, Name: "system_id", DataType: "character varying", UDTName: "varchar", CharMax: &varchar10, Nullable: true, Default: "''::character varying"},
			{Ordinal: 5, Name: "guid_code", DataType: "character varying", UDTName: "varchar", CharMax: &varchar100, Nullable: true, Default: "''::character varying"},
			{Ordinal: 6, Name: "image_order", DataType: "smallint", UDTName: "int2", Nullable: true, Default: "0"},
		},
		Constraints: []constraintSchema{{Name: "sml_doc_images_pkey", Type: "p", Definition: "PRIMARY KEY (roworder)"}},
		Indexes: []indexSchema{
			{Name: "sml_doc_images_pkey", Definition: "CREATE UNIQUE INDEX sml_doc_images_pkey ON public.sml_doc_images USING btree (roworder)"},
			{Name: "sml_doc_images_roworder_idx", Definition: "CREATE INDEX sml_doc_images_roworder_idx ON public.sml_doc_images USING btree (roworder)"},
		},
	}

	statements, err := buildProvisionStatements(DatabaseInfo{
		Owner:     "postgres",
		Encoding:  "UTF8",
		Collation: "th_TH.UTF-8",
		CType:     "th_TH.UTF-8",
	}, "stpt_images", schema)
	if err != nil {
		t.Fatalf("build statements failed: %v", err)
	}
	joined := strings.Join(statements, "\n")
	for _, want := range []string{
		`CREATE DATABASE "stpt_images"`,
		`CREATE SEQUENCE public."sml_doc_images_roworder_seq"`,
		`"image_file" bytea`,
		`ADD CONSTRAINT "sml_doc_images_pkey" PRIMARY KEY (roworder)`,
		`CREATE INDEX sml_doc_images_roworder_idx`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("statements missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "CREATE UNIQUE INDEX sml_doc_images_pkey") {
		t.Fatalf("primary-key backing index should not be emitted separately:\n%s", joined)
	}
}

func TestColumnsEqualIgnoresPhysicalColumnOrder(t *testing.T) {
	varchar100 := 100
	want := []columnSchema{
		{Ordinal: 1, Name: "image_url", DataType: "character varying", UDTName: "varchar", CharMax: &varchar100, Nullable: true, Default: "''::character varying"},
		{Ordinal: 2, Name: "create_date_time_now", DataType: "timestamp without time zone", UDTName: "timestamp", Nullable: true, Default: "CURRENT_TIMESTAMP"},
	}
	got := []columnSchema{
		{Ordinal: 1, Name: "create_date_time_now", DataType: "timestamp without time zone", UDTName: "timestamp", Nullable: true, Default: "CURRENT_TIMESTAMP"},
		{Ordinal: 2, Name: "image_url", DataType: "character varying", UDTName: "varchar", CharMax: &varchar100, Nullable: true, Default: "''::character varying"},
	}

	if !columnsEqual(got, want) {
		t.Fatal("equivalent columns in a different physical order should match")
	}
}

func TestColumnsEqualStillRejectsSemanticDifferences(t *testing.T) {
	varchar100 := 100
	varchar50 := 50
	want := []columnSchema{{Name: "image_url", DataType: "character varying", UDTName: "varchar", CharMax: &varchar100, Nullable: true}}
	tests := []struct {
		name string
		got  []columnSchema
	}{
		{name: "missing column", got: nil},
		{name: "different length", got: []columnSchema{{Name: "image_url", DataType: "character varying", UDTName: "varchar", CharMax: &varchar50, Nullable: true}}},
		{name: "different nullability", got: []columnSchema{{Name: "image_url", DataType: "character varying", UDTName: "varchar", CharMax: &varchar100, Nullable: false}}},
		{name: "different type", got: []columnSchema{{Name: "image_url", DataType: "text", UDTName: "text", Nullable: true}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if columnsEqual(tc.got, want) {
				t.Fatal("semantically different columns should not match")
			}
		})
	}
}

func TestDiffColumnsMissingColumnsOnly(t *testing.T) {
	want := []columnSchema{
		{Name: "roworder", DataType: "integer", UDTName: "int4", Nullable: false},
		{Name: "image_url", DataType: "text", UDTName: "text", Nullable: true},
	}
	got := []columnSchema{
		{Name: "roworder", DataType: "integer", UDTName: "int4", Nullable: false},
	}

	diff := diffColumns(got, want)
	if !diff.MissingColumnsOnly() {
		t.Fatalf("expected missing-columns-only diff, got %+v", diff)
	}
	if len(diff.Missing) != 1 || diff.Missing[0].Name != "image_url" {
		t.Fatalf("expected only image_url missing, got %+v", diff.Missing)
	}
}

func TestDiffColumnsRejectsExtraColumn(t *testing.T) {
	want := []columnSchema{{Name: "roworder", DataType: "integer", UDTName: "int4", Nullable: false}}
	got := []columnSchema{
		{Name: "roworder", DataType: "integer", UDTName: "int4", Nullable: false},
		{Name: "legacy_col", DataType: "text", UDTName: "text", Nullable: true},
	}

	diff := diffColumns(got, want)
	if diff.MissingColumnsOnly() {
		t.Fatal("a live table with an extra column must not be treated as missing-columns-only")
	}
	if !diff.Extra {
		t.Fatal("expected Extra to be true")
	}
}

func TestDiffColumnsRejectsChangedColumn(t *testing.T) {
	want := []columnSchema{{Name: "image_url", DataType: "text", UDTName: "text", Nullable: true}}
	got := []columnSchema{{Name: "image_url", DataType: "character varying", UDTName: "varchar", Nullable: true}}

	diff := diffColumns(got, want)
	if diff.MissingColumnsOnly() {
		t.Fatal("a type-mismatched column must not be treated as missing-columns-only")
	}
	if !diff.Changed {
		t.Fatal("expected Changed to be true")
	}
}

func TestDiffColumnsNoDifference(t *testing.T) {
	cols := []columnSchema{{Name: "roworder", DataType: "integer", UDTName: "int4", Nullable: false}}

	diff := diffColumns(cols, cols)
	if diff.MissingColumnsOnly() {
		t.Fatal("identical schemas should not report a missing-columns-only diff")
	}
	if diff.Extra || diff.Changed || len(diff.Missing) != 0 {
		t.Fatalf("expected no diff at all, got %+v", diff)
	}
}
