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
	CreatedAtMs  int64  `json:"createdAt"`
	UpdatedAtMs  int64  `json:"updatedAt"`
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

// MarkProjectStatus 事务内更新项目状态与 LastGoodHTML（失败/停止时保留最后成功版本）。
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

// DB 持有全局 gorm 实例。
var DB *gorm.DB
