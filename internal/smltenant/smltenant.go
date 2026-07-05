package smltenant

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"sml-api-bybos/internal/config"
)

const (
	DocImagesTable    = "sml_doc_images"
	DocImagesSequence = "sml_doc_images_roworder_seq"
)

type CheckStatus string

const (
	CheckOK   CheckStatus = "ok"
	CheckFail CheckStatus = "fail"
)

type Check struct {
	Name    string      `json:"name"`
	Status  CheckStatus `json:"status"`
	Message string      `json:"message"`
}

type DatabaseInfo struct {
	Name      string `json:"name"`
	Owner     string `json:"owner,omitempty"`
	Encoding  string `json:"encoding,omitempty"`
	Collation string `json:"collation,omitempty"`
	CType     string `json:"ctype,omitempty"`
	Exists    bool   `json:"exists"`
}

type DocRows struct {
	Rows              int  `json:"rows"`
	RowsWithImageFile int  `json:"rowsWithImageFile"`
	JPEGMagicRows     int  `json:"jpegMagicRows"`
	MinOrder          *int `json:"minOrder,omitempty"`
	MaxOrder          *int `json:"maxOrder,omitempty"`
}

type VerifyReport struct {
	Tenant        string        `json:"tenant"`
	ImageDatabase string        `json:"imageDatabase"`
	Template      string        `json:"template"`
	OK            bool          `json:"ok"`
	Checks        []Check       `json:"checks"`
	MainDatabase  *DatabaseInfo `json:"mainDatabase,omitempty"`
	ImageDB       *DatabaseInfo `json:"imageDatabaseInfo,omitempty"`
	TemplateDB    *DatabaseInfo `json:"templateDatabase,omitempty"`
	MainRows      *DocRows      `json:"mainRows,omitempty"`
	ImageRows     *DocRows      `json:"imageRows,omitempty"`
}

type VerifyOptions struct {
	Tenant        string
	Template      string
	AdminDatabase string
	DocNo         string
}

type ProvisionOptions struct {
	Tenant        string
	Template      string
	AdminDatabase string
	Apply         bool
}

type ProvisionPlan struct {
	Tenant        string   `json:"tenant"`
	ImageDatabase string   `json:"imageDatabase"`
	Template      string   `json:"template"`
	Apply         bool     `json:"apply"`
	Statements    []string `json:"statements"`
}

type tableSchema struct {
	Columns     []columnSchema
	Constraints []constraintSchema
	Indexes     []indexSchema
	HasSequence bool
}

type columnSchema struct {
	Ordinal  int
	Name     string
	DataType string
	UDTName  string
	CharMax  *int
	Nullable bool
	Default  string
}

type constraintSchema struct {
	Name       string
	Type       string
	Definition string
}

type indexSchema struct {
	Name       string
	Definition string
}

