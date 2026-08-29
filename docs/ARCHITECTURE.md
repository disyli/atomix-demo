# Atomix 架构

> AI Agent 驱动的应用生成平台。用户用自然语言描述需求，Agent 经 ReAct 循环（规划 → 写码 → 校验 → 修复）生成单文件 HTML 应用，SSE 实时推送构建时间线，沙箱 iframe 实时预览。

## 总体架构

```mermaid
flowchart TB
    subgraph Client["浏览器 · Vue 3 SPA（web/）"]
        direction TB
        LV["LoginView<br/>注册 / 登录"]
        DV["DashboardView<br/>意图输入 · 模式选择 · 附件上传"]
        WV["WorkspaceView<br/>构建时间线 · 沙箱预览 · 迭代修改 · 历史回放"]
        APIJS["api/index.js<br/>fetch 封装 + SSE 消费"]
        LV & DV & WV --- APIJS
    end

    subgraph Server["Go 单体后端（server/，Gin）"]
        direction TB
        subgraph Edge["接入层"]
            MW["middleware<br/>JWT 鉴权（UserIdentity）"]
            HD["api/handlers.go<br/>14 个 REST/SSE 端点"]
        end

        subgraph AgentLayer["Agent 编排层（internal/agent）"]
            direction TB
            INTENT["intent.go<br/>闲聊/澄清/构建 意图路由"]
            REACT["react.go<br/>ReAct 循环 think→act→observe（≤12轮）<br/>live 真实循环 / demo 脚本化同构轨迹"]
            TOOLS["tools.go<br/>plan_app / write_file / edit_file / read_file<br/>run_checks / finish"]
            PERM["permission.go<br/>权限网关 + 用户确认卡片（HITL）"]
            BUDGET["compress.go<br/>上下文预算与压缩"]
            VERIFY["verify.go<br/>静态校验 + 无头浏览器实测"]
            SUB["subagent.go<br/>研究子 Agent（research 模式）"]
            PLAN["plan.go + templates_html.go<br/>模板匹配与降级渲染<br/>todo / notes / kanban"]
            INTENT --> REACT --> TOOLS
            REACT --- PERM
            REACT --- BUDGET
            REACT -.研究简报.-> SUB
            TOOLS -.校验.-> VERIFY
            TOOLS -.降级.-> PLAN
        end

        subgraph LLM["模型层（internal/llm）"]
            SVC["llm.Service 接口<br/>ChatJSON / ChatHTML / ChatWithTools"]
            DS["deepseek.go<br/>deepseek-v4-flash-vision-exp（多模态）"]
            MOCK["mock（UseMock）<br/>无 Key 自动降级演示模式"]
            SVC --> DS
            SVC --> MOCK
        end

        subgraph Store["持久层（internal/store，GORM）"]
            DB[("SQLite<br/>users / projects / events / attachments")]
        end
    end

    SANDBOX["沙箱 iframe<br/>sandbox 属性隔离<br/>localStorage 垫片 + cookie 拦截"]

    APIJS -->|"HTTPS REST / SSE"| MW --> HD
    HD -->|"构建请求"| INTENT
    HD -->|"CRUD / 预览"| DB
    REACT -->|"附件注入多模态 parts"| SVC
    REACT -->|"事件回调 OnStage/OnDetail"| HD
    TOOLS -->|"产物/校验事件落库"| DB
    WV -->|"GET /projects/:id/preview"| SANDBOX

    DS -.->|"OpenAI 兼容 API"| EXT["DeepSeek 云端"]
```

## 生成流水线（时序）

```mermaid
sequenceDiagram
    autonumber
    actor U as 用户
    participant DV as DashboardView
    participant HD as Gin API
    participant DB as SQLite
    participant AG as Agent(ReAct)
    participant LLM as DeepSeek
    participant WV as WorkspaceView

    U->>DV: 输入需求（可带附件）
    DV->>HD: POST /api/chat（意图识别）
    HD->>LLM: 意图判断
    LLM-->>HD: intent = build
    DV->>WV: 跳转工作区
    WV->>HD: GET /api/generate?brief=…（SSE 长连接）
    HD->>DB: 创建项目行（status=generating，生成中即可见）
    HD->>AG: Run(brief, mode, attachmentIDs)
    loop ReAct 循环（≤12 轮，校验失败自动修复 ≤3 次）
        AG->>LLM: ChatWithTools（含附件多模态上下文）
        LLM-->>AG: 思考文本 + 工具调用
        AG->>AG: 权限网关拦截敏感工具 → 推送确认卡片（HITL）
        U-->>AG: allow / allow_session / reject（HTTP 回填）
        AG->>AG: 执行工具 plan_app → write_file → run_checks → finish
        AG-->>WV: SSE 推送 think / act / observe 事件（同步落库）
        AG-->>LLM: 工具观察结果回喂
    end
    AG->>DB: 回填产物 HTML（status=ready）
    AG-->>WV: stage:done 构建完成
    WV->>HD: GET /api/projects/:id/preview
    HD-->>WV: HTML → 沙箱 iframe 渲染
    U->>WV: 迭代修改（POST /projects/:id/refine，读取旧产物最小化修改）
```

