package store

import (
	"path/filepath"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open 打开（或创建）SQLite 数据库并完成迁移。
func Open(dataDir string) error {
	db, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "atomix.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return err
	}
	if err := db.AutoMigrate(&User{}, &Project{}, &Event{}, &Attachment{}); err != nil {
		return err
	}
	DB = db
	return nil
}

// Now 返回毫秒时间戳。
func Now() int64 { return time.Now().UnixMilli() }
