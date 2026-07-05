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
