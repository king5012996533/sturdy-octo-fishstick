package database

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type Config struct {
	Driver  string
	DSN     string
	DataDir string
}

// normalizeDriver 把配置里的驱动名收成内部判断用的形态；空值即默认的 sqlite。
func normalizeDriver(driver string) string {
	normalized := strings.ToLower(strings.TrimSpace(driver))
	if normalized == "" {
		return "sqlite"
	}
	return normalized
}

// SupportedDriver 说明这个二进制是否真的编译进了对应驱动。
//
// 部署清单、文档自检用它拦下"声明了 postgres 但代码里没有驱动"这类只有上线才会
// 暴露的错配：那种配置的表现是服务根本起不来，而不是某条链路降级。
func SupportedDriver(driver string) bool {
	switch normalizeDriver(driver) {
	case "sqlite", "mysql":
		return true
	default:
		return false
	}
}

func Open(config Config) (*gorm.DB, error) {
	driver := normalizeDriver(config.Driver)
	switch driver {
	case "sqlite":
		dsn := strings.TrimSpace(config.DSN)
		if dsn == "" {
			if err := os.MkdirAll(config.DataDir, 0o755); err != nil {
				return nil, err
			}
			dsn = config.DataDir + "/open_ai_canvas.db?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on&_synchronous=NORMAL"
		}
		return gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	case "mysql":
		dsn := strings.TrimSpace(config.DSN)
		if dsn == "" {
			return nil, errors.New("mysql 驱动需要显式配置 DSN")
		}
		return gorm.Open(mysql.Open(dsn), &gorm.Config{})
	default:
		return nil, fmt.Errorf("不支持的数据库驱动：%s", driver)
	}
}

func ConfigurePool(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	if db.Dialector.Name() == "mysql" {
		// MySQL 由服务端承担并发；这里只约束生命周期，避免连接被中间设备静默掐断后仍被复用。
		sqlDB.SetMaxOpenConns(20)
		sqlDB.SetMaxIdleConns(10)
		sqlDB.SetConnMaxLifetime(30 * time.Minute)
		sqlDB.SetConnMaxIdleTime(5 * time.Minute)
		return nil
	}
	// SQLite serializes writers. A single shared connection avoids intermittent
	// SQLITE_BUSY failures under concurrent autosave/task updates while WAL
	// still keeps reads cheap for this single-process desktop application.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	return nil
}
