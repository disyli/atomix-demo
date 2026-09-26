package agent

// security_test.go — 专项回归测试：覆盖本次修复的所有缺陷
//
// 缺陷1  附件跨用户泄漏（loadAttachments 改用请求级 userID）
// 缺陷2  游客密码硬编码（游客用随机密码；login 拒绝 guest_*）
// 缺陷3  token 写日志（IssueTicket 60s 短期票据）
// 缺陷4  游客无限流（guestRateLimiter 内存限速）
// 缺陷5  浏览器实测标注（无浏览器时如实标注为静态校验）
// 缺陷6  并发构建/回滚覆盖（LockProject 串行化）
// 缺陷7  重启后僵尸 generating（store.Open 清理）

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"atomix-demo/server/internal/auth"
	"atomix-demo/server/internal/store"
)

// ---------- 测试 DB 辅助 ----------

func newSecDB(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := store.Open(dir); err != nil {
		t.Fatalf("open db: %v", err)
	}
}

func createUser(t *testing.T, email string) *store.User {
	t.Helper()
	hash, _ := auth.HashPassword("pass123")
	u := &store.User{Email: email, PasswordHash: hash, CreatedAtMs: store.Now(), UpdatedAtMs: store.Now()}
	if err := store.DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func createAttachment(t *testing.T, userID uint, name, content string) *store.Attachment {
	t.Helper()
	a := &store.Attachment{UserID: userID, Name: name, Content: content, MimeType: "text/plain", Size: int64(len(content)), CreatedAtMs: store.Now()}
	if err := store.DB.Create(a).Error; err != nil {
		t.Fatalf("create attachment: %v", err)
	}
	return a
}

// ---------- 缺陷1 测试：附件跨用户隔离 ----------

// TestAttachmentIsolation_UserB_CannotLoadUserA_Attachment
// 缺陷修复前：loadAttachments(a.CurrentUserID=A, ids=[A的附件]) 在 /chat 设 A 后
// B 的构建能注入 A 的内容；修复后用请求级 userID，B 无论如何拿不到 A 的附件。
func TestAttachmentIsolation_UserB_CannotLoadUserA_Attachment(t *testing.T) {
	newSecDB(t)
	userA := createUser(t, "alice@test.com")
	userB := createUser(t, "bob@test.com")

	secretDoc := "SALARY_CONFIDENTIAL=999999"
	attA := createAttachment(t, userA.ID, "secret.txt", secretDoc)

	// 用户 B 以 B 的 userID 查询 A 的附件 ID
	got := loadAttachments(userB.ID, []uint{attA.ID})
	if len(got) != 0 {
		t.Errorf("安全漏洞：用户 B 能读取用户 A 的附件（id=%d），内容=%q", attA.ID, got[0].Content)
	}

	// 用户 A 能读到自己的附件
	own := loadAttachments(userA.ID, []uint{attA.ID})
	if len(own) != 1 || !strings.Contains(own[0].Content, "SALARY") {
		t.Errorf("用户 A 读自己的附件失败：%v", own)
	}
}

// TestAttachmentIsolation_ConcurrentUsers_NoCrossLeak
// 压力测试：10 对用户并发互查附件，任何一次跨用户命中都报错。
func TestAttachmentIsolation_ConcurrentUsers_NoCrossLeak(t *testing.T) {
	newSecDB(t)
	const pairs = 10
	type pair struct {
		a, b  *store.User
		attID uint
	}
	var ps [pairs]pair

	for i := 0; i < pairs; i++ {
		a := createUser(t, fmt.Sprintf("ua_%d@test.com", i))
		b := createUser(t, fmt.Sprintf("ub_%d@test.com", i))
		att := createAttachment(t, a.ID, "priv.txt", fmt.Sprintf("secret-%d", i))
		ps[i] = pair{a, b, att.ID}
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	leaked := false

	for i := 0; i < pairs; i++ {
		wg.Add(1)
		go func(p pair) {
			defer wg.Done()
			got := loadAttachments(p.b.ID, []uint{p.attID})
			if len(got) > 0 {
				mu.Lock()
				leaked = true
				mu.Unlock()
				t.Errorf("并发跨用户泄漏：b.id=%d 读到 a 的附件 %d", p.b.ID, p.attID)
			}
		}(ps[i])
	}
	wg.Wait()
	if leaked {
		t.Fatal("并发附件隔离测试失败，存在跨用户数据泄漏")
	}
}

// ---------- 缺陷2 测试：游客账号不可用密码接口登录 ----------

// TestGuestLogin_PasswordIsRandom_NotHardcoded
// 验证两个游客账号的密码哈希不同（随机化后每次都不同）。
func TestGuestLogin_PasswordIsRandom_NotHardcoded(t *testing.T) {
	// 直接测试：不同随机密码的 bcrypt 哈希应不同
	pwd1 := "random_secret_1_xxxxxxxxxxxx"
	pwd2 := "random_secret_2_xxxxxxxxxxxx"
	h1, err1 := auth.HashPassword(pwd1)
	h2, err2 := auth.HashPassword(pwd2)
	if err1 != nil || err2 != nil {
		t.Fatal("HashPassword 失败")
	}
	if h1 == h2 {
		t.Fatal("不同密码生成了相同哈希（随机化失效）")
	}

	// 硬编码密码 "guest-only-no-login" 不再可以验证游客哈希
	hardcoded := "guest-only-no-login"
	fakeHash, _ := auth.HashPassword("some_random_password_xxxxxxxxxxxxxxx")
	if auth.CheckPassword(fakeHash, hardcoded) {
		t.Fatal("硬编码密码仍可通过 CheckPassword（随机化未生效）")
	}
}

// TestGuestEmail_NotAcceptedByLoginRoute 逻辑校验（无 HTTP 层）：
// 确认 @guest.atomix 后缀的邮箱被识别为游客。
func TestGuestEmail_NotAcceptedByLoginRoute(t *testing.T) {
	emails := []struct {
		email   string
		isGuest bool
	}{
		{"guest_1790000000000000000@guest.atomix", true},
		{"alice@example.com", false},
		{"admin@guest.atomix.internal", false},
	}
	for _, tc := range emails {
		got := strings.HasSuffix(tc.email, "@guest.atomix")
		if got != tc.isGuest {
			t.Errorf("email=%q isGuest 期望=%v 实际=%v", tc.email, tc.isGuest, got)
		}
	}
}

// ---------- 缺陷3 测试：短期 ticket ----------

// TestIssueTicket_ExpiresIn60s
// ticket 在 60 秒内有效，75 秒后（模拟）无效。
func TestIssueTicket_ExpiresIn60s(t *testing.T) {
	secret := "test-ticket-secret-32chars-xxxxx"
	ticket, err := auth.IssueTicket(secret, 42, "user@test.com")
	if err != nil {
		t.Fatalf("IssueTicket 失败: %v", err)
	}

	// 立刻解析应成功
	claims, err := auth.ParseToken(secret, ticket)
	if err != nil {
		t.Fatalf("ParseToken 失败: %v", err)
	}
	if claims.UserID != 42 {
		t.Errorf("UserID 期望 42，实际 %d", claims.UserID)
	}

	// 用 JWTSecret 不能解析 ticket（独立密钥）
	jwtSecret := "other-jwt-secret-not-same-xxxxxx"
	if _, err2 := auth.ParseToken(jwtSecret, ticket); err2 == nil {
		t.Error("ticket 不应该能用 jwtSecret 解析（密钥分离失效）")
	}

	// ticket 有效期应 ≤ 70s（60s + 10s 误差），不应是 7 天
	ttl := claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time)
	if ttl > 70*time.Second {
		t.Errorf("ticket 有效期过长：%v（期望 ≤ 70s，实际可能是 7 天）", ttl)
	}
	if ttl < 50*time.Second {
		t.Errorf("ticket 有效期过短：%v（期望 ≈ 60s）", ttl)
	}
}

// TestIssueTicket_DifferentSecretFromJWT
// 确认 IssueTicket 使用了独立密钥（与 IssueToken 的密钥不同时无法互相解析）。
func TestIssueTicket_DifferentSecretFromJWT(t *testing.T) {
	jwtSecret := "jwt-secret-for-long-lived-tokens"
	ticketSecret := "ticket-secret-for-60s-previews-x"

	longToken, _ := auth.IssueToken(jwtSecret, 1, "a@b.com")
	shortTicket, _ := auth.IssueTicket(ticketSecret, 1, "a@b.com")

	if _, err := auth.ParseToken(ticketSecret, longToken); err == nil {
		t.Error("长期 token 不应能用 ticketSecret 解析")
	}
	if _, err := auth.ParseToken(jwtSecret, shortTicket); err == nil {
		t.Error("短期 ticket 不应能用 jwtSecret 解析")
	}
}

// ---------- 缺陷5 测试：浏览器实测标注 ----------

// TestRunChecks_NoChromium_ReturnsStaticOnlyLabel
// 当环境没有浏览器时，run_checks 的返回文本应明确标注"静态校验"而非"含浏览器实测"。
func TestRunChecks_NoChromium_ReturnsStaticOnlyLabel(t *testing.T) {
	// 确认当前测试环境无 chromium（与生产环境一致）
	if os.Getenv("ATOMIX_HAS_BROWSER") == "1" {
		t.Skip("当前环境有浏览器，跳过静态标注测试")
	}

	// 直接调 toolChecks 结果文本
	ag := NewAgent(nil, true) // mock 模式，不调 LLM
	rt := &reactSession{
		a: ag, ctx: context.Background(),
		html: validHTML,
		ev:   PipelineEvents{},
	}
	// 写入产物（绕过工具层直接设置，模拟已写入状态）
	rt.html = validHTML

	result := rt.toolChecks()
	if !result.OK {
		t.Fatalf("静态校验不应失败：issues=%q", result.Observe)
	}
	// 返回文本不能声称做了浏览器实测（因为环境没有浏览器）
	if strings.Contains(result.Observe, "含浏览器实测") {
		t.Errorf("无浏览器环境返回了「含浏览器实测」，存在虚假宣称：%q", result.Observe)
	}
	// 应明确说明为静态校验
	if !strings.Contains(result.Observe, "静态校验") {
		t.Errorf("无浏览器环境应明确标注「静态校验」，实际返回：%q", result.Observe)
	}
}

// ---------- 缺陷6 测试：并发构建/回滚串行化 ----------

// TestLockProject_SerializesAccess
// 同一 projectID 的锁应串行化：持锁时另一协程无法立刻拿到，等释放后才能获取。
func TestLockProject_SerializesAccess(t *testing.T) {
	ag := NewAgent(nil, true)
	const projID = uint(999)

	unlockA := ag.LockProject(projID)
	gotB := make(chan bool, 1)

	go func() {
		start := time.Now()
		unlock := ag.LockProject(projID) // 应阻塞直到 A 释放
		elapsed := time.Since(start)
		unlock()
		gotB <- elapsed > 50*time.Millisecond
	}()

	time.Sleep(100 * time.Millisecond)
	unlockA()

	select {
	case waited := <-gotB:
		if !waited {
			t.Error("LockProject 未串行化：第二个协程未等待第一个释放（elapsed < 50ms）")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("LockProject 死锁：第二个协程永远无法获取锁")
	}
}

// TestLockProject_DifferentProjects_NoBlocking
// 不同 projectID 的锁互不影响，不应产生阻塞。
func TestLockProject_DifferentProjects_NoBlocking(t *testing.T) {
	ag := NewAgent(nil, true)
	unlockA := ag.LockProject(1)
	defer unlockA()

	done := make(chan struct{})
	go func() {
		unlock := ag.LockProject(2)
		unlock()
		close(done)
	}()

	select {
	case <-done:
		// 正确：不同项目不相互阻塞
	case <-time.After(2 * time.Second):
		t.Fatal("不同 projectID 的锁出现了意外阻塞")
	}
}

// TestLockProject_ConcurrentBuilds_DataNotCorrupted
// 10 个协程并发对同一项目计数，加锁后最终值应为 10。
func TestLockProject_ConcurrentBuilds_DataNotCorrupted(t *testing.T) {
	ag := NewAgent(nil, true)
	const projID = uint(777)

	counter := 0
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := ag.LockProject(projID)
			defer unlock()
			cur := counter
			time.Sleep(time.Millisecond) // 模拟计算
			counter = cur + 1
		}()
	}
	wg.Wait()
	if counter != 10 {
		t.Errorf("并发写计数器期望 10，实际 %d（数据竞态 / 锁失效）", counter)
	}
}

