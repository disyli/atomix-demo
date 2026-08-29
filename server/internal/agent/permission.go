package agent

import (
	"fmt"
	"sync"
	"time"
)

// ToolPerm 工具权限级别：allow 直接放行 / ask 需用户确认 / deny 拒绝执行。
type ToolPerm string

const (
	PermAllow ToolPerm = "allow"
	PermAsk   ToolPerm = "ask"
	PermDeny  ToolPerm = "deny"
)

// permConfirmTimeout 单次权限确认的等待上限，超时按拒绝处理，避免构建流程被无限挂起。
const permConfirmTimeout = 180 * time.Second

// defaultPerms 内置工具的默认权限表：写入类操作需用户确认，只读与流程类直接放行。
// 未注册的工具一律按 ask 处理（新工具默认最小权限）。
func defaultPerms() map[string]ToolPerm {
	return map[string]ToolPerm{
		"plan_app":    PermAllow,
		"write_file":  PermAsk,
		"edit_file":   PermAllow,
		"read_file":   PermAllow,
		"run_checks":  PermAllow,
		"commit_plan": PermAllow,
		"finish":      PermAllow,
	}
}

// permissionRequest 一次待确认的权限请求。
type permissionRequest struct {
	ID     string
	Tool   string
	Detail string
	ch     chan string // "allow" | "allow_session" | "reject"
}

// PermRegistry 跨构建任务的待确认权限索引：HTTP 确认接口按请求 ID 回填用户决定。
type PermRegistry struct {
	mu      sync.Mutex
	pending map[string]*permissionRequest
}

// NewPermRegistry 创建权限注册表（挂在 Agent 上，随服务生命周期复用）。
func NewPermRegistry() *PermRegistry {
	return &PermRegistry{pending: map[string]*permissionRequest{}}
}

func (r *PermRegistry) add(req *permissionRequest) {
	r.mu.Lock()
	r.pending[req.ID] = req
	r.mu.Unlock()
}

func (r *PermRegistry) remove(reqID string) {
	r.mu.Lock()
	delete(r.pending, reqID)
	r.mu.Unlock()
}

// Resolve 回填用户决定。请求不存在（已超时/已处理）返回 false。
func (r *PermRegistry) Resolve(reqID, action string) bool {
	r.mu.Lock()
	req, ok := r.pending[reqID]
	r.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case req.ch <- action:
		return true
	default:
		return false
	}
}

// permGateway 一次构建任务内的权限网关：持有默认规则与本次会话内用户授予状态。
// "允许并记住"的授权只在当前构建会话内生效，任务结束即失效。
type permGateway struct {
	mu      sync.Mutex
	base    map[string]ToolPerm
	granted map[string]bool
	reg     *PermRegistry
}

func newPermGateway(reg *PermRegistry) *permGateway {
	if reg == nil {
		reg = NewPermRegistry()
	}
	return &permGateway{base: defaultPerms(), granted: map[string]bool{}, reg: reg}
}

// authorize 判定一个工具调用能否执行。
// 返回 (是否放行, 拒绝时回喂给模型的观察文本)。
// ask 级工具先推送确认卡片（OnPermission）再阻塞等待用户决定；无确认通道的环境降级为放行并留痕。
func (g *permGateway) authorize(tool, detail string, ev PipelineEvents) (bool, string) {
	g.mu.Lock()
	if g.granted[tool] {
		g.mu.Unlock()
		return true, ""
	}
	perm, ok := g.base[tool]
	if !ok {
		perm = PermAsk
	}
	g.mu.Unlock()

	switch perm {
	case PermAllow:
		return true, ""
	case PermDeny:
		return false, "权限拒绝：工具 " + tool + " 被平台策略禁用，请改用其他方式完成任务。"
	default: // ask
	}
	if ev.OnPermission == nil {
		// 无确认通道（演示模式 / 单测）：降级放行但留下可审计的事件
		if ev.OnDetail != nil {
			ev.OnDetail("act", "工具 "+tool+" 属确认级权限，当前环境无确认通道，自动放行", "warn")
		}
		return true, ""
	}

	req := &permissionRequest{
		ID:     fmt.Sprintf("perm-%d", time.Now().UnixNano()),
		Tool:   tool,
		Detail: detail,
		ch:     make(chan string, 1),
	}
	g.reg.add(req)
	defer g.reg.remove(req.ID)

	ev.OnPermission(req.ID, tool, detail)
	select {
	case act := <-req.ch:
		switch act {
		case "allow":
			return true, ""
		case "allow_session":
			g.mu.Lock()
			g.granted[tool] = true
			g.mu.Unlock()
			return true, ""
		default:
			return false, "用户拒绝了本次 " + tool + " 操作。请调整方案：不要执行该写入，直接向用户说明情况并收尾。"
		}
	case <-time.After(permConfirmTimeout):
		return false, "权限确认等待超时（用户未响应），视为拒绝。请直接说明情况并收尾。"
	}
}
