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

// DB 持有全局 gorm 实例。
var DB *gorm.DB