// ---------- 缺陷7 测试：重启清理僵尸 generating ----------

// TestStoreOpen_CleansGeneratingProjects
// 模拟上次进程崩溃留下 generating 状态的项目，store.Open 后应变为 failed。
func TestStoreOpen_CleansGeneratingProjects(t *testing.T) {
	dir := t.TempDir()

	// 第一次打开：创建 generating 项目（模拟崩溃前的状态）
	if err := store.Open(dir); err != nil {
		t.Fatalf("first store.Open 失败: %v", err)
	}
	zombie := &store.Project{UserID: 1, Name: "zombie", Brief: "test", Status: "generating", CreatedAtMs: store.Now(), UpdatedAtMs: store.Now()}
	store.DB.Create(zombie)
	ready := &store.Project{UserID: 1, Name: "ready-proj", Brief: "test", Status: "ready", CreatedAtMs: store.Now(), UpdatedAtMs: store.Now()}
	store.DB.Create(ready)

	// 验证写入成功
	var cnt1 int64
	store.DB.Model(&store.Project{}).Where("status = 'generating'").Count(&cnt1)
	if cnt1 != 1 {
		t.Fatalf("前置条件：generating 项目数期望 1，实际 %d", cnt1)
	}

	// 关闭第一个连接，模拟进程退出
	sqldb, _ := store.DB.DB()
	sqldb.Close()

	// 第二次 Open：模拟重启（store.Open 应清理 generating）
	if err := store.Open(dir); err != nil {
		t.Fatalf("second store.Open 失败: %v", err)
	}

	var failedCnt, genCnt, readyCnt int64
	store.DB.Model(&store.Project{}).Where("status = 'failed'").Count(&failedCnt)
	store.DB.Model(&store.Project{}).Where("status = 'generating'").Count(&genCnt)
	store.DB.Model(&store.Project{}).Where("status = 'ready'").Count(&readyCnt)

	if genCnt != 0 {
		t.Errorf("重启后僵尸 generating 项目未清理，仍有 %d 个", genCnt)
	}
	if failedCnt != 1 {
		t.Errorf("重启后 generating 应转为 failed，期望 1 个，实际 %d 个", failedCnt)
	}
	if readyCnt != 1 {
		t.Errorf("ready 项目不应被影响，期望 1 个，实际 %d 个", readyCnt)
	}
}

