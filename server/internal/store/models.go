package store

import (
	"fmt"

	"gorm.io/gorm"
)

// User 用户表。
type User struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	Email        string `gorm:"uniqueIndex;size:190" json:"email"`
	PasswordHash string `json:"-"`
	// TokenVersion 令牌版本号：退出登录时 +1，之前签发的所有 JWT（claims.tv 为旧版本号）
	// 立即失效，无需等待 7 天自然过期。默认 0，新用户与历史用户均从 0 起步。
	TokenVersion int   `gorm:"not null;default:0" json:"-"`
	CreatedAtMs  int64 `json:"createdAt"`
	UpdatedAtMs  int64 `json:"updatedAt"`
}

func (User) TableName() string { return "users" }

// Project 生成任务与产出的应用。
type Project struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	UserID   uint   `gorm:"index" json:"userId"`
	Name     string `json:"name"`
	Brief    string `gorm:"type:text" json:"brief"`
	Template string `gorm:"size:32" json:"template"`
	HTML     string `gorm:"type:text" json:"-"`
	Status   string `gorm:"size:16" json:"status"`
	// Version 当前生效版本号：每次成功构建/迭代/回滚后 +1（与最新 Snapshot 对齐）
	Version int `gorm:"not null;default:0" json:"version"`
	// LastGoodHTML 最近一次通过校验并落库的产物（失败时保留的最后成功版本）
	LastGoodHTML string `gorm:"type:text" json:"-"`
	CreatedAtMs  int64  `json:"createdAt"`
	UpdatedAtMs  int64  `json:"updatedAt"`
}

func (Project) TableName() string { return "projects" }

// Event 一次生成的执行事件流（时间线）。
type Event struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	ProjectID uint   `gorm:"index" json:"projectId"`
	Stage     string `gorm:"size:32" json:"stage"`
	Message   string `gorm:"type:text" json:"message"`
	Level     string `gorm:"size:8" json:"level"`
	TsMs      int64  `json:"ts"`
}

func (Event) TableName() string { return "events" }

// Message 同一 project 的对话消息：每一轮（用户输入与对应助手回复）都保存为新记录，
// 刷新/回看历史项目时按序还原完整对话。
type Message struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	ProjectID   uint   `gorm:"index" json:"projectId"`
	UserID      uint   `gorm:"index" json:"userId"`
	Role        string `gorm:"size:16" json:"role"`   // user | assistant
	Kind        string `gorm:"size:16" json:"kind"`   // text（普通回复） | run（构建回合）
	Text        string `gorm:"type:text" json:"text"` // 用户输入或助手文本回复
	Status      string `gorm:"size:16" json:"status"` // run 消息的终态：done/failed/stopped
	CreatedAtMs int64  `json:"createdAt"`
}

func (Message) TableName() string { return "messages" }

// Attachment 用户上传的附件（图片走多模态识图，文本/代码直接注入上下文）。
type Attachment struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	UserID      uint   `gorm:"index" json:"userId"`
	Name        string `gorm:"size:255" json:"name"`
	MimeType    string `gorm:"size:100" json:"mimeType"`
	Size        int64  `json:"size"`
	Content     string `gorm:"type:text" json:"-"` // 文本类内容原文
	DataURL     string `gorm:"type:text" json:"-"` // 图片 dataURL（vision 模型用）
	CreatedAtMs int64  `json:"createdAt"`
}

func (Attachment) TableName() string { return "attachments" }

// Snapshot 一次成功构建/迭代的产物快照：版本回滚的真实数据源。
// 每次产物通过校验并落库时创建（version 与 Project.Version 同步递增）。
type Snapshot struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	ProjectID   uint   `gorm:"index:idx_snap_proj_ver,unique" json:"projectId"`
	Version     int    `gorm:"index:idx_snap_proj_ver,unique" json:"version"`
	HTML        string `gorm:"type:text" json:"-"`
	Label       string `gorm:"size:190" json:"label"` // 快照说明（首次构建 / 每轮迭代 / 回滚说明）
	Status      string `gorm:"size:16" json:"status"` // done（成功版本才有快照）
	CreatedAtMs int64  `json:"createdAt"`
}

