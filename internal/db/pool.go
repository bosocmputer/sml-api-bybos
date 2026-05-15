package db

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"sml-api-bybos/internal/config"
)

// Manager holds one pgxpool per tenant DB name.
type Manager struct {
	mu     sync.RWMutex
	pools  map[string]*pgxpool.Pool
	cfg    *config.Config
}

func NewManager(cfg *config.Config) *Manager {
	return &Manager{
		pools: make(map[string]*pgxpool.Pool),
		cfg:   cfg,
	}
}

// Get returns an existing pool or creates one for the given DB name.
func (m *Manager) Get(ctx context.Context, dbName string) (*pgxpool.Pool, error) {
	m.mu.RLock()
	p, ok := m.pools[dbName]
	m.mu.RUnlock()
	if ok {
		return p, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// double-check after acquiring write lock
	if p, ok = m.pools[dbName]; ok {
		return p, nil
	}

	dsn := m.cfg.DSN(dbName)
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn for %s: %w", dbName, err)
	}
	poolCfg.MaxConns = 10

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("open pool for %s: %w", dbName, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping %s: %w", dbName, err)
	}

	m.pools[dbName] = pool
	return pool, nil
}

// Close shuts down all pools.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.pools {
		p.Close()
	}
	m.pools = make(map[string]*pgxpool.Pool)
}
