// Package tool 定义 Agent 可调用的工具协议、Registry 与执行内核。
package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mars/marspi-core/console"
)

// Result 是工具执行结果，对齐 ToolBase.ok/fail。
type Result struct {
	Success bool
	Content any // 通常是 string；view_image 返回 image 结构
	Extra   map[string]any
	// EndTurn 表示成功后结束当前轮次；业务任务是否完成由上层判断。
	EndTurn bool
}

// OK 构造成功结果。
func OK(content any) Result { return Result{Success: true, Content: content} }

// Fail 构造失败结果。
func Fail(content any) Result { return Result{Success: false, Content: content} }

// Param 描述一个工具参数。类型以字符串表达，"?" 后缀表示可选。
type Param struct {
	Name string
	Type string // "string" | "number" | "boolean"，可加 "?" 后缀表示可选
	Desc string
}

// Tool 是所有工具的统一接口，对齐 Python ToolBase。
type Tool interface {
	Name() string
	Description() string
	Params() []Param
	Run(args map[string]any) Result

	// Before 在确认前执行副作用（如 diff 预览）。
	Before(args map[string]any)
	// Confirm 返回是否需要用户放行（true 表示继续）。
	Confirm(args map[string]any) bool
	// Preview 返回工具调用的单行预览文本。
	Preview(args map[string]any) string
	// UseSpinner 表示执行期间是否显示 spinner。
	UseSpinner() bool
	// PreviewLines/PreviewWidth 控制结果回显的截断。
	PreviewLines() int
	PreviewWidth() int
}

// InputSchemaTool 可提供完整 JSON Schema；未实现时仍使用 Params。
// schema 仅描述模型入参，执行时的业务校验仍由工具负责。
type InputSchemaTool interface {
	InputSchema() map[string]any
}

// Base 提供 Tool 接口的默认实现，宿主工具通过嵌入复用。
type Base struct{}

func (Base) Params() []Param             { return nil }
func (Base) Before(map[string]any)       {}
func (Base) Confirm(map[string]any) bool { return true }
func (Base) UseSpinner() bool            { return false }
func (Base) PreviewLines() int           { return 20 }
func (Base) PreviewWidth() int           { return 100 }
func (Base) Preview(args map[string]any) string {
	for _, v := range args {
		return truncate(toStr(v), 100)
	}
	return ""
}

// Registry 保存工具集合并生成 OpenAI tools schema。
type Registry struct {
	host         console.Host
	order        []string
	tools        map[string]Tool
	builtinOrder []string
	builtinTools map[string]Tool
	providers    []Provider
}

// NewEmptyRegistry 构建不带内置工具的注册表，供上层按需注册能力。
// host 为 nil 时不输出 UI，也不会读取配置或环境变量。
func NewEmptyRegistry(host console.Host) *Registry {
	if host == nil {
		host = console.Nop{}
	}
	return &Registry{
		host:         host,
		tools:        map[string]Tool{},
		builtinTools: map[string]Tool{},
	}
}

// Register 追加一个工具，保持注册顺序（顺序影响 schema 顺序）。
func (r *Registry) Register(t Tool) {
	if _, ok := r.builtinTools[t.Name()]; !ok {
		r.builtinOrder = append(r.builtinOrder, t.Name())
	}
	r.builtinTools[t.Name()] = t
	r.rebuildIndex()
}

// AddProvider 增加一个工具提供方（MCP 等），并立即刷新一次索引。
func (r *Registry) AddProvider(p Provider) error {
	if err := p.Refresh(context.Background()); err != nil {
		return err
	}
	r.providers = append(r.providers, p)
	r.rebuildIndex()
	return nil
}

// RefreshProviders 刷新所有 provider 的工具定义并重建索引。
func (r *Registry) RefreshProviders(ctx context.Context) error {
	for _, p := range r.providers {
		if err := p.Refresh(ctx); err != nil {
			return err
		}
	}
	r.rebuildIndex()
	return nil
}

