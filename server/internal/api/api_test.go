package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"atomix-demo/server/internal/agent"
	"atomix-demo/server/internal/api"
	"atomix-demo/server/internal/auth"
	"atomix-demo/server/internal/config"
	"atomix-demo/server/internal/store"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const (
	testJWTSecret    = "api-test-jwt-secret-32bytes-xxxxxxxx"
	testTicketSecret = "api-test-ticket-secret-32bytes-xxxxx"
)

// newTestApp 构造挂好全部路由 + 内存库的测试实例，返回 engine 与已登录用户 token。
func newTestApp(t *testing.T) (*gin.Engine, string, uint) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "t.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&store.User{}, &store.Project{}, &store.Event{}, &store.Attachment{}, &store.Message{}, &store.Snapshot{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := store.DB
	store.DB = db
	t.Cleanup(func() { store.DB = prev })

	cfg := &config.Config{
		JWTSecret: testJWTSecret, TicketSecret: testTicketSecret,
		DeploySHA: "test", GuestEnabled: true,
	}
	h := &api.Handlers{Cfg: cfg, Agent: agent.NewAgent(nil, true)}
	r := gin.New()
	api.Register(r, h)

	// 注册一个正式账号（绕过限流计数从 0 开始的干扰：直接落库）
	hash, _ := auth.HashPassword("pass123456")
	u := &store.User{Email: "owner@t.test", PasswordHash: hash, CreatedAtMs: store.Now(), UpdatedAtMs: store.Now()}
	if err := store.DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	tok, _ := auth.IssueToken(testJWTSecret, u.ID, u.Email)
	return r, tok, u.ID
}

// createReadyProject 直接落库一个带产物的项目（status=ready，v1）
func createReadyProject(t *testing.T, uid uint, html string) uint {
	t.Helper()
	p := &store.Project{UserID: uid, Name: "App", Brief: "b", Template: "todo", HTML: html,
		LastGoodHTML: html, Status: "ready", Version: 1, CreatedAtMs: store.Now(), UpdatedAtMs: store.Now()}
	if err := store.DB.Create(p).Error; err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := store.DB.Create(&store.Snapshot{ProjectID: p.ID, Version: 1, HTML: html, Label: "首次构建", Status: "done", CreatedAtMs: store.Now()}).Error; err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	return p.ID
}

func doJSON(r *gin.Engine, method, path, token string, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ---------- CSP sandbox：预览响应头必须带沙箱指令 ----------

func TestPreview_CSPSandboxHeader(t *testing.T) {
	r, tok, uid := newTestApp(t)
	pid := createReadyProject(t, uid, "<!DOCTYPE html><html><body><h1>hi</h1></body></html>")

	// 先签发预览 Cookie（authed 接口）
	w := doJSON(r, "POST", "/api/ticket", tok, "{}")
	if w.Code != http.StatusOK {
		t.Fatalf("issueTicket: %d %s", w.Code, w.Body.String())
	}
	cookie := w.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, "atomix_preview=") || !strings.Contains(cookie, "HttpOnly") {
		t.Fatalf("Set-Cookie 应为 HttpOnly atomix_preview，实际: %s", cookie)
	}

	// 携带 Cookie 请求预览
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/projects/%d/preview", pid), nil)
	req.Header.Set("Cookie", strings.Split(cookie, ";")[0])
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req)
	if w2.Code != http.StatusOK {
		t.Fatalf("preview with cookie: %d %s", w2.Code, w2.Body.String())
	}
	csp := w2.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox") {
		t.Errorf("preview 响应必须带 CSP sandbox 头，实际: %q", csp)
	}
	if !strings.Contains(csp, "allow-scripts") || strings.Contains(csp, "allow-same-origin") {
		t.Errorf("CSP 应含 allow-scripts 且不含 allow-same-origin，实际: %q", csp)
	}
}

// ---------- Cookie 鉴权：无凭据 401、过期 401、他项目 404 ----------

func TestPreview_CookieRequired(t *testing.T) {
	r, _, uid := newTestApp(t)
	pid := createReadyProject(t, uid, "<html>ok</html>")
	w := doJSON(r, "GET", fmt.Sprintf("/api/projects/%d/preview", pid), "", "")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("无凭据预览应 401，得到 %d", w.Code)
	}
}

