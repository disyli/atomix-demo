package store

import (
	"os"
	"path/filepath"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open 打开（或创建）SQLite 数据库并完成迁移。
func Open(dataDir string) error {
	dbPath := filepath.Join(dataDir, "atomix.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return err
	}
	if err := db.AutoMigrate(&User{}, &Project{}, &Event{}, &Attachment{}, &Message{}, &Snapshot{}); err != nil {
		return err
	}
	DB = db
	// 收敛数据库文件权限：SQLite 默认跟随 umask 生成 644（同机其他用户可读），
	// 库内含密码哈希与全部业务数据，统一压到 0600。失败不阻断启动（个别文件系统不支持）。
	_ = os.Chmod(dbPath, 0o600)
	// 清理上次进程异常退出留下的僵尸 generating 项目：重启后这些项目没有对应的运行
	// goroutine，状态永远不会翻转。逐个走 CommitFailure 事务补写失败事件与消息，
	// 避免"状态已 failed 但时间线/对话历史缺失说明"的中间态（此前批量 UPDATE 不写
	// 事件/消息，用户刷新页面只看到卡住的状态，不知道发生了什么）。
	var stuck []Project
	DB.Where("status = ?", "generating").Find(&stuck)
	for _, p := range stuck {
		// 取该项目最后一条用户消息文本作为 run 消息的 text（回看时与用户消息成对还原）；
		// 找不到时传空 brief，CommitFailure 仅补事件、不补消息（无对应用户输入可配对）。
		var lastUserMsg Message
		brief := ""
		if err := DB.Where("project_id = ? AND role = ?", p.ID, "user").Order("id DESC").First(&lastUserMsg).Error; err == nil {
			brief = lastUserMsg.Text
		}
		_ = CommitFailure(p.ID, "failed", "服务重启：构建任务未完成已标记失败，已保留最后成功版本", "err", true, brief, p.UserID)
	}
	return nil
}

// Now 返回毫秒时间戳。
func Now() int64 { return time.Now().UnixMilli() }