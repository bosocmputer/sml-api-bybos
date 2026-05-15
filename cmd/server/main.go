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