func TestPreview_CookieExpired(t *testing.T) {
	r, tok, uid := newTestApp(t)
	pid := createReadyProject(t, uid, "<html>ok</html>")
	_ = tok
	// 直接构造过期票据当 Cookie
	expired, _ := auth.IssueTicketTTL(testTicketSecret, uid, "owner@t.test", -time.Second)
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/projects/%d/preview", pid), nil)
	req.Header.Set("Cookie", "atomix_preview="+expired)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("过期 Cookie 预览应 401，得到 %d", w.Code)
	}
}

func TestPreview_OtherUsersProject(t *testing.T) {
	r, _, uid := newTestApp(t)
	pid := createReadyProject(t, uid, "<html>ok</html>")
	// 另一个用户（同密钥签发）带合法 Cookie 访问别人项目
	other, _ := auth.IssueToken(testJWTSecret, uid+1, "other@t.test")
	w := doJSON(r, "POST", "/api/ticket", other, "{}")
	if w.Code != http.StatusOK {
		t.Fatalf("issueTicket: %d", w.Code)
	}
	cookie := strings.Split(w.Header().Get("Set-Cookie"), ";")[0]
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/projects/%d/preview", pid), nil)
	req.Header.Set("Cookie", cookie)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req)
	if w2.Code != http.StatusNotFound {
		t.Errorf("访问他人项目预览应 404，得到 %d", w2.Code)
	}
}

// ---------- 票据拆分：JSON 票据 60s、Cookie 票据 1h，二者独立 ----------

func TestIssueTicket_TTLSplit(t *testing.T) {
	r, tok, _ := newTestApp(t)
	w := doJSON(r, "POST", "/api/ticket", tok, "{}")
	if w.Code != http.StatusOK {
		t.Fatalf("issueTicket: %d", w.Code)
	}
	var body struct{ Ticket string }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Ticket == "" {
		t.Fatalf("ticket 响应解析失败: %v %s", err, w.Body.String())
	}
	// JSON 票据必须约 60s（防止长凭据进 SSE URL/访问日志）
	sseClaims, err := auth.ParseToken(testTicketSecret, body.Ticket)
	if err != nil {
		t.Fatalf("JSON ticket 解析失败: %v", err)
	}
	remain := time.Until(sseClaims.ExpiresAt.Time)
	if remain > 61*time.Second || remain < 59*time.Second {
		t.Errorf("JSON ticket 应约 60s，实际剩余 %v", remain)
	}
	// Cookie 票据必须约 1h（预览会话时长）
	ck := strings.Split(w.Header().Get("Set-Cookie"), ";")[0]
	ckVal := strings.TrimPrefix(ck, "atomix_preview=")
	ckClaims, err := auth.ParseToken(testTicketSecret, ckVal)
	if err != nil {
		t.Fatalf("cookie ticket 解析失败: %v", err)
	}
	ckRemain := time.Until(ckClaims.ExpiresAt.Time)
	if ckRemain > 61*time.Minute || ckRemain < 59*time.Minute {
		t.Errorf("Cookie ticket 应约 1h，实际剩余 %v", ckRemain)
	}
	// Cookie 属性：HttpOnly + Secure + SameSite=Lax
	setCookie := w.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "HttpOnly") || !strings.Contains(setCookie, "Secure") || !strings.Contains(setCookie, "SameSite=Lax") {
		t.Errorf("Set-Cookie 应含 HttpOnly/Secure/SameSite=Lax，实际: %s", setCookie)
	}
	// 两张票据互不相同
	if body.Ticket == ckVal {
		t.Error("JSON ticket 与 Cookie ticket 应分开签发（不同 jti）")
	}
}

// ---------- 下载：Cookie 鉴权可用、无凭据 401、60s ticket 回落可用 ----------

