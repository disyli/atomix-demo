// Package api 提供 HTTP 接口。
package api

import (
	"net/http"
	"strings"
	"time"

	"atomix-demo/server/internal/agent"
	"atomix-demo/server/internal/auth"
	"atomix-demo/server/internal/config"
	"atomix-demo/server/internal/middleware"
	"atomix-demo/server/internal/store"

	"github.com/gin-gonic/gin"
)

// Handlers 集中所有路由处理器。
type Handlers struct {
	Cfg   *config.Config
	Agent *agent.Agent
}

// Register 注册全部路由。
func Register(r *gin.Engine, h *Handlers) {
	r.Use(cors())
	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "mode": modeName(h.Agent.UseMock), "time": time.Now().UnixMilli()})
	})

	api := r.Group("/api")
	api.POST("/auth/register", h.register)
	api.POST("/auth/login", h.login)

	authed := api.Group("", middleware.UserIdentity(h.Cfg.JWTSecret))
	authed.GET("/me", h.me)
	authed.GET("/projects", h.listProjects)
	authed.POST("/projects", h.createProject)
	authed.GET("/projects/:id", h.getProject)
	authed.GET("/projects/:id/events", h.getEvents)
	authed.GET("/projects/:id/preview", h.previewHTML)
	authed.GET("/generate", h.generateSSE)
}

func modeName(useMock bool) string {
	if useMock {
		return "demo"
	}
	return "live"
}

func cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

type credReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handlers) register(c *gin.Context) {
	var req credReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Email == "" || len(req.Password) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "邮箱不能为空，密码至少 6 位"})
		return
	}
	var cnt int64
	store.DB.Model(&store.User{}).Where("email = ?", req.Email).Count(&cnt)
	if cnt > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "该邮箱已注册"})
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
		return
	}
	u := &store.User{Email: req.Email, PasswordHash: hash, CreatedAtMs: store.Now(), UpdatedAtMs: store.Now()}
	if err := store.DB.Create(u).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建用户失败"})
		return
	}
	token, _ := auth.IssueToken(h.Cfg.JWTSecret, u.ID, u.Email)
	c.JSON(http.StatusOK, gin.H{"token": token, "user": gin.H{"id": u.ID, "email": u.Email}})
}

func (h *Handlers) login(c *gin.Context) {
	var req credReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	var u store.User
	if err := store.DB.Where("email = ?", req.Email).First(&u).Error; err != nil || !auth.CheckPassword(u.PasswordHash, req.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "邮箱或密码错误"})
		return
	}
	token, _ := auth.IssueToken(h.Cfg.JWTSecret, u.ID, u.Email)
	c.JSON(http.StatusOK, gin.H{"token": token, "user": gin.H{"id": u.ID, "email": u.Email}})
}

func (h *Handlers) me(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"id": middleware.UID(c), "email": c.GetString("email")})
}

func (h *Handlers) listProjects(c *gin.Context) {
	var ps []store.Project
	store.DB.Where("user_id = ?", middleware.UID(c)).Order("id DESC").Find(&ps)
	out := make([]gin.H, 0, len(ps))
	for _, p := range ps {
		out = append(out, projectBrief(p))
	}
	c.JSON(http.StatusOK, out)
}

func projectBrief(p store.Project) gin.H {
	return gin.H{
		"id": p.ID, "userId": p.UserID, "name": p.Name, "brief": p.Brief,
		"template": p.Template, "status": p.Status,
		"createdAt": p.CreatedAtMs, "updatedAt": p.UpdatedAtMs,
	}
}

