package store

import "gorm.io/gorm"

// User 用户表。
type User struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	Email        string `gorm:"uniqueIndex;size:190" json:"email"`
	PasswordHash string  `json:"-"`
	CreatedAtMs  int64  `json:"createdAt"`
	UpdatedAtMs  int64  `json:"updatedAt"`
}

func (User) TableName() string { return "users" }

// Project 生成任务与产出的应用。
type Project struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	UserID       uint   `gorm:"index" json:"userId"`
	Name         string `json:"name"`
	Brief        string `gorm:"type:text" json:"brief"`
	Template     string `gorm:"size:32" json:"template"`
	HTML         string `gorm:"type:text" json:"-"`
	Status       string `gorm:"size:16" json:"status"`
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
	ID        uint   `gorm:"primaryKey" json:"id"`
	ProjectID uint   `gorm:"index" json:"projectId"`
	UserID    uint   `gorm:"index" json:"userId"`
	Role      string `gorm:"size:16" json:"role"`  // user | assistant
	Kind      string `gorm:"size:16" json:"kind"`  // text（普通回复） | run（构建回合）
	Text      string `gorm:"type:text" json:"text"` // 用户输入或助手文本回复
	Status    string `gorm:"size:16" json:"status"` // run 消息的终态：done/failed/stopped
	CreatedAtMs int64 `json:"createdAt"`
}

func (Message) TableName() string { return "messages" }

// Attachment 用户上传的附件（图片走多模态识图，文本/代码直接注入上下文）。
type Attachment struct {
	ID         uint   `gorm:"primaryKey" json:"id"`
	UserID     uint   `gorm:"index" json:"userId"`
	Name       string `gorm:"size:255" json:"name"`
	MimeType   string `gorm:"size:100" json:"mimeType"`
	Size       int64  `json:"size"`
	Content    string `gorm:"type:text" json:"-"` // 文本类内容原文
	DataURL    string `gorm:"type:text" json:"-"` // 图片 dataURL（vision 模型用）
	CreatedAtMs int64 `json:"createdAt"`
}

func (Attachment) TableName() string { return "attachments" }

// DB 持有全局 gorm 实例。
var DB *gorm.DB
