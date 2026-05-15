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
	r.Use(middleware.Logger(logger))

	// Health — ไม่ต้อง auth/tenant
	h := handlers.NewHealthHandler(dbm)
	r.GET("/health", h.Live)
	r.GET("/health/ready", h.Ready)

	// API v1 — ต้อง auth + tenant
	v1 := r.Group("/api/v1")
	v1.Use(middleware.Auth(cfg.APIKeys))
	v1.Use(middleware.Tenant())

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

	// ── SML REST compat layer ──────────────────────────────────────────────
	// BillFlow ชี้ SHOPEE_SML_URL มาที่นี่แทน 192.168.2.248:8080
	// path ทุก path ตรงกับ SMLJavaRESTService3 เป๊ะ
	cw := compat.NewWriteHandler(dbm)
	cr := compat.NewReadHandler(dbm)

	sml := r.Group("/SMLJavaRESTService")
	sml.Use(middleware.Auth(cfg.APIKeys))
	sml.Use(middleware.Tenant())

	// Write — ส่งเอกสารเข้า SML DB
	sml.POST("/v3/api/saleorder", cw.CreateSaleOrder)
	sml.POST("/saleinvoice/v4", cw.CreateSaleInvoice)
	sml.POST("/v3/api/purchaseorder", cw.CreatePurchaseOrder)

	// Read — party, product, warehouse
	sml.GET("/v3/api/customer", cr.ListCustomers)
	sml.GET("/v3/api/supplier", cr.ListSuppliers)
	sml.GET("/v3/api/product/:code", cr.GetProduct)
	sml.GET("/warehouse/v4", cr.ListWarehouses)
	sml.GET("/warehouse/v4/:code", cr.GetWarehouse)

	addr := cfg.Server.Host + ":" + cfg.Server.Port
	srv := &http.Server{Addr: addr, Handler: r}

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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("shutdown error", zap.Error(err))
	}
}