func TestSourceDownload_AuthPaths(t *testing.T) {
	r, tok, uid := newTestApp(t)
	pid := createReadyProject(t, uid, "<html><body>ok</body></html>")

	// 无凭据：401
	w := doJSON(r, "GET", fmt.Sprintf("/api/projects/%d/source?download=1", pid), "", "")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("无凭据下载应 401，得到 %d", w.Code)
	}

	// 签发票据后仅凭 Cookie 下载：200 + 附件头（浏览器 <a download> 的真实路径）
	wt := doJSON(r, "POST", "/api/ticket", tok, "{}")
	if wt.Code != http.StatusOK {
		t.Fatalf("issueTicket: %d", wt.Code)
	}
	cookie := strings.Split(wt.Header().Get("Set-Cookie"), ";")[0]
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/projects/%d/source?download=1", pid), nil)
	req.Header.Set("Cookie", cookie)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req)
	if w2.Code != http.StatusOK {
		t.Fatalf("Cookie 下载应 200，得到 %d %s", w2.Code, w2.Body.String())
	}
	if cd := w2.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("下载应带 attachment 头，实际: %q", cd)
	}
	if !strings.Contains(w2.Body.String(), "ok") {
		t.Errorf("下载应返回源码正文")
	}

	// JSON 返回的 60s ticket 走 query（API 直调兼容路径）：同样可用
	var body struct{ Ticket string }
	json.Unmarshal(wt.Body.Bytes(), &body)
	w3 := doJSON(r, "GET", fmt.Sprintf("/api/projects/%d/source?download=1&ticket=%s", pid, body.Ticket), "", "")
	if w3.Code != http.StatusOK {
		t.Errorf("query ticket 下载应 200，得到 %d", w3.Code)
	}
}

// ---------- 退出登录：HttpOnly Cookie 必须被清除 ----------

func TestLogout_ClearsPreviewCookie(t *testing.T) {
	r, tok, uid := newTestApp(t)
	pid := createReadyProject(t, uid, "<html>ok</html>")

	// 先签发拿到 Cookie，确认可用
	wt := doJSON(r, "POST", "/api/ticket", tok, "{}")
	cookie := strings.Split(wt.Header().Get("Set-Cookie"), ";")[0]
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/projects/%d/preview", pid), nil)
	req.Header.Set("Cookie", cookie)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("签发后预览应 200，得到 %d", w.Code)
	}

	// 退出：Set-Cookie MaxAge=-1
	wl := doJSON(r, "POST", "/api/auth/logout", tok, "{}")
	if wl.Code != http.StatusOK {
		t.Fatalf("logout: %d", wl.Code)
	}
	clear := wl.Header().Get("Set-Cookie")
	if !strings.Contains(clear, "atomix_preview=") || !strings.Contains(clear, "Max-Age=0") {
		t.Fatalf("logout 应下发清 Cookie 头（MaxAge=-1），实际: %s", clear)
	}

	// 旧 Cookie 值再次使用仍被服务端拒收？——注意：票据是自包含 JWT，服务端无会话表，
	// 清 Cookie 只是让浏览器丢弃；未过期的旧值技术上仍有效。这是 JWT 的固有属性，
	// 防御目标是「浏览器不再自动携带」，此测试验证清 Cookie 头下发正确即达标。
}

// ---------- 邮箱校验与规范化 ----------

func TestRegister_EmailValidation(t *testing.T) {
	r, _, _ := newTestApp(t)
	cases := []struct {
		email string
		code  int
	}{
		{"x", http.StatusBadRequest},       // 无 @
		{"a@b", http.StatusBadRequest},     // 域名无点
		{"@b.c", http.StatusBadRequest},    // 空局部
		{"a b@c.d", http.StatusBadRequest}, // 含空格
		{"valid@b.c", http.StatusOK},       // 合法
	}
	for _, c := range cases {
		w := doJSON(r, "POST", "/api/auth/register", "", fmt.Sprintf(`{"email":%q,"password":"123456"}`, c.email))
		if w.Code != c.code {
			t.Errorf("email=%q 想要 %d 得到 %d（body: %s）", c.email, c.code, w.Code, w.Body.String())
		}
	}
	// 限流会触发：上面 5 次注册在 10/min 内，再加 6 次应 429
	for i := 0; i < 6; i++ {
		w := doJSON(r, "POST", "/api/auth/register", "", fmt.Sprintf(`{"email":"u%d@b.c","password":"123456"}`, i))
		if i < 5 && w.Code != http.StatusOK {
			t.Fatalf("第 %d 次注册应成功，得到 %d", i+6, w.Code)
		}
		if i == 5 && w.Code != http.StatusTooManyRequests {
			t.Fatalf("第 11 次注册应 429，得到 %d", w.Code)
		}
	}
}