func NormalizeTenant(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func ImageDatabaseName(tenant string) string {
	tenant = NormalizeTenant(tenant)
	if tenant == "" {
		return "_images"
	}
	return tenant + "_images"
}

func AllowedTenants(cfg *config.Config) []string {
	tenants := make([]string, 0, len(cfg.DB.AllowedTenants))
	for tenant := range cfg.DB.AllowedTenants {
		tenants = append(tenants, tenant)
	}
	sort.Strings(tenants)
	return tenants
}

func VerifyTenant(ctx context.Context, cfg *config.Config, opts VerifyOptions) (VerifyReport, error) {
	opts.Tenant = NormalizeTenant(opts.Tenant)
	opts.Template = NormalizeTenant(opts.Template)
	if opts.AdminDatabase == "" {
		opts.AdminDatabase = "postgres"
	}
	report := VerifyReport{
		Tenant:        opts.Tenant,
		ImageDatabase: ImageDatabaseName(opts.Tenant),
		Template:      opts.Template,
		OK:            true,
	}
	if opts.Tenant == "" {
		return report, errors.New("tenant is required")
	}
	if opts.Template == "" {
		return report, errors.New("template image database is required")
	}

	adminConn, err := pgx.Connect(ctx, cfg.DSN(opts.AdminDatabase))
	if err != nil {
		return report, fmt.Errorf("connect admin database: %w", err)
	}
	defer adminConn.Close(ctx)

	mainInfo, err := databaseInfo(ctx, adminConn, opts.Tenant)
	if err != nil {
		return report, err
	}
	imageInfo, err := databaseInfo(ctx, adminConn, report.ImageDatabase)
	if err != nil {
		return report, err
	}
	templateInfo, err := databaseInfo(ctx, adminConn, opts.Template)
	if err != nil {
		return report, err
	}
	report.MainDatabase = &mainInfo
	report.ImageDB = &imageInfo
	report.TemplateDB = &templateInfo
	report.addCheck("main_database", mainInfo.Exists, fmt.Sprintf("main database %s exists", opts.Tenant), fmt.Sprintf("main database %s is missing", opts.Tenant))
	report.addCheck("image_database", imageInfo.Exists, fmt.Sprintf("image database %s exists", report.ImageDatabase), fmt.Sprintf("image database %s is missing", report.ImageDatabase))
	report.addCheck("template_database", templateInfo.Exists, fmt.Sprintf("template database %s exists", opts.Template), fmt.Sprintf("template database %s is missing", opts.Template))
	if !mainInfo.Exists || !imageInfo.Exists || !templateInfo.Exists {
		report.finalize()
		return report, nil
	}

	templateConn, err := pgx.Connect(ctx, cfg.DSN(opts.Template))
	if err != nil {
		return report, fmt.Errorf("connect template database %s: %w", opts.Template, err)
	}
	defer templateConn.Close(ctx)
	templateSchema, err := loadDocImagesSchema(ctx, templateConn)
	if err != nil {
		return report, err
	}
	report.addCheck("template_sml_doc_images", templateSchema.hasTable(), "template sml_doc_images schema loaded", "template sml_doc_images table is missing")
	if !templateSchema.hasTable() {
		report.finalize()
		return report, nil
	}

	mainConn, err := pgx.Connect(ctx, cfg.DSN(opts.Tenant))
	if err != nil {
		return report, fmt.Errorf("connect tenant database %s: %w", opts.Tenant, err)
	}
	defer mainConn.Close(ctx)
	mainSchema, err := loadDocImagesSchema(ctx, mainConn)
	if err != nil {
		return report, err
	}
	report.addSchemaChecks("tenant_sml_doc_images", mainSchema, templateSchema)
	if opts.DocNo != "" {
		rows, err := loadDocRows(ctx, mainConn, opts.DocNo)
		if err != nil {
			return report, err
		}
		report.MainRows = &rows
	}

	imageConn, err := pgx.Connect(ctx, cfg.DSN(report.ImageDatabase))
	if err != nil {
		return report, fmt.Errorf("connect image database %s: %w", report.ImageDatabase, err)
	}
	defer imageConn.Close(ctx)
	imageSchema, err := loadDocImagesSchema(ctx, imageConn)
	if err != nil {
		return report, err
	}
	report.addSchemaChecks("image_sml_doc_images", imageSchema, templateSchema)
	if opts.DocNo != "" {
		rows, err := loadDocRows(ctx, imageConn, opts.DocNo)
		if err != nil {
			return report, err
		}
		report.ImageRows = &rows
	}

	report.finalize()
	return report, nil
}

func BuildProvisionPlan(ctx context.Context, cfg *config.Config, opts ProvisionOptions) (ProvisionPlan, error) {
	opts.Tenant = NormalizeTenant(opts.Tenant)
	opts.Template = NormalizeTenant(opts.Template)
	if opts.AdminDatabase == "" {
		opts.AdminDatabase = "postgres"
	}
	plan := ProvisionPlan{
		Tenant:        opts.Tenant,
		ImageDatabase: ImageDatabaseName(opts.Tenant),
		Template:      opts.Template,
		Apply:         opts.Apply,
	}
	if opts.Tenant == "" {
		return plan, errors.New("tenant is required")
	}
	if opts.Template == "" {
		return plan, errors.New("template image database is required")
	}

	adminConn, err := pgx.Connect(ctx, cfg.DSN(opts.AdminDatabase))
	if err != nil {
		return plan, fmt.Errorf("connect admin database: %w", err)
	}
	defer adminConn.Close(ctx)

	mainInfo, err := databaseInfo(ctx, adminConn, opts.Tenant)
	if err != nil {
		return plan, err
	}
	if !mainInfo.Exists {
		return plan, fmt.Errorf("main database %s is missing", opts.Tenant)
	}
	imageInfo, err := databaseInfo(ctx, adminConn, plan.ImageDatabase)
	if err != nil {
		return plan, err
	}
	if imageInfo.Exists {
		return plan, fmt.Errorf("image database %s already exists", plan.ImageDatabase)
	}
	templateInfo, err := databaseInfo(ctx, adminConn, opts.Template)
	if err != nil {
		return plan, err
	}
	if !templateInfo.Exists {
		return plan, fmt.Errorf("template database %s is missing", opts.Template)
	}

	templateConn, err := pgx.Connect(ctx, cfg.DSN(opts.Template))
	if err != nil {
		return plan, fmt.Errorf("connect template database %s: %w", opts.Template, err)
	}
	defer templateConn.Close(ctx)
	templateSchema, err := loadDocImagesSchema(ctx, templateConn)
	if err != nil {
		return plan, err
	}
	if !templateSchema.hasTable() {
		return plan, fmt.Errorf("template %s does not contain public.%s", opts.Template, DocImagesTable)
	}

	statements, err := buildProvisionStatements(mainInfo, plan.ImageDatabase, templateSchema)
	if err != nil {
		return plan, err
	}
	plan.Statements = statements
	if !opts.Apply {
		return plan, nil
	}

	if _, err := adminConn.Exec(ctx, statements[0]); err != nil {
		return plan, fmt.Errorf("create database %s: %w", plan.ImageDatabase, err)
	}
	targetConn, err := pgx.Connect(ctx, cfg.DSN(plan.ImageDatabase))
	if err != nil {
		return plan, fmt.Errorf("connect created image database %s: %w", plan.ImageDatabase, err)
	}
	defer targetConn.Close(ctx)

	tx, err := targetConn.Begin(ctx)
	if err != nil {
		return plan, fmt.Errorf("start schema transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, stmt := range statements[1:] {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return plan, fmt.Errorf("apply schema statement %q: %w", stmt, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return plan, fmt.Errorf("commit schema: %w", err)
	}
	return plan, nil
}

func WithDefaultTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 45*time.Second)
}

