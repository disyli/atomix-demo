# Atomix Demo

一个 **AI Agent 驱动的应用生成平台** Demo：用自然语言描述需求，Agent 经「规划 → 构建 → 运行 → 校验」流水线生成一个真实可交互的网页应用，并在沙箱中实时预览。生成结果持久化保存，可随时回看。

> 本项目为 ROOT 团队 Atoms-Demo 笔试作品。

## 功能

- 注册 / 登录（JWT + bcrypt，密码哈希存储）
- 自然语言 → 应用生成，SSE 实时推送 Agent 五阶段执行时间线
- 生成产物为单文件 HTML 应用，沙箱 iframe 实时预览，支持新窗口独立打开
- 应用数据 `localStorage` 持久化（刷新不丢）
- 项目历史持久化（服务端 SQLite），可回看任意一次生成
- 双模式运行：
  - `live` 模式：配置 `DEEPSEEK_API_KEY` 后由 DeepSeek 生成计划与应用代码
  - `demo` 模式：未配置 Key 时自动降级，由内置规则引擎 + 模板生成，全流程可体验

内置应用模板：极简待办清单 / 彩色便签墙 / 轻量项目看板（Agent 根据需求语义自动选择）。

## 快速开始

```bash
# 1. 启动后端（demo 模式，无需 API Key）
cd server && go run .

# 2. 启动前端
cd web && npm install && npm run dev
```

打开 http://localhost:5173 → 注册 → 输入需求 → 开始生成。

### 启用 DeepSeek

```bash
export DEEPSEEK_API_KEY=sk-xxx
cd server && go run .
```

### 生产构建

```bash
cd web && npm run build && cp -r dist ../server/static
cd server && go run .
```

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ATOMIX_PORT` | `51720` | 服务端口 |
| `ATOMIX_DATA_DIR` | `server/data` | SQLite 数据目录 |
| `ATOMIX_JWT_SECRET` | 内置开发密钥 | JWT 签名密钥（生产请务必修改） |
| `DEEPSEEK_API_KEY` | 空 | 留空则进入演示模式 |
| `DEEPSEEK_BASE_URL` | `https://api.deepseek.com` | API 地址 |
| `DEEPSEEK_MODEL` | `deepseek-chat` | 模型名 |

## 技术栈

Vue 3 + Vite + Pinia + Vue Router | Go (Gin + GORM + SQLite) | DeepSeek API | SSE
