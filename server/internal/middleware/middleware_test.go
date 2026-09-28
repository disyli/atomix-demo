package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"atomix-demo/server/internal/auth"
	"atomix-demo/server/internal/middleware"
	"atomix-demo/server/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 测试用同一密钥签 token 与 ticket：这样才能真正执行到 UserIdentity 里
// claims.Use == "ticket" 的检测分支（此前两密钥不同，Parse 在签名校验就失败，
// Use 检测从未被执行，测试是假通过）。
const sharedSecret = "same-secret-for-both-32bytes-xxxxxxxx"

// setupDB 初始化内存 SQLite 并替换全局 store.DB：UserIdentity 现在会查询
// User.TokenVersion 做吊销校验，中间件测试也必须提供真实可查询的用户行，
// 否则 IsTokenRevoked 找不到用户会保守返回已吊销（401），测试语义失真。
func setupDB(t *testing.T) uint {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&store.User{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := store.DB
	store.DB = db
	t.Cleanup(func() { store.DB = prev })
	u := &store.User{Email: "x@y.com", CreatedAtMs: store.Now(), UpdatedAtMs: store.Now()}
	if err := store.DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u.ID
}

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
	uid := setupDB(t)
	tok, _ := auth.IssueToken(sharedSecret, uid, "x@y.com")
	r := newTestEngine(middleware.UserIdentity(sharedSecret, sharedSecret))
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("想要 200，得到 %d", w.Code)
	}
}

// TestUserIdentity_RevokedAfterLogout 退出登录（TokenVersion+1）后，旧 token 立即失效
func TestUserIdentity_RevokedAfterLogout(t *testing.T) {
	uid := setupDB(t)
	tok, _ := auth.IssueToken(sharedSecret, uid, "x@y.com")
	if _, err := store.RevokeUserTokens(uid); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	r := newTestEngine(middleware.UserIdentity(sharedSecret, sharedSecret))
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("退出登录后旧 token 应 401，得到 %d", w.Code)
	}
}

// TestUserIdentity_DBUnavailable store.DB 未初始化（如启动异常场景）时中间件必须
// 优雅返回 401，而不能 panic 拖垮整个进程。
func TestUserIdentity_DBUnavailable(t *testing.T) {
	prev := store.DB
	store.DB = nil
	t.Cleanup(func() { store.DB = prev })
	tok, _ := auth.IssueToken(sharedSecret, 5, "x@y.com")
	r := newTestEngine(middleware.UserIdentity(sharedSecret, sharedSecret))
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("DB 不可用时应 401（不 panic），得到 %d", w.Code)
	}
}

// TestUserIdentity_TicketRejected authed 组不允许 ticket 参数（无论签名是否有效）
func TestUserIdentity_TicketRejected(t *testing.T) {
	setupDB(t)
	ticket, _ := auth.IssueTicket(sharedSecret, 5, "x@y.com")
	r := newTestEngine(middleware.UserIdentity(sharedSecret, sharedSecret))
	req := httptest.NewRequest("GET", "/protected?ticket="+ticket, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("ticket 在 authed 组应返回 401，得到 %d", w.Code)
	}
}

// TestUserIdentity_TicketAsBearer 票据当 Bearer 使用应被 Use==ticket 检测拒绝。
// 同密钥签名保证 Parse 成功、检测分支真正执行。
func TestUserIdentity_TicketAsBearer(t *testing.T) {
	setupDB(t)
	ticket, _ := auth.IssueTicket(sharedSecret, 5, "x@y.com")
	r := newTestEngine(middleware.UserIdentity(sharedSecret, sharedSecret))
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
	r := newTestEngine(middleware.UserIdentity(sharedSecret, sharedSecret))
	req := httptest.NewRequest("GET", "/protected", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("无 token 应返回 401，得到 %d", w.Code)
	}
}

// TestUserIdentity_InvalidToken 非法 token 应返回 401
func TestUserIdentity_InvalidToken(t *testing.T) {
	r := newTestEngine(middleware.UserIdentity(sharedSecret, sharedSecret))
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer garbage.token.here")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("非法 token 应返回 401，得到 %d", w.Code)
	}
}

// TestUserIdentity_TamperedToken 篡改载荷的 token（签名不匹配）应 401
func TestUserIdentity_TamperedToken(t *testing.T) {
	tok, _ := auth.IssueToken(sharedSecret, 5, "x@y.com")
	// 截掉最后一个字符再补一个不同字符：签名校验必须失败
	tampered := tok[:len(tok)-1] + "X"
	r := newTestEngine(middleware.UserIdentity(sharedSecret, sharedSecret))
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tampered)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("篡改 token 应返回 401，得到 %d", w.Code)
	}
}