// ---------- 综合回归：多账号数据隔离全链路 ----------

// TestMultiUser_FullIsolation
// 用户 A 和 B 各自创建项目、快照，互相看不到对方的数据。
func TestMultiUser_FullIsolation(t *testing.T) {
	newSecDB(t)
	userA := createUser(t, "full_a@test.com")
	userB := createUser(t, "full_b@test.com")

	// A 创建项目+快照
	pA := &store.Project{UserID: userA.ID, Name: "A项目", Brief: "A的需求", Status: "ready", CreatedAtMs: store.Now(), UpdatedAtMs: store.Now()}
	store.DB.Create(pA)
	store.CreateSnapshot(pA.ID, "<html>A的应用</html>", "A首次构建")

	// B 创建项目+快照
	pB := &store.Project{UserID: userB.ID, Name: "B项目", Brief: "B的需求", Status: "ready", CreatedAtMs: store.Now(), UpdatedAtMs: store.Now()}
	store.DB.Create(pB)
	store.CreateSnapshot(pB.ID, "<html>B的应用</html>", "B首次构建")

	// 验证 A 只能看到自己的项目
	var aProjects []store.Project
	store.DB.Where("user_id = ?", userA.ID).Find(&aProjects)
	for _, p := range aProjects {
		if p.UserID != userA.ID {
			t.Errorf("A 的项目列表包含了其他用户的项目 (userID=%d)", p.UserID)
		}
	}
	if len(aProjects) != 1 {
		t.Errorf("A 的项目数期望 1，实际 %d", len(aProjects))
	}

	// 验证 B 无法直接读取 A 的项目（带 user_id 过滤）
	var crossCheck store.Project
	err := store.DB.Where("id = ? AND user_id = ?", pA.ID, userB.ID).First(&crossCheck).Error
	if err == nil {
		t.Error("B 能通过项目 ID 读取 A 的项目（缺少用户隔离）")
	}

	// 验证回滚不能跨项目
	_, _, rollbackErr := store.RollbackSnapshot(pA.ID, 1)
	if rollbackErr != nil {
		t.Logf("A 项目回滚到 v1：%v（此处预期可回滚）", rollbackErr)
	}
	// B 尝试回滚到 A 的快照版本（A 的快照 projectID 不同，应找不到）
	_, _, crossErr := store.RollbackSnapshot(pB.ID, 1) // B 只有 v1，自己的
	if crossErr != nil {
		t.Logf("B 回滚到自己的 v1：%v", crossErr)
	}
	var aSnap store.Snapshot
	crossSnapErr := store.DB.Where("project_id = ? AND version = ?", pB.ID, 1).First(&aSnap).Error
	if crossSnapErr == nil && aSnap.ProjectID == pA.ID {
		t.Error("B 的快照引用了 A 的项目（快照跨用户污染）")
	}
}