func TestRegister_EmailCaseInsensitive(t *testing.T) {
	r, _, _ := newTestApp(t)
	w := doJSON(r, "POST", "/api/auth/register", "", `{"email":"Case@T.Test","password":"123456"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("大写邮箱注册: %d %s", w.Code, w.Body.String())
	}
	// 小写变体应判定为已注册（409）
	w2 := doJSON(r, "POST", "/api/auth/register", "", `{"email":"case@t.test","password":"123456"}`)
	if w2.Code != http.StatusConflict {
		t.Errorf("大小写变体应 409，得到 %d", w2.Code)
	}
}

// ---------- 长度上限 ----------

func TestBrief_LengthLimit(t *testing.T) {
	r, tok, _ := newTestApp(t)
	big := strings.Repeat("a", 20001)
	// createProject
	w := doJSON(r, "POST", "/api/projects", tok, fmt.Sprintf(`{"brief":%q}`, big))
	if w.Code != http.StatusBadRequest {
		t.Errorf("20001 字 brief 应 400，得到 %d", w.Code)
	}
	// chat
	w2 := doJSON(r, "POST", "/api/chat", tok, fmt.Sprintf(`{"message":%q}`, strings.Repeat("a", 8001)))
	if w2.Code != http.StatusBadRequest {
		t.Errorf("8001 字消息应 400，得到 %d", w2.Code)
	}
}

// ---------- 限流 ----------

func TestLogin_RateLimit(t *testing.T) {
	r, _, _ := newTestApp(t)
	var wg sync.WaitGroup
	codes := make([]int, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := doJSON(r, "POST", "/api/auth/login", "", `{"email":"nobody@x.y","password":"wrongpw"}`)
			codes[i] = w.Code
		}(i)
	}
	wg.Wait()
	// IP 相同（127.0.0.1）：前 10 次 401（密码错误），之后 429
	ok401, got429 := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusUnauthorized:
			ok401++
		case http.StatusTooManyRequests:
			got429++
		}
	}
	if got429 == 0 {
		t.Errorf("12 次错误登录应触发限流 429，实际 401×%d", ok401)
	}
}

// ---------- Run 与 Refine/Rollback 互斥（锁语义验证） ----------

func TestProjectLock_MutexSemantics(t *testing.T) {
	r, _, uid := newTestApp(t)
	_ = r
	// 构造「生成中」项目行，验证锁语义：持有时 TryLock 失败、释放后成功
	ag := agent.NewAgent(nil, true)
	p := &store.Project{UserID: uid, Name: "p", Brief: "b", Status: "generating", CreatedAtMs: store.Now(), UpdatedAtMs: store.Now()}
	store.DB.Create(p)
	unlock := ag.LockProject(p.ID)
	if _, ok := ag.TryLockProject(p.ID); ok {
		t.Error("锁被持有时 TryLockProject 应返回 false")
	}
	unlock()
	if _, ok := ag.TryLockProject(p.ID); !ok {
		t.Error("释放后 TryLockProject 应返回 true")
	}
}

// ---------- 游客保留域：register 必须拒绝 @guest.atomix ----------

// TestRegister_RejectsGuestDomain
// 后台清理按 @guest.atomix 后缀级联删除过期游客；若正式注册可占用该域，
// 注册用户的账号会被清理任务误删。修复后 register 直接拒绝保留域。
func TestRegister_RejectsGuestDomain(t *testing.T) {
	r, _, _ := newTestApp(t)
	w := doJSON(r, "POST", "/api/auth/register", "", `{"email":"realuser@guest.atomix","password":"123456"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("注册 @guest.atomix 保留域应被拒绝(400)，实际 %d: %s", w.Code, w.Body.String())
	}
	// 正常域名不受影响
	w2 := doJSON(r, "POST", "/api/auth/register", "", `{"email":"realuser@example.com","password":"123456"}`)
	if w2.Code != http.StatusOK {
		t.Errorf("正常邮箱注册应成功(200)，实际 %d: %s", w2.Code, w2.Body.String())
	}
}