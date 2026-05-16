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
	h := handlers.NewHealthHandler(dbm)
	r.GET("/health", h.Live)
	r.GET("/health/ready", h.Ready)

	// API Docs — no auth
	dh := handlers.NewDocsHandler()
	r.GET("/docs", dh.UI)
	r.GET("/docs/openapi.json", dh.Spec)

	tenantMW := middleware.Tenant(cfg.DB.DefaultTenant, cfg.DB.AllowedTenants)

	// ── API v1 ────────────────────────────────────────────────────────────────
	v1 := r.Group("/api/v1")
	v1.Use(middleware.Auth(cfg.APIKeys))
	v1.Use(tenantMW)

	// Products
	ph := handlers.NewProductHandler(dbm)
	v1.GET("/ic/products", ph.List)
	v1.GET("/ic/products/:code", ph.Get)

	// Customers
	ch := handlers.NewCustomerHandler(dbm)
	v1.GET("/ar/customers", ch.List)
	v1.GET("/ar/customers/:code", ch.Get)

	// Suppliers
	sh := handlers.NewSupplierHandler(dbm)
	v1.GET("/ap/suppliers", sh.List)
	v1.GET("/ap/suppliers/:code", sh.Get)

	// Transactions
	th := handlers.NewTransactionHandler(dbm)
	v1.GET("/ic/transactions", th.List)
	v1.GET("/ic/transactions/:doc_no", th.Get)
	v1.POST("/ic/transactions", th.Create)

	// Transaction summary (daily aggregate by trans_flag)
	smh := handlers.NewSummaryHandler(dbm)
	v1.GET("/ic/transactions/summary", smh.DailySummary)

	// Stock (warehouse breakdown from vw_stock_balance_by_sloc)
	skh := handlers.NewStockHandler(dbm)
	v1.GET("/ic/stock", skh.List)
	v1.GET("/ic/stock/:code", skh.Get)

	// Write — sale orders, invoices, purchase orders, products
	cw := compat.NewWriteHandler(dbm)
	v1.POST("/ic/sale-orders", cw.CreateSaleOrder)
	v1.POST("/ic/sale-invoices", cw.CreateSaleInvoice)
	v1.POST("/ic/purchase-orders", cw.CreatePurchaseOrder)
	v1.POST("/ic/products", cw.CreateProduct)

	// Warehouses (compat read handler — not yet in dedicated handler)
	cr := compat.NewReadHandler(dbm)
	v1.GET("/ic/warehouses", cr.ListWarehouses)
	v1.GET("/ic/warehouses/:code", cr.GetWarehouse)

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
