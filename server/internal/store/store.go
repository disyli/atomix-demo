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
	if err := db.AutoMigrate(&User{}, &Project{}, &Event{}, &Attachment{}, &Message{}, &Snapshot{}); err != nil {
		return err
	}
	DB = db
	// 清理上次进程异常退出留下的僵尸 generating 项目：
	// 重启后这些项目没有对应的运行 goroutine，状态永远不会翻转，保留 LastGoodHTML 兜底预览。
	DB.Exec("UPDATE projects SET status = 'failed', updated_at_ms = ? WHERE status = 'generating'", Now())
	return nil
}

// Now 返回毫秒时间戳。
func Now() int64 { return time.Now().UnixMilli() }
