// Package console 定义 core 运行时对终端/宿主输出的最小接口。
// 具体实现由消费方（如 marspi-cli 的 ui.Printer）提供，core 不依赖 TUI。
package console

// Reporter 是通用状态输出（警告、正文、错误、压缩状态等）。
type Reporter interface {
	Error(msg string)
	Warning(msg string)
	Text(msg string)
	CompactStatus(before, after, maxCtx int, strategy string)
}

// Spinner 控制短暂忙碌指示。
type Spinner interface {
	StartSpinner(message string)
	EndSpinner()
}

// Confirmer 处理危险操作确认与 diff 预览。
type Confirmer interface {
	Diff(old, new, filename string)
	PromptApply(message string) bool
}

// ToolUI 覆盖工具执行时的展示（Execute 非 Quiet 路径）。
type ToolUI interface {
	ToolCall(name, desc string)
	ToolResult(ok bool)
	ToolPreview(lines []string)
	StartSpinner(message string)
	EndSpinner()
}

// Host 是 Registry / 工具层需要的完整宿主能力。
type Host interface {
	Reporter
	Spinner
	Confirmer
	ToolUI
}

// Nop 是空实现，便于测试与无 UI 场景。
type Nop struct{}

func (Nop) Error(string)                        {}
func (Nop) Warning(string)                      {}
func (Nop) Text(string)                         {}
func (Nop) CompactStatus(int, int, int, string) {}
func (Nop) StartSpinner(string)                 {}
func (Nop) EndSpinner()                         {}
func (Nop) Diff(string, string, string)         {}
func (Nop) PromptApply(string) bool             { return true }
func (Nop) ToolCall(string, string)             {}
func (Nop) ToolResult(bool)                     {}
func (Nop) ToolPreview([]string)                {}