func (r *VerifyReport) addCheck(name string, ok bool, okMessage, failMessage string) {
	status := CheckOK
	message := okMessage
	if !ok {
		status = CheckFail
		message = failMessage
	}
	r.Checks = append(r.Checks, Check{Name: name, Status: status, Message: message})
}

func (r *VerifyReport) addSchemaChecks(prefix string, got, want tableSchema) {
	r.addCheck(prefix+"_table", got.hasTable(), "public."+DocImagesTable+" exists", "public."+DocImagesTable+" is missing")
	if !got.hasTable() {
		return
	}
	r.addCheck(prefix+"_columns", columnsEqual(got.Columns, want.Columns), "columns match template", "columns do not match template")
	r.addCheck(prefix+"_sequence", got.HasSequence == want.HasSequence, "roworder sequence matches template", "roworder sequence does not match template")
	r.addCheck(prefix+"_constraints", constraintsEqual(got.Constraints, want.Constraints), "constraints match template", "constraints do not match template")
	r.addCheck(prefix+"_indexes", indexesEqual(got.Indexes, want.Indexes), "indexes match template", "indexes do not match template")
}

func (r *VerifyReport) finalize() {
	r.OK = true
	for _, check := range r.Checks {
		if check.Status != CheckOK {
			r.OK = false
			return
		}
	}
}

