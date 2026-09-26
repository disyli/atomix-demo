package auth_test

import (
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
	if claims.ExpiresAt == nil {
		t.Fatal("ticket 应携带 ExpiresAt")
	}
	// IssueTicket 默认 60s：过期时间应在 [59s, 61s] 区间（容许时钟抖动）
	remain := time.Until(claims.ExpiresAt.Time)
	if remain < 59*time.Second || remain > 61*time.Second {
		t.Errorf("IssueTicket 默认有效期应约 60s，实际剩余 %v", remain)
	}
}

// TestIssueTicketTTL_CustomTTL 参数化 TTL：1 小时票据（预览 Cookie）与 60s 票据各自正确
func TestIssueTicketTTL_CustomTTL(t *testing.T) {
	long, err := auth.IssueTicketTTL(ticketSecret, 1, "a@b.com", time.Hour)
	if err != nil {
		t.Fatalf("IssueTicketTTL error: %v", err)
	}
	claims, err := auth.ParseToken(ticketSecret, long)
	if err != nil {
		t.Fatalf("ParseToken error: %v", err)
	}
	remain := time.Until(claims.ExpiresAt.Time)
	if remain < 59*time.Minute || remain > 61*time.Minute {
		t.Errorf("1h 票据剩余应约 60min，实际 %v", remain)
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

// TestParseToken_Expired 真实过期路径：签发短 TTL 票据并等到过期，解析必须失败。
// TTL 用 2s：JWT NumericDate 是秒级精度，亚秒 TTL 会被截断成"签发即过期"。
// （此前版本用伪造签名的字符串测 err!=nil，实际测的是签名错误而非过期。）
func TestParseToken_Expired(t *testing.T) {
	ticket, err := auth.IssueTicketTTL(ticketSecret, 1, "x@y.com", 2*time.Second)
	if err != nil {
		t.Fatalf("IssueTicketTTL error: %v", err)
	}
	// 未过期时先确认能解析成功（保证不是签名问题导致的假通过）
	if _, err := auth.ParseToken(ticketSecret, ticket); err != nil {
		t.Fatalf("过期前解析应成功: %v", err)
	}
	time.Sleep(2500 * time.Millisecond)
	if _, err := auth.ParseToken(ticketSecret, ticket); err == nil {
		t.Error("过期后解析应失败（jwt 校验 ExpiresAt），但未报错")
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
	time.Sleep(2 * time.Millisecond) // jti 含纳秒时间戳，确保跨纳秒
	t2, _ := auth.IssueTicket(ticketSecret, 1, "a@b.com")
	if t1 == t2 {
		t.Error("连续签发的票据应不同（jti 含纳秒时间戳）")
	}
	c1, err1 := auth.ParseToken(ticketSecret, t1)
	c2, err2 := auth.ParseToken(ticketSecret, t2)
	if err1 != nil || err2 != nil {
		t.Fatalf("票据解析失败: %v / %v", err1, err2)
	}
	if c1.ID == "" || c1.ID == c2.ID {
		t.Errorf("jti 应非空且唯一，实际 %q vs %q", c1.ID, c2.ID)
	}
}
