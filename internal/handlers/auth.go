package handlers

import (
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"sml-api-bybos/internal/api"
	"sml-api-bybos/internal/config"
	"sml-api-bybos/internal/db"
)

type AuthHandler struct {
	dbm *db.Manager
	cfg *config.Config
}

type smlLoginRequest struct {
	Provider     string `json:"provider"`
	DataGroup    string `json:"dataGroup"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	DatabaseName string `json:"databaseName"`
}

type smlLoginUser struct {
	UserCode     string `json:"userCode"`
	UserName     string `json:"userName"`
	UserLevel    int16  `json:"userLevel"`
	ActiveStatus int16  `json:"activeStatus"`
}

type smlLoginDatabase struct {
	DataGroup    string              `json:"dataGroup"`
	DataCode     string              `json:"dataCode"`
	DataName     string              `json:"dataName"`
	DatabaseName string              `json:"databaseName"`
	Tenant       string              `json:"tenant"`
	Readiness    *smlTenantReadiness `json:"readiness,omitempty"`
}

type smlTenantReadiness struct {
	OK            bool   `json:"ok"`
	Status        string `json:"status"`
	Message       string `json:"message"`
	Tenant        string `json:"tenant"`
	ImageDatabase string `json:"imageDatabase"`
}

type smlLoginResult struct {
	Provider         string             `json:"provider"`
	DataGroup        string             `json:"dataGroup"`
	User             smlLoginUser       `json:"user"`
	Databases        []smlLoginDatabase `json:"databases"`
	SelectedDatabase *smlLoginDatabase  `json:"selectedDatabase,omitempty"`
}

func NewAuthHandler(dbm *db.Manager, cfg *config.Config) *AuthHandler {
	return &AuthHandler{dbm: dbm, cfg: cfg}
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req smlLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.BadRequest(c, "invalid_json", "request body must be valid JSON", nil)
		return
	}

	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	dataGroup := strings.ToLower(strings.TrimSpace(req.DataGroup))
	username := strings.TrimSpace(req.Username)
	password := req.Password
	if provider == "" || dataGroup == "" || username == "" || password == "" {
		api.BadRequest(c, "missing_credentials", "provider, dataGroup, username, and password are required", nil)
		return
	}
	if provider != h.cfg.Auth.Provider || dataGroup != h.cfg.Auth.DataGroup {
		api.Forbidden(c, "auth_scope_invalid", "provider or dataGroup is not allowed", nil)
		return
	}
	if h.cfg.Auth.MainDatabase == "" {
		api.Internal(c, "auth_main_database_missing", "SML auth main database is not configured", "")
		return
	}

	pool, err := h.dbm.Get(c.Request.Context(), h.cfg.Auth.MainDatabase)
	if err != nil {
		api.Internal(c, "auth_db_pool_error", "connect to SML auth database failed", err.Error())
		return
	}

	user, storedPassword, err := h.lookupUser(c.Request.Context(), pool, username)
	if err == pgx.ErrNoRows || (err == nil && !smlPasswordMatches(password, storedPassword)) {
		api.Unauthorized(c, "username or password is incorrect")
		return
	}
	if err != nil {
		api.Internal(c, "auth_user_lookup_failed", "lookup SML user failed", err.Error())
		return
	}

	databases, err := h.listUserDatabases(c.Request.Context(), pool, username, dataGroup)
	if err != nil {
		api.Internal(c, "auth_database_list_failed", "load SML database permissions failed", err.Error())
		return
	}
	if len(databases) == 0 {
		api.Forbidden(c, "auth_database_empty", "user has no allowed database", nil)
		return
	}
	h.attachDatabaseReadiness(c.Request.Context(), pool, databases)

	var selected *smlLoginDatabase
	if strings.TrimSpace(req.DatabaseName) != "" {
		tenant := normalizeSMLTenant(req.DatabaseName)
		for i := range databases {
			if databases[i].Tenant == tenant || strings.EqualFold(databases[i].DataCode, req.DatabaseName) {
				selected = &databases[i]
				break
			}
		}
		if selected == nil {
			api.Forbidden(c, "database_not_allowed", "database is not allowed for this user", nil)
			return
		}
		if len(h.cfg.DB.AllowedTenants) > 0 {
			if _, ok := h.cfg.DB.AllowedTenants[selected.Tenant]; !ok {
				api.Forbidden(c, "tenant_not_allowed", "tenant is not allowed by this API", gin.H{"tenant": selected.Tenant})
				return
			}
		}
		if _, err := h.dbm.Get(c.Request.Context(), selected.Tenant); err != nil {
			api.Internal(c, "tenant_connect_failed", "selected database cannot be reached", err.Error())
			return
		}
	}

	api.OK(c, smlLoginResult{
		Provider:         h.cfg.Auth.Provider,
		DataGroup:        h.cfg.Auth.DataGroup,
		User:             user,
		Databases:        databases,
		SelectedDatabase: selected,
	})
}

func (h *AuthHandler) lookupUser(ctx context.Context, q pgxQuerier, username string) (smlLoginUser, string, error) {
	var user smlLoginUser
	var storedPassword string
	err := q.QueryRow(ctx, `
SELECT COALESCE(user_code,''), COALESCE(user_name,''), COALESCE(user_password,''), COALESCE(user_level,0), COALESCE(active_status,0)
FROM public.sml_user_list
WHERE lower(trim(user_code)) = lower(trim($1))
LIMIT 1
`, username).Scan(&user.UserCode, &user.UserName, &storedPassword, &user.UserLevel, &user.ActiveStatus)
	return user, storedPassword, err
}

func (h *AuthHandler) listUserDatabases(ctx context.Context, q pgxQuerier, username, dataGroup string) ([]smlLoginDatabase, error) {
	rows, err := q.Query(ctx, `
WITH user_groups AS (
    SELECT trim(group_code) AS group_code
    FROM public.sml_user_and_group
    WHERE lower(trim(user_code)) = lower(trim($1))
),
allowed AS (
    SELECT data_group, data_code
    FROM public.sml_database_list_user_and_group
    WHERE user_or_group_status = 0
      AND lower(trim(user_or_group_code)) = lower(trim($1))
    UNION
    SELECT m.data_group, m.data_code
    FROM public.sml_database_list_user_and_group m
    JOIN user_groups g ON lower(trim(m.user_or_group_code)) = lower(g.group_code)
    WHERE m.user_or_group_status = 1
)
SELECT DISTINCT COALESCE(dl.data_group,''), COALESCE(dl.data_code,''), COALESCE(dl.data_name,''), COALESCE(dl.data_database_name,'')
FROM allowed a
JOIN public.sml_database_list dl ON dl.data_group = a.data_group AND dl.data_code = a.data_code
WHERE lower(trim(dl.data_group)) = lower(trim($2))
  AND COALESCE(dl.data_database_name,'') <> ''
ORDER BY COALESCE(dl.data_code,'')
`, username, dataGroup)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []smlLoginDatabase{}
	for rows.Next() {
		var item smlLoginDatabase
		if err := rows.Scan(&item.DataGroup, &item.DataCode, &item.DataName, &item.DatabaseName); err != nil {
			return nil, err
		}
		item.Tenant = normalizeSMLTenant(item.DatabaseName)
		if item.DataName == "" {
			item.DataName = item.DataCode
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].DataCode) < strings.ToLower(out[j].DataCode)
	})
	return out, nil
}

func (h *AuthHandler) attachDatabaseReadiness(ctx context.Context, q pgxQuerier, databases []smlLoginDatabase) {
	existing := map[string]bool{}
	rows, err := q.Query(ctx, `SELECT lower(datname) FROM pg_database`)
	if err != nil {
		for i := range databases {
			databases[i].Readiness = &smlTenantReadiness{
				OK:            false,
				Status:        "unknown",
				Message:       "cannot verify database readiness right now",
				Tenant:        databases[i].Tenant,
				ImageDatabase: productImageDatabaseName(databases[i].Tenant),
			}
		}
		return
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			existing[strings.ToLower(strings.TrimSpace(name))] = true
		}
	}
	for i := range databases {
		tenant := databases[i].Tenant
		imageDatabase := productImageDatabaseName(tenant)
		readiness := &smlTenantReadiness{
			OK:            true,
			Status:        "ready",
			Message:       "พร้อมใช้งาน",
			Tenant:        tenant,
			ImageDatabase: imageDatabase,
		}
		if !existing[tenant] {
			readiness.OK = false
			readiness.Status = "main_db_missing"
			readiness.Message = "ไม่พบฐานข้อมูล SML หลัก"
		} else if !existing[imageDatabase] {
			readiness.OK = false
			readiness.Status = "image_db_missing"
			readiness.Message = "ยังไม่มีฐานข้อมูลรูป SML"
		}
		databases[i].Readiness = readiness
	}
}

type pgxQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func smlPasswordMatches(password, stored string) bool {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(password), []byte(stored)) == 1 {
		return true
	}
	sum := md5.Sum([]byte(password))
	md5Hex := hex.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(stored)), []byte(md5Hex)) == 1
}

func normalizeSMLTenant(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	return value
}
