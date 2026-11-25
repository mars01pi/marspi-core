# marspi-core

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue)](LICENSE)
[![MCP](https://img.shields.io/badge/MCP-1.6-blueviolet)](https://github.com/modelcontextprotocol/go-sdk)

> Marspi 单智能体运行库 — ReAct 循环、LLM Provider、工具/MCP 插件、上下文压缩、记忆、技能

Core 是 `marspi-cli` 和 `marspi-graph` 的底层依赖。**不依赖任何 TUI 或 CLI 包**，可嵌入任何 Go 应用使用。

---

## 📦 Packages

| 包 | 角色 |
|------|------|
| [`agent`](./agent/) | **ReAct 主循环**：Runner、事件发射器、LLM 调用 + 工具执行编排 |
| [`agentctx`](./agentctx/) | **上下文管理**：消息追加、三级压缩（micro / session / full compact） |
| [`llm`](./llm/) | **LLM Provider 抽象**：OpenAI 兼容 API、流式 SSE、Smart Routing、累加器 |
| [`tool`](./tool/) | **工具内核**：Tool 接口、Registry、Provider、schema、执行与取消 |
| [`mcp`](./mcp/) | **MCP 集成**：stdio MCP 服务器管理，tools/list + tools/call 协议 |
| [`console`](./console/) | **控制台抽象**：Reporter 接口（Nop / Text / TUI），供 CLI 层注入 |
| [`config`](./config/) | **配置加载**：`MARS_*` 环境变量、持久化路径、版本号 |
| [`agentctx`](./agentctx/) | **上下文压缩**：micro → session → full compact 三级策略 |
| [`memory`](./memory/) | **长期记忆**：Markdown 文件按日追加，关键词打分检索 |
| [`skill`](./skill/) | **技能系统**：SKILL.md 解析加载，按名称/标签检索 |
| [`flash`](./flash/) | **思考框架**：关键词触发结构化思维引导（debug / design / explain / optimize / implement） |
| [`logx`](./logx/) | **日志工具**：调试日志条件输出 |

---

## 🚀 快速开始

```bash
go build ./...
go test ./...
```

> `marspi-core` 是库项目，不提供独立二进制。通过 `marspi-cli` 或自定义 Go 应用使用。

---

## 🧠 ReAct 循环

核心是 `agent.Runner`，承载完整的 **LLM 调用 → 工具执行 → 结果反馈** 循环：

```
LLM 调用 → 解析响应
     ├── 有 ToolCall → 执行工具 → 反馈结果 → 继续 LLM 调用
     └── 有 Content  → 返回最终输出
```

```go
runner := agent.Runner{
    Provider:   provider,
    Registry:   registry,
    Console:    console,
    Events:     emitter,
    MaxIter:    100,
}

result := runner.Run(ctx, agent.RunConfig{
    Messages:    msgs,
    System:      systemPrompt,
})
```

### 事件系统

循环过程中通过 `Emitter` 发布细粒度事件，供 TUI / 日志层消费：

| 事件 | 触发时机 |
|------|---------|
| `EventRunStart` / `EventRunEnd` | 整次运行开始/结束 |
| `EventTurnStart` / `EventTurnEnd` | 单轮 LLM 交互 |
| `EventLLMStart` / `EventLLMEnd` | LLM 请求开始/完成 |
| `EventMessageDelta` / `EventMessageEnd` | 流式消息增量/结束 |
| `EventToolStart` / `EventToolUpdate` / `EventToolEnd` | 工具调用生命周期 |

---

## 🔌 LLM Provider

支持 OpenAI 兼容 API（DeepSeek、GPT、Claude 等），通过 `llm.Provider` 接口统一：

```go
type Provider interface {
    APIURL() string
    Headers() map[string]string
    BuildBody(msgs, tools) map[string]any
    ParseResponse(raw) Response
    // ...
}
```

| 特性 | 说明 |
|------|------|
| **流式 SSE** | 实时输出 content / reasoning 增量 |
| **Smart Routing** | 关键词 + LLM 混合评分，自动在 low / medium / high 三级模型间路由 |
| **累加器** | SSE 流式分片合并为完整 Message |

---

## 🛠 工具系统

### Tool 接口

```go
type Tool interface {
    Name() string
    Description() string
    Params() []Param
    Run(args map[string]any) Result
    Before(args map[string]any)
    Confirm(args map[string]any) bool
    Preview(args map[string]any) string
    UseSpinner() bool
    PreviewLines() int
    PreviewWidth() int
}
```

宿主工具可嵌入 `tool.Base` 复用可选行为的默认实现。实现 `tool.ContextualTool` 后可通过 `RunCtx` 接收取消信号和 `ExecutionMeta`。

### 显式注册工具

```go
registry := tool.NewEmptyRegistry(host)
registry.Register(myTool)
registry.Register(tool.NewAttemptCompletionTool())
```

注册表初始为空，由宿主决定能力集合。Core 保留通用的 `attempt_completion` 工厂，其成功结果通过 `Result.EndTurn` 结束当前轮次。文件、Shell、Web、图片、Memory、Skill、RAG 工具及其安全策略由 `marspi-cli/internal/tools` 维护。

迁移旧调用时，使用 `NewEmptyRegistry(host)` 加显式 `Register` 替换已移除的 `tool.NewRegistry(cfg, host, mem, skills)`。`Registry.RegisterKnowledge` 也已移除；宿主注入 Retriever 后注册自己的检索工具。`tool` 和 `agent` 的生产依赖不再包含 `config`、`memory`、`rag`、`skill`。

### Provider 抽象

```go
type Provider interface {
    ID() string
    Tools() []Tool
    Refresh(ctx) error
    Close() error
}
```

宿主工具通过 `Registry.Register` 注册；MCP 工具通过 `mcp.Provider` 动态加载，注册表负责刷新和关闭工具提供方。

---

## 🔗 MCP 集成

`mcp.Provider` 通过 stdio 协议管理 MCP 服务器：

- tools/list → 映射为 `tool.Tool`
- tools/call → 代理执行结果
- 60s 超时，自动清理异常进程

```go
mcpProvider, _ := mcp.NewProvider(mcp.Config{
    Servers: map[string]mcp.ServerConfig{
        "my-mcp": {Command: "npx", Args: []string{"my-mcp-server"}},
    },
})
registry.RegisterProvider(mcpProvider)
```

---

## 🗜 上下文压缩

三级压缩策略，自动管理 token 上限：

| 级别 | 触发条件 | 操作 |
|------|---------|------|
| **Micro** | 每条消息追加后 | 丢弃末尾 n 条非 user 消息 |
| **Session** | token 超限 | 保留系统提示 + 最后 k 轮对话 + 其余摘要 |
| **Full Compact** | Session 后仍超限 | 调用 LLM 对整个对话生成浓缩摘要 |

---

## 💾 长期记忆

基于 Markdown 的持久化记忆：

- 按日期分文件存储
- 写入时关键词提取 + 打分
- 检索时关键词匹配 + 时间权重排序

```go
mm := memory.New(dir)
mm.Append("用户偏好：使用 VS Code 编辑器")
results := mm.Search("editor preference", 5)
```

---

## 🧩 技能系统

解析 SKILL.md 文件，支持：

- **元数据**：名称、描述、标签
- **脚本**：安装/测试等可执行脚本
- **引用**：关联文件路径

---

## 💭 思考框架（Flash）

根据用户输入关键词自动选择结构化思维模板：

| 框架 | 触发关键词 | 用途 |
|------|-----------|------|
| `debug` | bug, error, 报错, crash, fix… | 问题排查 |
| `design` | 设计, architect, 架构, refactor… | 架构设计 |
| `explain` | 什么是, explain, what is, read… | 解释说明 |
| `optimize` | 优化, performance, 性能… | 性能优化 |
| `implement` | 实现, implement, create, build… | 代码实现 |

---

## 📁 项目结构

```
marspi-core/
├── agent/               # ReAct 主循环
│   ├── loop.go          # Runner：LLM + 工具编排
│   ├── loop_llm.go      # LLM 调用封装
│   ├── loop_tool.go     # 工具执行调度
│   └── event.go         # 事件类型 + Emitter
├── agentctx/            # 上下文管理
│   ├── manager.go       # 消息管理器
│   ├── compact.go       # Micro + Session 压缩
│   ├── fullcompact.go   # LLM 全量压缩
│   ├── persist.go       # 原子写入持久化
│   └── analyze.go       # Token 分析
├── llm/                 # LLM Provider
│   ├── provider.go      # Provider 接口
│   ├── openai.go        # OpenAI 兼容实现
│   ├── routed.go        # Smart Routing
│   ├── sse.go           # SSE 流式解析
│   ├── stream.go        # 流式累加器
│   └── types.go         # 消息/响应/工具调用类型
├── tool/                # 工具系统
│   ├── tool.go          # Tool、Result、Registry、schema
│   ├── provider.go      # Provider 抽象
│   ├── execute.go       # 执行调度
│   ├── execution_meta.go # 取消与编排元数据接口
│   ├── console.go       # 宿主输出接口别名
│   ├── preview.go       # 默认参数预览
│   └── completion.go    # 通用轮次结束工具
├── mcp/                 # MCP 集成
│   ├── provider.go      # MCP Provider（stdio）
│   └── config.go        # MCP 服务器配置
├── memory/              # 长期记忆
│   └── memory.go        # Manager（Markdown）
├── skill/               # 技能系统
│   └── skill.go         # SKILL.md 加载
├── flash/               # 思考框架
│   └── flash.go         # 关键词匹配 + 模板
├── config/              # 配置加载
│   └── config.go        # MARS_* 环境变量
├── console/             # 控制台抽象
│   └── console.go       # Reporter 接口
├── logx/                # 日志工具
│   └── logx.go          # 调试日志
└── prompt/              # 系统提示词
    └── system.go        # 提示词组装
```

---

## 📐 依赖规则

```
cli → graph → core    （core 不依赖 cli 或 graph）
```

`marspi-core` 位于依赖链最底层，零外部 TUI/CLI 依赖。唯一外部依赖是 `modelcontextprotocol/go-sdk`（MCP 协议）。

---

## 🛠 开发

```bash
go test ./...            # 运行全部测试
go test -race ./...      # 竞态检测
go vet ./...             # 静态分析
```

---

## 📄 License

[Apache License 2.0](LICENSE)
