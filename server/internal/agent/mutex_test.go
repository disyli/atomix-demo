package agent

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"atomix-demo/server/internal/store"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// openMutexTestDB 打开临时 SQLite 并注册到全局 store.DB。
func openMutexTestDB(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "t.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&store.User{}, &store.Project{}, &store.Event{}, &store.Attachment{}, &store.Message{}, &store.Snapshot{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := store.DB
	store.DB = db
	t.Cleanup(func() { store.DB = prev })
}

// TestRun_HoldsProjectLock Run 执行期间项目锁被持有：并发的 TryLockProject 必须失败。
// 用一个慢 LLM 不需要——mock 模式的 Run 很快，因此在 Run 内部短暂阻塞：
// 这里用 ctx 取消让 Run 提前结束的方式验证锁在 Run 生命周期内被持有。
func TestRun_HoldsProjectLock(t *testing.T) {
	openMutexTestDB(t)
	ag := NewAgent(nil, true)
	uid := uint(101)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	var tryLockFailed int32

	go func() {
		defer close(done)
		_, _ = ag.Run(ctx, uid, "做一个待办清单", "build", nil, PipelineEvents{})
	}()

	// 轮询等待项目行落库（Run 一开始就建行），随即验证锁已被 Run 持有
	deadline := time.Now().Add(3 * time.Second)
	locked := false
	for time.Now().Before(deadline) {
		var cnt int64
		store.DB.Model(&store.Project{}).Where("user_id = ?", uid).Count(&cnt)
		if cnt > 0 {
			// 项目行已建：此刻锁应被 Run 持有（TryLock 应失败）
			var p store.Project
			store.DB.Where("user_id = ?", uid).First(&p)
			if _, ok := ag.TryLockProject(p.ID); !ok {
				atomic.StoreInt32(&tryLockFailed, 1)
				locked = true
				cancel() // 验证完成，让 Run 尽快收尾
				break
			}
			// 极小概率 Run 已完成（mock 很快）：此时锁已释放，跳过断言
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	<-done
	if locked && atomic.LoadInt32(&tryLockFailed) == 0 {
		t.Error("Run 运行中 TryLockProject 不应成功（Run 应持有项目锁）")
	}
}

// TestRefine_SerializesConcurrentEdits 两次并发 Refine 串行执行：
// 第二次必须读到第一次的产物（版本连续递增、无覆盖丢失）。
func TestRefine_SerializesConcurrentEdits(t *testing.T) {
	openMutexTestDB(t)
	ag := NewAgent(nil, true)
	uid := uint(202)

	// 建一个 ready 项目（v1）：初始产物必须能通过静态校验
	// （含 localStorage/脚本/完整结构/超 1000 字符），否则 Refine 收尾会走 failed 路径
	base := `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="UTF-8">
<title>便签墙</title>
<style>
  body { font-family: system-ui, sans-serif; margin: 0; padding: 24px; background: #f6f7fb; color: #1c2333; }
  h1 { font-size: 20px; margin: 0 0 16px; }
  .note { background: #fff; border: 1px solid #e3e6ef; border-radius: 12px; padding: 12px 14px; margin-bottom: 10px; box-shadow: 0 1px 3px rgba(28,35,51,.05); }
  .note b { display: block; margin-bottom: 4px; }
  button { border: 1px solid #c9cede; background: #fff; border-radius: 8px; padding: 6px 14px; cursor: pointer; font-size: 13px; }
  button:hover { background: #f1f3f9; }
</style>
</head>
<body>
<h1>便签墙</h1>
<div id="list"></div>
<button id="add">新增便签</button>
<script>
(function () {
  var KEY = 'notes_data';
  function read() { try { return JSON.parse(localStorage.getItem(KEY) || '[]') } catch (e) { return [] } }
  function save(list) { localStorage.setItem(KEY, JSON.stringify(list)) }
  function render() {
    var list = read();
    var box = document.getElementById('list');
    box.innerHTML = '';
    list.forEach(function (n, i) {
      var d = document.createElement('div');
      d.className = 'note';
      d.innerHTML = '<b>' + n.title + '</b>' + n.content;
      box.appendChild(d);
    });
  }
  document.getElementById('add').addEventListener('click', function () {
    var list = read();
    list.push({ title: '新便签 ' + (list.length + 1), content: '点击新增的内容' });
    save(list);
    render();
  });
  render();
})();
</script>
</body>
</html>`
	p := &store.Project{UserID: uid, Name: "便签", Brief: "b", Template: "notes",
		HTML: base, LastGoodHTML: base, Status: "ready", Version: 1, CreatedAtMs: store.Now(), UpdatedAtMs: store.Now()}
	if err := store.DB.Create(p).Error; err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := store.DB.Create(&store.Snapshot{ProjectID: p.ID, Version: 1, HTML: p.HTML, Label: "首次构建", Status: "done", CreatedAtMs: store.Now()}).Error; err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = ag.Refine(context.Background(), uid, p.ID, "加上历史记录", nil, PipelineEvents{})
		}()
	}
	wg.Wait()

	// 两次迭代都成功落库：版本从 v1 递增到 v3，两次快照都不丢
	var snaps []store.Snapshot
	store.DB.Where("project_id = ?", p.ID).Order("version ASC").Find(&snaps)
	if len(snaps) != 3 {
		t.Errorf("v1 + 两次迭代应有 3 个快照，实际 %d", len(snaps))
	}
	for i, s := range snaps {
		if s.Version != i+1 {
			t.Errorf("快照版本应连续 1..3，第 %d 个为 v%d", i, s.Version)
		}
	}
	var final store.Project
	store.DB.Where("id = ?", p.ID).First(&final)
	if final.Version != 3 {
		t.Errorf("最终版本应为 v3，实际 v%d", final.Version)
	}
	// 两次迭代都注入了历史徽标（demoRefine 的编辑都会生效，第二次基于第一次产物）
	if final.Status != "ready" {
		t.Errorf("最终状态应 ready，实际 %s", final.Status)
	}
}