func (h *Handlers) createProject(c *gin.Context) {
	var req struct {
		Brief string `json:"brief"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Brief == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需求描述不能为空"})
		return
	}
	p := &store.Project{UserID: middleware.UID(c), Brief: req.Brief, Status: "draft", CreatedAtMs: store.Now(), UpdatedAtMs: store.Now()}
	if err := store.DB.Create(p).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	c.JSON(http.StatusOK, projectBrief(*p))
}

func (h *Handlers) getProject(c *gin.Context) {
	var p store.Project
	if err := store.DB.Where("id = ? AND user_id = ?", c.Param("id"), middleware.UID(c)).First(&p).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "项目不存在"})
		return
	}
	c.JSON(http.StatusOK, projectBrief(p))
}

func (h *Handlers) getEvents(c *gin.Context) {
	var p store.Project
	if err := store.DB.Where("id = ? AND user_id = ?", c.Param("id"), middleware.UID(c)).First(&p).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "项目不存在"})
		return
	}
	var es []store.Event
	store.DB.Where("project_id = ?", p.ID).Order("id ASC").Find(&es)
	if es == nil {
		es = []store.Event{}
	}
	c.JSON(http.StatusOK, es)
}

// previewHTML 以独立文档形式返回生成的应用（供 iframe srcdoc/src 使用）。
// 沙箱 iframe 无 allow-same-origin 时产物访问 localStorage 会抛 SecurityError，
// 这里在 <head> 前注入存储垫片：探测失败则以内存存储降级并通知父页面。
func (h *Handlers) previewHTML(c *gin.Context) {
	var p store.Project
	if err := store.DB.Where("id = ? AND user_id = ?", c.Param("id"), middleware.UID(c)).First(&p).Error; err != nil {
		c.String(http.StatusNotFound, "project not found")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(injectStorageShim(p.HTML)))
}

// injectStorageShim 在产物 HTML 中注入沙箱存储垫片。
// 只处理一次：已有垫片标记的文档直接原样返回。
func injectStorageShim(html string) string {
	const marker = "<!--atomix-storage-shim-->"
	if strings.Contains(html, marker) {
		return html
	}
	headIdx := strings.Index(strings.ToLower(html), "<head")
	if headIdx < 0 {
		// 无 <head> 结构的产物，整体包裹一层保证垫片最先执行
		return marker + shimScript + html
	}
	// 插入点在 <head ...> 标签结束之后
	insertAt := strings.Index(html[headIdx:], ">")
	if insertAt < 0 {
		return marker + shimScript + html
	}
	insertAt = headIdx + insertAt + 1
	return html[:insertAt] + shimScript + html[insertAt:]
}

// shimScript 存储垫片脚本：在沙箱产物自身代码执行前运行。
// - 探测 localStorage 可用性（Chrome 内核下 opaque origin 直接访问即抛错）
// - 不可用时以内存 Map 实现完整 localStorage 接口语义，并对 window.sessionStorage 做同样保护
// - 历史数据可经 location.hash（#atomix-data=<urlencoded JSON>）恢复
// - 写入操作通过 postMessage 通知父页面，由父页面持久化
const shimScript = `
<!--atomix-storage-shim-->
<script>
(function () {
  var mem = {};
  function tryNative() {
    try {
      var w = window.localStorage;
      w.setItem('__atomix_probe__', '1');
      w.removeItem('__atomix_probe__');
      return w;
    } catch (e) { return null; }
  }
  function restoreFromHash(store) {
    try {
      var m = /(?:^|&)atomix-data=([^&]*)/.exec(location.hash.slice(1));
      if (m && m[1]) {
        var data = JSON.parse(decodeURIComponent(m[1]));
        for (var k in data) { store.setItem(k, data[k]); }
      }
    } catch (e) {}
  }
  function makeMemoryStore(native) {
    var nativeOK = !!native;
    function get(k) { return Object.prototype.hasOwnProperty.call(mem, k) ? mem[k] : null; }
    function set(k, v) {
      mem[k] = String(v);
      try { parent.postMessage({ source: 'atomix-shim', type: 'storage', key: k, value: mem[k] }, '*'); } catch (e) {}
    }
    function remove(k) {
      delete mem[k];
      if (nativeOK) { try { native.removeItem(k); } catch (e) {} }
      try { parent.postMessage({ source: 'atomix-shim', type: 'storage', key: k, value: null }, '*'); } catch (e) {}
    }
    var store = {
      getItem: get,
      setItem: set,
      removeItem: remove,
      clear: function () {
        mem = {};
        if (nativeOK) { try { native.clear(); } catch (e) {} }
        try { parent.postMessage({ source: 'atomix-shim', type: 'clear' }, '*'); } catch (e) {}
      },
      key: function (i) {
        var ks = Object.keys(mem);
        return i >= 0 && i < ks.length ? ks[i] : null;
      }
    };
    Object.defineProperty(store, 'length', { get: function () { return Object.keys(mem).length; } });
    restoreFromHash(store);
    return store;
  }
  var native = tryNative();
  if (native) {
    // 原生存储可用（如新窗口打开、或沙箱允许同源）：保留原生行为，仅尝试从 hash 恢复历史数据
    restoreFromHash(native);
  } else {
    var shim = makeMemoryStore(null);
    try {
      Object.defineProperty(window, 'localStorage', { value: shim, writable: true, configurable: true });
    } catch (e) {}
  }
  try {
    window.sessionStorage.getItem('__atomix_probe__');
  } catch (e) {
    var sMem = {};
    var sess = {
      getItem: function (k) { return Object.prototype.hasOwnProperty.call(sMem, k) ? sMem[k] : null; },
      setItem: function (k, v) { sMem[k] = String(v); },
      removeItem: function (k) { delete sMem[k]; },
      clear: function () { sMem = {}; },
      key: function (i) {
        var ks = Object.keys(sMem);
        return i >= 0 && i < ks.length ? ks[i] : null;
      }
    };
    Object.defineProperty(sess, 'length', { get: function () { return Object.keys(sMem).length; } });
    try {
      Object.defineProperty(window, 'sessionStorage', { value: sess, writable: true, configurable: true });
    } catch (e) {}
  }
  try { parent.postMessage({ source: 'atomix-shim', type: 'ready' }, '*'); } catch (e) {}
})();
</script>
`