func databaseInfo(ctx context.Context, conn *pgx.Conn, name string) (DatabaseInfo, error) {
	var info DatabaseInfo
	info.Name = name
	err := conn.QueryRow(ctx, `
SELECT datname, pg_get_userbyid(datdba), pg_encoding_to_char(encoding), datcollate, datctype, true
FROM pg_database
WHERE datname = $1
`, name).Scan(&info.Name, &info.Owner, &info.Encoding, &info.Collation, &info.CType, &info.Exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return DatabaseInfo{Name: name, Exists: false}, nil
	}
	return info, err
}

func loadDocImagesSchema(ctx context.Context, conn *pgx.Conn) (tableSchema, error) {
	var schema tableSchema
	rows, err := conn.Query(ctx, `
SELECT ordinal_position, column_name, data_type, udt_name, character_maximum_length, is_nullable, COALESCE(column_default, '')
FROM information_schema.columns
WHERE table_schema = 'public' AND table_name = $1
ORDER BY ordinal_position
`, DocImagesTable)
	if err != nil {
		return schema, err
	}
	defer rows.Close()
	for rows.Next() {
		var col columnSchema
		var nullable string
		if err := rows.Scan(&col.Ordinal, &col.Name, &col.DataType, &col.UDTName, &col.CharMax, &nullable, &col.Default); err != nil {
			return schema, err
		}
		col.Nullable = nullable == "YES"
		schema.Columns = append(schema.Columns, col)
	}
	if err := rows.Err(); err != nil {
		return schema, err
	}
	if !schema.hasTable() {
		return schema, nil
	}

	if err := conn.QueryRow(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = 'public' AND c.relkind = 'S' AND c.relname = $1
)
`, DocImagesSequence).Scan(&schema.HasSequence); err != nil {
		return schema, err
	}

	constraintRows, err := conn.Query(ctx, `
SELECT c.conname, c.contype::text, pg_get_constraintdef(c.oid)
FROM pg_constraint c
JOIN pg_class t ON t.oid = c.conrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE n.nspname = 'public' AND t.relname = $1
ORDER BY c.conname
`, DocImagesTable)
	if err != nil {
		return schema, err
	}
	defer constraintRows.Close()
	for constraintRows.Next() {
		var item constraintSchema
		if err := constraintRows.Scan(&item.Name, &item.Type, &item.Definition); err != nil {
			return schema, err
		}
		schema.Constraints = append(schema.Constraints, item)
	}
	if err := constraintRows.Err(); err != nil {
		return schema, err
	}

	indexRows, err := conn.Query(ctx, `
SELECT indexname, indexdef
FROM pg_indexes
WHERE schemaname = 'public' AND tablename = $1
ORDER BY indexname
`, DocImagesTable)
	if err != nil {
		return schema, err
	}
	defer indexRows.Close()
	for indexRows.Next() {
		var item indexSchema
		if err := indexRows.Scan(&item.Name, &item.Definition); err != nil {
			return schema, err
		}
		schema.Indexes = append(schema.Indexes, item)
	}
	return schema, indexRows.Err()
}

func loadDocRows(ctx context.Context, conn *pgx.Conn, docNo string) (DocRows, error) {
	var rows DocRows
	var minOrder, maxOrder *int
	err := conn.QueryRow(ctx, `
SELECT
  COUNT(*)::int,
  COUNT(*) FILTER (WHERE image_file IS NOT NULL AND octet_length(image_file) > 0)::int,
  COUNT(*) FILTER (WHERE image_file IS NOT NULL AND substring(image_file from 1 for 3) = decode('ffd8ff', 'hex'))::int,
  MIN(image_order)::int,
  MAX(image_order)::int
FROM public.sml_doc_images
WHERE TRIM(image_id) = $1
`, strings.TrimSpace(docNo)).Scan(&rows.Rows, &rows.RowsWithImageFile, &rows.JPEGMagicRows, &minOrder, &maxOrder)
	if err != nil {
		return rows, err
	}
	rows.MinOrder = minOrder
	rows.MaxOrder = maxOrder
	return rows, nil
}

func (s tableSchema) hasTable() bool {
	return len(s.Columns) > 0
}

func columnsEqual(a, b []columnSchema) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Ordinal != b[i].Ordinal ||
			a[i].Name != b[i].Name ||
			a[i].DataType != b[i].DataType ||
			a[i].UDTName != b[i].UDTName ||
			a[i].Nullable != b[i].Nullable ||
			a[i].Default != b[i].Default {
			return false
		}
		if (a[i].CharMax == nil) != (b[i].CharMax == nil) {
			return false
		}
		if a[i].CharMax != nil && b[i].CharMax != nil && *a[i].CharMax != *b[i].CharMax {
			return false
		}
	}
	return true
}

func constraintsEqual(a, b []constraintSchema) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func indexesEqual(a, b []indexSchema) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func buildProvisionStatements(mainInfo DatabaseInfo, imageDatabase string, schema tableSchema) ([]string, error) {
	if !schema.HasSequence {
		return nil, fmt.Errorf("template schema has no %s sequence", DocImagesSequence)
	}
	statements := []string{
		fmt.Sprintf(
			"CREATE DATABASE %s WITH OWNER %s ENCODING %s LC_COLLATE %s LC_CTYPE %s TEMPLATE template0",
			quoteIdent(imageDatabase),
			quoteIdent(mainInfo.Owner),
			quoteLiteral(mainInfo.Encoding),
			quoteLiteral(mainInfo.Collation),
			quoteLiteral(mainInfo.CType),
		),
		fmt.Sprintf("CREATE SEQUENCE public.%s", quoteIdent(DocImagesSequence)),
	}

	columnDefs := make([]string, 0, len(schema.Columns))
	for _, col := range schema.Columns {
		def, err := columnDefinition(col)
		if err != nil {
			return nil, err
		}
		columnDefs = append(columnDefs, "    "+def)
	}
	statements = append(statements, fmt.Sprintf("CREATE TABLE public.%s (\n%s\n)", quoteIdent(DocImagesTable), strings.Join(columnDefs, ",\n")))
	statements = append(statements, fmt.Sprintf("ALTER SEQUENCE public.%s OWNED BY public.%s.%s", quoteIdent(DocImagesSequence), quoteIdent(DocImagesTable), quoteIdent("roworder")))
	for _, constraint := range schema.Constraints {
		statements = append(statements, fmt.Sprintf("ALTER TABLE ONLY public.%s ADD CONSTRAINT %s %s", quoteIdent(DocImagesTable), quoteIdent(constraint.Name), constraint.Definition))
	}
	constraintIndexes := make(map[string]struct{}, len(schema.Constraints))
	for _, constraint := range schema.Constraints {
		constraintIndexes[constraint.Name] = struct{}{}
	}
	for _, index := range schema.Indexes {
		if _, ok := constraintIndexes[index.Name]; ok {
			continue
		}
		statements = append(statements, index.Definition)
	}
	return statements, nil
}

func columnDefinition(col columnSchema) (string, error) {
	dataType := col.DataType
	if col.DataType == "character varying" {
		if col.CharMax == nil {
			return "", fmt.Errorf("column %s is varchar without max length", col.Name)
		}
		dataType = fmt.Sprintf("character varying(%d)", *col.CharMax)
	}
	parts := []string{quoteIdent(col.Name), dataType}
	if col.Default != "" {
		parts = append(parts, "DEFAULT "+col.Default)
	}
	if !col.Nullable {
		parts = append(parts, "NOT NULL")
	}
	return strings.Join(parts, " "), nil
}

func quoteIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