func (Snapshot) TableName() string { return "snapshots" }

// CreateSnapshot 事务内创建版本快照：Project.Version 递增 + Snapshot 写入，
// 保证「版本号 → 快照内容」原子一致（并发下同 project 版本唯一索引防重）。
func CreateSnapshot(projectID uint, html, label string) (*Snapshot, error) {
	var out *Snapshot
	err := DB.Transaction(func(tx *gorm.DB) error {
		var p Project
		if err := tx.Where("id = ?", projectID).First(&p).Error; err != nil {
			return err
		}
		now := Now()
		nextVer := p.Version + 1
		if err := tx.Model(&Project{}).Where("id = ?", projectID).
			Updates(map[string]interface{}{"version": nextVer, "html": html, "last_good_html": html, "status": "ready", "updated_at_ms": now}).Error; err != nil {
			return err
		}
		s := &Snapshot{ProjectID: projectID, Version: nextVer, HTML: html, Label: label, Status: "done", CreatedAtMs: now}
		if err := tx.Create(s).Error; err != nil {
			return err
		}
		out = s
		return nil
	})
	return out, err
}

// RollbackSnapshot 事务内回滚到指定版本：校验目标快照存在后原子更新 Project 三个字段，
// 并新建一条回滚快照（版本继续递增），保证「源码 / 预览 / 版本号」三者一致。
func RollbackSnapshot(projectID uint, targetVersion int) (*Project, *Snapshot, error) {
	var p *Project
	var snap *Snapshot
	err := DB.Transaction(func(tx *gorm.DB) error {
		var proj Project
		if err := tx.Where("id = ?", projectID).First(&proj).Error; err != nil {
			return err
		}
		var target Snapshot
		if err := tx.Where("project_id = ? AND version = ? AND status = ?", projectID, targetVersion, "done").First(&target).Error; err != nil {
			return fmt.Errorf("目标版本 v%d 不存在或不可回滚", targetVersion)
		}
		now := Now()
		nextVer := proj.Version + 1
		// 原子更新：HTML 与 LastGoodHTML 同时指向目标版本内容，状态回到 ready
		if err := tx.Model(&Project{}).Where("id = ?", projectID).Updates(map[string]interface{}{
			"html": target.HTML, "last_good_html": target.HTML,
			"status": "ready", "version": nextVer, "updated_at_ms": now,
		}).Error; err != nil {
			return err
		}
		// 回滚也生成快照（可再回滚回来）：内容 = 目标版本源码
		s := &Snapshot{ProjectID: projectID, Version: nextVer, HTML: target.HTML,
			Label: fmt.Sprintf("回滚至 v%d", targetVersion), Status: "done", CreatedAtMs: now}
		if err := tx.Create(s).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", projectID).First(&proj).Error; err != nil {
			return err
		}
		p = &proj
		snap = s
		return nil
	})
	return p, snap, err
}

