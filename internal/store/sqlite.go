package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"

	"trades-ai/internal/config"
)

// Store 封装 SQLite 连接。
type Store struct {
	db *sql.DB
}

// NewSQLite 根据配置初始化 SQLite 存储。
func NewSQLite(cfg config.DatabaseConfig) (*Store, error) {
	dsn := cfg.Path
	if cfg.InMemory {
		dsn = ":memory:"
	} else {
		if err := ensureDir(filepath.Dir(cfg.Path)); err != nil {
			return nil, err
		}
	}

	conn, err := sql.Open("sqlite3", fmt.Sprintf("%s?_busy_timeout=5000&_foreign_keys=on", dsn))
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite 数据库失败: %w", err)
	}

	conn.SetMaxOpenConns(cfg.MaxOpenConns)
	conn.SetMaxIdleConns(cfg.MaxIdleConns)
	conn.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	if _, err := conn.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("设置 SQLite WAL 模式失败: %w", err)
	}

	if _, err := conn.Exec("PRAGMA synchronous=NORMAL;"); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("设置 SQLite 同步级别失败: %w", err)
	}

	return &Store{db: conn}, nil
}

// DB 返回底层 *sql.DB.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Close 关闭数据库连接。
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func ensureDir(path string) error {
	if path == "" || path == "." {
		return nil
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("创建目录 %q 失败: %w", path, err)
	}
	return nil
}
