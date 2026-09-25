package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"atomix-demo/server/internal/auth"
	"atomix-demo/server/internal/middleware"
	"github.com/gin-gonic/gin"
)

const (
	jwtSecret    = "test-jwt-secret-32bytes-xxxxxxxxxxx"
	ticketSecret = "test-ticket-secret-32bytes-xxxxxxx"
)

func newTestEngine(mid gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/protected", mid, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"uid": middleware.UID(c)})
	})
	return r
}

// TestUserIdentity_ValidBearer 正常 Bearer token 应通过
func TestUserIdentity_ValidBearer(t *testing.T) {
	tok, _ := auth.IssueToken(jwtSecret, 5, "x@y.com")
	r := newTestEngine(middleware.UserIdentity(jwtSecret, ticketSecret))
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("想要 200，得到 %d", w.Code)
	}
}

// TestUserIdentity_TicketRejected authed 组不允许 ticket 参数
func TestUserIdentity_TicketRejected(t *testing.T) {
	ticket, _ := auth.IssueTicket(ticketSecret, 5, "x@y.com")
	r := newTestEngine(middleware.UserIdentity(jwtSecret, ticketSecret))
	req := httptest.NewRequest("GET", "/protected?ticket="+ticket, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("ticket 在 authed 组应返回 401，得到 %d", w.Code)
	}
}

// TestUserIdentity_TicketAsBearer 把 ticket 当 Bearer 传应被拒绝（Use=ticket 检测）
func TestUserIdentity_TicketAsBearer(t *testing.T) {
	ticket, _ := auth.IssueTicket(ticketSecret, 5, "x@y.com")
	// 如果攻击者用 ticketSecret 签名的 ticket 尝试当 jwtSecret Bearer 使用
	// 由于密钥不同，ParseToken(jwtSecret, ticket) 会直接报错，同样应该 401
	r := newTestEngine(middleware.UserIdentity(jwtSecret, ticketSecret))
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+ticket)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("ticket 当 Bearer 使用应返回 401，得到 %d", w.Code)
	}
}

// TestUserIdentity_NoToken 无 token 应返回 401
func TestUserIdentity_NoToken(t *testing.T) {
	r := newTestEngine(middleware.UserIdentity(jwtSecret, ticketSecret))
	req := httptest.NewRequest("GET", "/protected", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("无 token 应返回 401，得到 %d", w.Code)
	}
}

// TestUserIdentity_InvalidToken 非法 token 应返回 401
func TestUserIdentity_InvalidToken(t *testing.T) {
	r := newTestEngine(middleware.UserIdentity(jwtSecret, ticketSecret))
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer garbage.token.here")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("非法 token 应返回 401，得到 %d", w.Code)
	}
}
