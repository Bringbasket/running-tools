package persistence

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/Bringbasket/running-tools/internal/platform/persistence/ent"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	config Config
	db     *sql.DB
	ent    *ent.Client
	redis  *redis.Client
}

func Open(ctx context.Context, cfg Config) (*Service, error) {
	service := &Service{config: cfg}
	if cfg.Mode != StorageJSON {
		db, err := sql.Open("pgx", cfg.DatabaseURL)
		if err != nil {
			return nil, fmt.Errorf("open PostgreSQL: %w", err)
		}
		db.SetMaxOpenConns(cfg.MaxOpenConns)
		db.SetMaxIdleConns(cfg.MaxIdleConns)
		db.SetConnMaxLifetime(30 * time.Minute)
		if err := db.PingContext(ctx); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("connect PostgreSQL: %w", err)
		}
		if err := migrate(ctx, db); err != nil {
			_ = db.Close()
			return nil, err
		}
		service.db = db
		service.ent = ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	}
	if cfg.RedisAddr != "" {
		service.redis = redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword, DB: cfg.RedisDB})
	}
	return service, nil
}

func (s *Service) Mode() StorageMode    { return s.config.Mode }
func (s *Service) Ent() *ent.Client     { return s.ent }
func (s *Service) DB() *sql.DB          { return s.db }
func (s *Service) Redis() *redis.Client { return s.redis }
func (s *Service) RedisPrefix() string  { return s.config.RedisPrefix }

func (s *Service) Close() error {
	if s.redis != nil {
		_ = s.redis.Close()
	}
	if s.ent != nil {
		return s.ent.Close()
	}
	return nil
}

func (s *Service) Health(ctx context.Context) (map[string]string, bool) {
	result := map[string]string{"storageMode": string(s.config.Mode), "postgres": "disabled", "redis": "disabled"}
	healthy := true
	if s.db != nil {
		if err := s.db.PingContext(ctx); err != nil {
			result["postgres"] = "error"
			healthy = false
		} else {
			result["postgres"] = "ok"
		}
	}
	if s.redis != nil {
		if err := s.redis.Ping(ctx).Err(); err != nil {
			result["redis"] = "degraded"
		} else {
			result["redis"] = "ok"
		}
	}
	return result, healthy
}

// AcquireLock obtains a renewable Redis lease. Redis errors are returned so the
// caller can explicitly choose a local-lock fallback.
func (s *Service) AcquireLock(ctx context.Context, name string) (func(), bool, error) {
	if s.redis == nil {
		return func() {}, true, nil
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, false, err
	}
	token := hex.EncodeToString(tokenBytes)
	key := s.config.RedisPrefix + name
	acquired, err := s.redis.SetNX(ctx, key, token, s.config.LockTTL).Result()
	if err != nil || !acquired {
		return nil, acquired, err
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(s.config.LockTTL / 3)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				renewCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				_, _ = s.redis.Eval(renewCtx, `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("pexpire", KEYS[1], ARGV[2]) else return 0 end`, []string{key}, token, s.config.LockTTL.Milliseconds()).Result()
				cancel()
			case <-stop:
				return
			}
		}
	}()
	var once sync.Once
	release := func() {
		once.Do(func() {
			close(stop)
			<-done
			releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, _ = s.redis.Eval(releaseCtx, `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`, []string{key}, token).Result()
		})
	}
	return release, true, nil
}