// CommitSuccess 成功终态的单一事务原子提交：满足"只有源码、Preview、事件和消息
// 均成功落库后才能标记完成"的硬性要求。同一事务内完成：
//   1. Project：version+1 / html / last_good_html / status=ready（完成标记）
//   2. Snapshot：新版本快照（版本回滚的真实数据源）
//   3. Event：完成事件（时间线可见的"落库为 vN"留痕）
//   4. Message：assistant run 消息（status=done，对话回看依据）
// 任一步失败整体回滚——项目中途崩溃不会出现"显示完成但事件/消息缺失"的中间态；
// 事务外的 name/template 回填独立先行（展示字段，不影响完成语义）。
func CommitSuccess(projectID uint, html, label, doneEventMsg, summary, brief string, userID uint) (*Snapshot, error) {
	var out *Snapshot
	err := DB.Transaction(func(tx *gorm.DB) error {
		var p Project
		if err := tx.Where("id = ?", projectID).First(&p).Error; err != nil {
			return err
		}
		now := Now()
		nextVer := p.Version + 1
		if err := tx.Model(&Project{}).Where("id = ?", projectID).
			Updates(map[string]interface{}{"version": nextVer, "html": html, "last_good_html": html, "status": "ready", "updated_at_ms": now}).Error; err != nil {
			return err
		}
		s := &Snapshot{ProjectID: projectID, Version: nextVer, HTML: html, Label: label, Status: "done", CreatedAtMs: now}
		if err := tx.Create(s).Error; err != nil {
			return err
		}
		if doneEventMsg != "" {
			msg := fmt.Sprintf(doneEventMsg, nextVer)
			if err := tx.Create(&Event{ProjectID: projectID, Stage: "done", Message: msg, Level: "info", TsMs: now}).Error; err != nil {
				return err
			}
		}
		if summary != "" {
			if err := tx.Create(&Event{ProjectID: projectID, Stage: "done", Message: summary, Level: "info", TsMs: now}).Error; err != nil {
				return err
			}
		}
		if brief != "" {
			if err := tx.Create(&Message{ProjectID: projectID, UserID: userID, Role: "assistant", Kind: "run", Text: brief, Status: "done", CreatedAtMs: now}).Error; err != nil {
				return err
			}
		}
		out = s
		return nil
	})
	return out, err
}

// CommitFailure 失败/停止终态的单一事务原子提交：与 CommitSuccess 对称，修复
// 原实现"状态、事件、消息分三步写库且不检查错误"的缺口（进程崩溃可能出现
// status=failed 但缺失事件或消息的中间态）。同一事务内完成：
//  1. Project：status=status，keepHTML 时 HTML 回退到 LastGoodHTML（保留最后成功版本）
//  2. Event：终态事件（stage=done，供时间线展示失败/停止原因）
//  3. Message：assistant run 消息（status 为 failed/stopped，对话回看依据）
// 任一步失败整体回滚并原样返回 error，调用方需自行决定重试或放弃（不会出现部分落库）。
func CommitFailure(projectID uint, status, eventMsg, level string, keepHTML bool, brief string, userID uint) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var p Project
		if err := tx.Where("id = ?", projectID).First(&p).Error; err != nil {
			return err
		}
		now := Now()
		updates := map[string]interface{}{"status": status, "updated_at_ms": now}
		if keepHTML && p.LastGoodHTML != "" {
			updates["html"] = p.LastGoodHTML
		}
		if err := tx.Model(&Project{}).Where("id = ?", projectID).Updates(updates).Error; err != nil {
			return err
		}
		if eventMsg != "" {
			if err := tx.Create(&Event{ProjectID: projectID, Stage: "done", Message: eventMsg, Level: level, TsMs: now}).Error; err != nil {
				return err
			}
		}
		if brief != "" {
			if err := tx.Create(&Message{ProjectID: projectID, UserID: userID, Role: "assistant", Kind: "run", Text: brief, Status: status, CreatedAtMs: now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// MarkProjectStatus 事务内更新项目状态与 LastGoodHTML（失败/停止时保留最后成功版本）。
// 单独调用场景已收窄：仅 CommitFailure 内部与極少数不需要事件/消息的路径使用；
// 失败终态请优先使用 CommitFailure 保证状态+事件+消息原子一致。
func MarkProjectStatus(projectID uint, status string, keepHTML bool) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var p Project
		if err := tx.Where("id = ?", projectID).First(&p).Error; err != nil {
			return err
		}
		updates := map[string]interface{}{"status": status, "updated_at_ms": Now()}
		// 失败/停止时：HTML 回退到最后成功版本（若有），保证预览永远指向可用产物
		if keepHTML && p.LastGoodHTML != "" {
			updates["html"] = p.LastGoodHTML
		}
		return tx.Model(&Project{}).Where("id = ?", projectID).Updates(updates).Error
	})
}

