package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"sml-api-bybos/internal/config"
	"sml-api-bybos/internal/db"
	"sml-api-bybos/internal/handlers"
	"sml-api-bybos/internal/handlers/compat"
	"sml-api-bybos/internal/middleware"
)

func main() {
	cfg := config.Load()

	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("init logger: %v", err)
	}
	defer logger.Sync()

	dbm := db.NewManager(cfg)
	defer dbm.Close()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(middleware.Logger(logger))

	// Health — no auth/tenant
	h := handlers.NewHealthHandler(dbm, cfg)
	r.GET("/health", h.Live)
	r.GET("/health/ready", h.Ready)

	// API Docs — no auth
	dh := handlers.NewDocsHandler()
	r.GET("/docs", dh.UI)
	r.GET("/docs/", dh.UI)
	r.GET("/docs/openapi.json", dh.Spec)
	r.GET("/openapi.json", dh.Spec)
	r.GET("/docs/:asset", dh.Asset)

	tenantMW := middleware.Tenant(cfg.DB.DefaultTenant, cfg.DB.AllowedTenants)

	// ── API v1 ────────────────────────────────────────────────────────────────
	v1 := r.Group("/api/v1")
	v1.Use(middleware.Auth(cfg.APIKeys))

	// SML auth uses the main registry database, so it must run before tenant
	// resolution. It is still protected by the internal API key middleware.
	ah := handlers.NewAuthHandler(dbm, cfg)
	v1.POST("/auth/sml/login", ah.Login)
	v1.POST("/auth/sml/users/sync-candidates", ah.SyncCandidates)
	trh := handlers.NewTenantReadinessHandler(cfg)
	v1.GET("/tenants/readiness", trh.Get)
	v1.POST("/tenants/image-database", trh.ProvisionImageDatabase)

	v1.Use(tenantMW)

	// Products
	ph := handlers.NewProductHandler(dbm)
	v1.GET("/ic/products", ph.List)
	v1.GET("/ic/units", ph.ListUnits)
	v1.GET("/ic/products/:code/images", ph.ListImages)
	v1.GET("/ic/products/:code/images/:roworder", ph.GetImage)
	v1.GET("/ic/products/:code", ph.Get)

	// Customers
	ch := handlers.NewCustomerHandler(dbm)
	v1.GET("/ar/customers", ch.List)
	v1.POST("/ar/customers", ch.Create)
	v1.GET("/ar/customers/:code", ch.Get)

	// Suppliers
	sh := handlers.NewSupplierHandler(dbm)
	v1.GET("/ap/suppliers", sh.List)
	v1.POST("/ap/suppliers", sh.Create)
	v1.GET("/ap/suppliers/:code", sh.Get)

	// Transactions
	th := handlers.NewTransactionHandler(dbm)
	v1.GET("/ic/transactions", th.List)
	v1.GET("/ic/transactions/:doc_no", th.Get)
	v1.POST("/ic/transactions", th.Create)

	// Transaction summary (daily aggregate by trans_flag)
	smh := handlers.NewSummaryHandler(dbm)
	v1.GET("/ic/transactions/summary", smh.DailySummary)

	// Marketplace read models
	nmh := handlers.NewNextStepMarketplaceHandler(dbm)
	v1.GET("/marketplace/nextstep/orders", nmh.Orders)

	// Stock (warehouse breakdown from vw_stock_balance_by_sloc)
	skh := handlers.NewStockHandler(dbm)
	v1.GET("/ic/stock", skh.List)
	v1.GET("/ic/stock/:code", skh.Get)

	// Write — sale orders, invoices, purchase orders, products
	cw := compat.NewWriteHandler(dbm, logger)
	v1.POST("/ic/sale-orders", cw.CreateSaleOrder)
	v1.POST("/ic/sale-invoices", cw.CreateSaleInvoice)
	v1.POST("/ic/sale-invoices/:doc_no/cancel/preview", cw.PreviewSaleInvoiceCancel)
	v1.POST("/ic/sale-invoices/:doc_no/cancel", cw.CreateSaleInvoiceCancel)
	v1.POST("/ic/purchase-orders", cw.CreatePurchaseOrder)
	v1.PATCH("/ic/purchase-orders/:doc_no/creditor", cw.UpdatePurchaseOrderCreditor)
	v1.PATCH("/ic/purchase-orders/:doc_no/doc-ref", cw.UpdatePurchaseOrderDocRef)
	v1.POST("/ic/products", cw.CreateProduct)

	// Warehouses (compat read handler — not yet in dedicated handler)
	cr := compat.NewReadHandler(dbm)
	v1.GET("/ic/warehouses", cr.ListWarehouses)
	v1.GET("/ic/warehouses/:code", cr.GetWarehouse)

	// Doc Formats
	dfh := handlers.NewDocFormatHandler(dbm)
	v1.GET("/ic/doc-formats/by-code", dfh.GetByCode)
	v1.GET("/ic/doc-formats", dfh.List)
	dch := handlers.NewDocumentCandidateHandler(dbm)
	v1.GET("/ic/document-candidates", dch.List)
	v1.GET("/ic/document-candidates/:doc_no", dch.Get)
	dnh := handlers.NewDocNoHandler(dbm)
	v1.GET("/ic/doc-no/next", dnh.Next)

	// ERP master data
	emh := handlers.NewERPMasterHandler(dbm)
	v1.GET("/erp/branches", emh.ListBranches)
	v1.GET("/erp/users", emh.ListUsers)
	v1.GET("/erp/expenses", emh.ListExpenses)
	v1.GET("/erp/incomes", emh.ListIncomes)
	v1.GET("/erp/passbooks", emh.ListPassbooks)
	v1.GET("/erp/sml-user-list", emh.ListSMLUserList)

	// AR receipts
	arh := handlers.NewARReceiptHandler(dbm)
	v1.POST("/ar/receipt-candidates", arh.Candidates)
	v1.POST("/ar/receipts", arh.Create)

	// Document lock — PaperLess freezes a fully-signed doc by setting
	// is_lock_record=1 (works across ic_trans and ap_ar_trans; idempotent).
	lh := handlers.NewLockHandler(dbm)
	v1.POST("/documents/:doc_no/lock", lh.Lock)
	dih := handlers.NewDocumentImageHandler(dbm)
	v1.POST("/documents/:doc_no/images", dih.Replace)
	rdh := handlers.NewRelatedDocumentHandler(dbm)
	v1.GET("/documents/:doc_no/related", rdh.Related)
	v1.GET("/documents/:doc_no/references", rdh.References)

	addr := cfg.Server.Host + ":" + cfg.Server.Port
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		logger.Info("server starting", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("shutdown error", zap.Error(err))
	}
}
