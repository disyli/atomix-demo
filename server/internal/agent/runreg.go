package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrCanceled 用户主动停止任务时返回的唯一取消错误。
var ErrCanceled = errors.New("任务已被用户停止")

// runHandle 一个活跃构建任务的取消句柄。
type runHandle struct {
	userID uint
	cancel context.CancelFunc
}

// RunRegistry 活跃构建任务注册表：runID → 归属用户与取消函数。
// 前端停止按钮通过 POST /api/runs/:runId/cancel 触发取消（校验归属，只能停止自己的任务）。
type RunRegistry struct {
	mu   sync.Mutex
	runs map[string]*runHandle
}

// NewRunRegistry 创建任务注册表（挂在 Agent 上，随服务生命周期复用）。
func NewRunRegistry() *RunRegistry {
	return &RunRegistry{runs: map[string]*runHandle{}}
}

// Start 注册一个新的可取消任务：创建独立的取消 context（与 HTTP 请求生命周期解耦，
// 浏览器断开/刷新不会误杀后台构建），返回 runID 与任务 context。
func (r *RunRegistry) Start(userID uint) (string, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	id := fmt.Sprintf("run-%d", time.Now().UnixNano())
	r.mu.Lock()
	r.runs[id] = &runHandle{userID: userID, cancel: cancel}
	r.mu.Unlock()
	return id, ctx
}

// Cancel 取消指定任务。runID 不存在或归属不符返回 false（不能停止别人的任务）。
func (r *RunRegistry) Cancel(userID uint, runID string) bool {
	r.mu.Lock()
	h, ok := r.runs[runID]
	r.mu.Unlock()
	if !ok || h.userID != userID {
		return false
	}
	h.cancel()
	return true
}

// Remove 任务结束后清理注册项（handler defer 调用）。
func (r *RunRegistry) Remove(runID string) {
	r.mu.Lock()
	delete(r.runs, runID)
	r.mu.Unlock()
}