// ListSnapshots 返回项目全部成功版本快照（不含 HTML 正文，降传输）。
func ListSnapshots(projectID uint) ([]Snapshot, error) {
	var ss []Snapshot
	err := DB.Where("project_id = ? AND status = ?", projectID, "done").Order("version DESC").Find(&ss).Error
	return ss, err
}

// IsTokenRevoked 判断给定用户的 token 版本号是否已被吊销：tv 落后于库内当前
// TokenVersion 即视为已吊销（用户已退出登录或版本号被主动升级）。
// 查询失败（如用户已被删除）或 DB 未初始化时保守返回 true（拒绝访问，不 panic）：
// 中间件因此只会多返回一次 401，绝不会因为 store.DB 为 nil 而使进程崩溃。
func IsTokenRevoked(userID uint, tv int) bool {
	if DB == nil {
		return true
	}
	var u User
	if err := DB.Select("token_version").Where("id = ?", userID).First(&u).Error; err != nil {
		return true
	}
	return tv < u.TokenVersion
}

// RevokeUserTokens 退出登录：TokenVersion+1，之前签发的所有 token（tv 为旧版本号）
// 立即失效。返回递增后的新版本号，供调用方立即签发新 token（如需要）。
func RevokeUserTokens(userID uint) (int, error) {
	var newVersion int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var u User
		if err := tx.Where("id = ?", userID).First(&u).Error; err != nil {
			return err
		}
		newVersion = u.TokenVersion + 1
		return tx.Model(&User{}).Where("id = ?", userID).
			Updates(map[string]interface{}{"token_version": newVersion, "updated_at_ms": Now()}).Error
	})
	return newVersion, err
}

// DB 持有全局 gorm 实例。
var DB *gorm.DB

// CleanupGuests 清理过期游客账号及其全部数据（级联删除）。
// 游客邮箱统一 @guest.atomix 后缀，创建时间早于 cutoff 的游客连同其
// 项目/事件/消息/附件/快照一并删除；仍处 generating 状态的项目所在游客跳过
// （避免删到正在运行的构建）。返回删除的游客数；ttlHours<=0 时直接返回（清理关闭）。
// 正式注册账号不匹配该后缀（register 拒绝保留域），永不受影响。
func CleanupGuests(ttlHours int) (int64, error) {
	if ttlHours <= 0 {
		return 0, nil
	}
	cutoff := Now() - int64(ttlHours)*3600*1000
	var removed int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		var guestIDs []uint
		if err := tx.Model(&User{}).
			Where("email LIKE ? AND created_at_ms < ?"+
				" AND id NOT IN (SELECT user_id FROM projects WHERE status = 'generating')",
				"%@guest.atomix", cutoff).
			Pluck("id", &guestIDs).Error; err != nil {
			return err
		}
		if len(guestIDs) == 0 {
			return nil
		}
		var projectIDs []uint
		if err := tx.Model(&Project{}).Where("user_id IN ?", guestIDs).Pluck("id", &projectIDs).Error; err != nil {
			return err
		}
		if len(projectIDs) > 0 {
			if err := tx.Where("project_id IN ?", projectIDs).Delete(&Event{}).Error; err != nil {
				return err
			}
			if err := tx.Where("project_id IN ?", projectIDs).Delete(&Snapshot{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("user_id IN ?", guestIDs).Delete(&Message{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id IN ?", guestIDs).Delete(&Attachment{}).Error; err != nil {
			return err
		}
		if len(projectIDs) > 0 {
			if err := tx.Where("id IN ?", projectIDs).Delete(&Project{}).Error; err != nil {
				return err
			}
		}
		res := tx.Where("id IN ?", guestIDs).Delete(&User{})
		if res.Error != nil {
			return res.Error
		}
		removed = res.RowsAffected
		return nil
	})
	return removed, err
}