## 目录结构

```
atomix-demo/
├── web/                        # Vue 3 + Vite 前端
│   └── src/
│       ├── views/              # Login / Dashboard / Workspace 三视图
│       ├── api/index.js        # fetch 封装 + SSE 消费
│       └── router/             # 路由（login/dashboard/workspace）
├── server/                     # Go 单体后端（Gin + GORM + SQLite）
│   ├── main.go                 # 入口：装配依赖，静态资源托管
│   └── internal/
│       ├── api/                # 14 个 REST/SSE 端点
│       ├── middleware/         # JWT 鉴权
│       ├── agent/              # 意图路由 + ReAct 循环 + 工具集 + 模板
│       ├── llm/                # Service 接口 + DeepSeek 实现（双模式）
│       ├── store/              # GORM 模型（users/projects/events/attachments）
│       ├── auth/               # bcrypt + JWT 签发
│       └── config/             # 环境变量装配（含 UseMock 判定）
├── Dockerfile                  # 前端构建产物内嵌进 Go 二进制，单容器部署
└── docs/
```

## API 端点

| 方法 | 路径 | 鉴权 | 说明 |
| --- | --- | --- | --- |
| GET | `/api/health` | - | 健康检查（返回 live/demo 模式） |
| POST | `/api/auth/register` | - | 注册（bcrypt 哈希） |
| POST | `/api/auth/login` | - | 登录（签发 JWT） |
| GET | `/api/me` | JWT | 当前用户信息 |
| GET/POST | `/api/projects` | JWT | 项目列表 / 创建 |
| GET | `/api/projects/:id` | JWT | 项目详情（含产物 HTML） |
| GET | `/api/projects/:id/events` | JWT | 构建事件（历史回放） |
| GET | `/api/projects/:id/preview` | JWT | 产物 HTML（沙箱预览） |
| POST | `/api/projects/:id/refine` | JWT/SSE | 迭代修改（SSE 推送） |
| GET | `/api/generate` | JWT/SSE | 自然语言生成（SSE 实时时间线） |
| POST | `/api/chat` | JWT | 意图路由（闲聊/澄清/构建） |
| POST/GET | `/api/attachments` | JWT | 附件上传 / 列表 |
| GET | `/api/attachments/:id` | JWT | 附件内容（图片转 dataURL 多模态注入） |
| POST | `/api/permissions/:reqId` | JWT | 权限确认回填（allow / allow_session / reject） |

## 关键设计

- **三模式构建**：`build` 标准构建 / `plan` 两阶段规划先行（工具层门控：plan_app、commit_plan 之外物理不可见，commit_plan 提交后解锁实施工具） / `research` 研究子 Agent 独立上下文拆解需求，简报注入主循环构建。
- **权限网关（HITL）**：`write_file` / `edit_file` 等 ask 级工具调用前推送确认卡片；用户 allow / allow_session / reject，HTTP 接口按请求 ID 回填注册表解除阻塞，超时或拒绝则把拦截结果回喂模型。
- **上下文压缩**：消息历史接近预算时自动压缩为状态摘要，保留 system / 需求 / 最近消息，长任务不爆上下文。
- **自修复闭环**：`run_checks` 静态校验 + 无头浏览器实测运行时异常，失败优先 `edit_file` 片段级精准修复（≤3 次），整体性缺陷才 `write_file` 重写（成功写入 ≤2 次）。
- **事件双写**：SSE 实时推送的同时落库 `events` 表，历史项目可完整回放 ReAct 轨迹。
- **项目行先行落库**：构建任务开始前即创建 `status=generating` 的项目行，修复"生成中不可见"问题。
- **沙箱安全**：产物在 `sandbox` 属性 iframe 中运行；`document.cookie` 在校验层与提示词双重拦截，`localStorage` 不可用时平台自动垫片降级。
- **多模态附件**：图片附件以 `image_url` parts 注入 vision 模型，文本附件并入用户消息。
- **防护栏**：ReAct ≤12 轮（plan 模式 +4）、自动修复 ≤3 次。

## 部署拓扑

```mermaid
flowchart LR
    U["用户浏览器"] -->|":80"| NGINX["Docker 容器 atomix-demo<br/>Go 二进制（静态资源内嵌）<br/>:8080"]
    NGINX --> V[("/opt/atomix-data<br/>SQLite 数据卷")]
    NGINX -.->|"DEEPSEEK_API_KEY"| DS["DeepSeek API"]
```
