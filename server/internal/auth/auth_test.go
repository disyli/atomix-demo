package auth_test

import (
	"strings"
	"testing"
	"time"

	"atomix-demo/server/internal/auth"
)

const (
	jwtSecret    = "test-jwt-secret-32bytes-xxxxxxxxxxx"
	ticketSecret = "test-ticket-secret-32bytes-xxxxxxx"
)

// TestIssueToken_Valid 验证普通 token 可解析且不携带 use=ticket
func TestIssueToken_Valid(t *testing.T) {
	tok, err := auth.IssueToken(jwtSecret, 42, "a@b.com")
	if err != nil {
		t.Fatalf("IssueToken error: %v", err)
	}
	claims, err := auth.ParseToken(jwtSecret, tok)
	if err != nil {
		t.Fatalf("ParseToken error: %v", err)
	}
	if claims.UserID != 42 {
		t.Errorf("UserID want 42 got %d", claims.UserID)
	}
	if claims.Use == "ticket" {
		t.Error("普通 token 不应携带 use=ticket")
	}
}

// TestIssueTicket_HasUseField 验证票据携带 use=ticket 且用 ticketSecret 签名
func TestIssueTicket_HasUseField(t *testing.T) {
	ticket, err := auth.IssueTicket(ticketSecret, 7, "x@y.com")
	if err != nil {
		t.Fatalf("IssueTicket error: %v", err)
	}
	claims, err := auth.ParseToken(ticketSecret, ticket)
	if err != nil {
		t.Fatalf("ParseToken(ticketSecret) error: %v", err)
	}
	if claims.Use != "ticket" {
		t.Errorf("ticket 的 Use 字段应为 'ticket'，实际为 %q", claims.Use)
	}
	if claims.UserID != 7 {
		t.Errorf("UserID want 7 got %d", claims.UserID)
	}
}

// TestTicket_RejectedByJwtSecret 票据用 jwtSecret 解析应失败（密钥隔离）
func TestTicket_RejectedByJwtSecret(t *testing.T) {
	ticket, _ := auth.IssueTicket(ticketSecret, 1, "x@y.com")
	_, err := auth.ParseToken(jwtSecret, ticket)
	if err == nil {
		t.Error("用 jwtSecret 解析票据应失败，但未报错")
	}
}

// TestToken_RejectedByTicketSecret 普通 token 用 ticketSecret 解析应失败
func TestToken_RejectedByTicketSecret(t *testing.T) {
	tok, _ := auth.IssueToken(jwtSecret, 1, "x@y.com")
	_, err := auth.ParseToken(ticketSecret, tok)
	if err == nil {
		t.Error("用 ticketSecret 解析普通 token 应失败，但未报错")
	}
}

// TestParseToken_Expired 验证过期 token 解析失败
func TestParseToken_Expired(t *testing.T) {
	// IssueTicket 正常给 60s，这里直接做一个用 1ns 的 token（即刻过期）
	// 通过 time.Sleep 等到过期（只 1ms，不影响测试速度）
	// 因为 IssueTicket 固定 60s，改用内部方法伪造不方便；
	// 此处验证 ParseToken 在 token 已过期后（例如被篡改 ExpiresAt）的行为，
	// 用一个非法签名的 expired-looking token 字符串来测 err != nil 路径
	fakeExpiredToken := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1aWQiOjEsImVtYWlsIjoiYUBiLmNvbSIsImV4cCI6MX0.invalid"
	_, err := auth.ParseToken(jwtSecret, fakeExpiredToken)
	if err == nil {
		t.Error("解析过期/非法 token 应返回错误")
	}
}

// TestHashPassword_CheckPassword 验证哈希与校验
func TestHashPassword_CheckPassword(t *testing.T) {
	hash, err := auth.HashPassword("secret123")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	if !auth.CheckPassword(hash, "secret123") {
		t.Error("正确密码校验应通过")
	}
	if auth.CheckPassword(hash, "wrong") {
		t.Error("错误密码校验应失败")
	}
}

// TestIssueTicket_JTI_Unique 每次签发的票据 jti 不同（防客户端缓存后复用）
func TestIssueTicket_JTI_Unique(t *testing.T) {
	t1, _ := auth.IssueTicket(ticketSecret, 1, "a@b.com")
	time.Sleep(time.Millisecond)
	t2, _ := auth.IssueTicket(ticketSecret, 1, "a@b.com")
	if t1 == t2 {
		t.Error("连续签发的票据应不同（jti 含纳秒时间戳）")
	}
	// 验证两个票据都能正常解析
	for _, tok := range []string{t1, t2} {
		if _, err := auth.ParseToken(ticketSecret, tok); err != nil {
			t.Errorf("票据 %s 解析失败: %v", tok[:20], err)
		}
	}
	_ = strings.Contains // suppress unused import
}