// Close 关闭所有 provider 资源（例如 MCP 子进程连接）。
func (r *Registry) Close() error {
	var firstErr error
	for _, p := range r.providers {
		if err := p.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Get 按名称取工具。
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// View 返回仅包含 allowlist 中工具的浅拷贝 Registry（共享同一 Tool 实例与 host）。
// allowlist 为空时返回包含全部工具的拷贝。用于 per-agent 工具隔离。
func (r *Registry) View(allowlist []string) *Registry {
	out := &Registry{
		host:         r.host,
		tools:        map[string]Tool{},
		builtinTools: map[string]Tool{},
		providers:    append([]Provider(nil), r.providers...),
	}
	allow := map[string]bool{}
	for _, n := range allowlist {
		allow[n] = true
	}
	filter := len(allow) > 0
	for _, name := range r.order {
		if filter && !allow[name] {
			continue
		}
		t := r.tools[name]
		out.order = append(out.order, name)
		out.tools[name] = t
		if _, ok := r.builtinTools[name]; ok {
			out.builtinOrder = append(out.builtinOrder, name)
			out.builtinTools[name] = t
		}
	}
	return out
}

// Names 返回注册顺序下的工具名。
func (r *Registry) Names() []string { return append([]string(nil), r.order...) }

// Schemas 生成 OpenAI function-calling 的 tools 数组。
// 自定义 schema 无法编码时 panic；需要处理配置错误的调用方使用 SchemasChecked。
func (r *Registry) Schemas() []map[string]any {
	out, err := r.SchemasChecked()
	if err != nil {
		panic(err)
	}
	return out
}

// SchemasChecked 与 Schemas 相同，但返回自定义 schema 的配置错误，供嵌入式运行时处理。
func (r *Registry) SchemasChecked() ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(r.order))
	for _, name := range r.order {
		schema, err := checkedSchemaOf(r.tools[name])
		if err != nil {
			return nil, err
		}
		out = append(out, schema)
	}
	return out, nil
}

func (r *Registry) rebuildIndex() {
	order := make([]string, 0, len(r.builtinOrder))
	tools := make(map[string]Tool, len(r.builtinTools))
	for _, name := range r.builtinOrder {
		t := r.builtinTools[name]
		order = append(order, name)
		tools[name] = t
	}
	for _, p := range r.providers {
		for _, t := range p.Tools() {
			name := t.Name()
			if _, exists := tools[name]; exists {
				continue
			}
			order = append(order, name)
			tools[name] = t
		}
	}
	r.order = order
	r.tools = tools
}

// schemaOf 依据 Tool.Params 生成单个 function schema，对齐 ToolBase.schema。
func schemaOf(t Tool) map[string]any {
	schema, err := checkedSchemaOf(t)
	if err != nil {
		panic(err)
	}
	return schema
}

func checkedSchemaOf(t Tool) (map[string]any, error) {
	if st, ok := t.(InputSchemaTool); ok {
		// 每次返回独立 JSON 值，调用方修改 schema 不会污染工具定义。
		raw, err := json.Marshal(st.InputSchema())
		if err != nil {
			return nil, fmt.Errorf("invalid input schema for %s: %w", t.Name(), err)
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil || schema == nil {
			return nil, fmt.Errorf("invalid input schema for %s: expected object schema", t.Name())
		}
		if typ, exists := schema["type"]; exists && typ != "object" {
			return nil, fmt.Errorf("invalid input schema for %s: expected object schema", t.Name())
		}
		return functionSchema(t, schema), nil
	}
	properties := map[string]any{}
	required := []string{}
	for _, p := range t.Params() {
		optional := len(p.Type) > 0 && p.Type[len(p.Type)-1] == '?'
		base := p.Type
		if optional {
			base = base[:len(base)-1]
		}
		jsonType := base
		if base == "number" {
			jsonType = "integer"
		}
		properties[p.Name] = map[string]any{"type": jsonType, "description": p.Desc}
		if !optional {
			required = append(required, p.Name)
		}
	}
	return functionSchema(t, map[string]any{
		"type": "object", "properties": properties, "required": required,
	}), nil
}

func functionSchema(t Tool, parameters map[string]any) map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        t.Name(),
			"description": t.Description(),
			"parameters":  parameters,
		},
	}
}
