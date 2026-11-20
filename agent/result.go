package agent

import "errors"

// StopReason 区分运行结束原因，不表达业务任务是否完成。
type StopReason string

const (
	StopCompleted     StopReason = "completed"
	StopModel         StopReason = "model_stop"
	StopMaxIterations StopReason = "max_iterations"
	StopCanceled      StopReason = "canceled"
	StopTimeout       StopReason = "timeout"
	StopError         StopReason = "error"
)

var ErrMaxIterations = errors.New("agent maximum iterations reached")

// RunResult 是单次运行结果；Output 保留结束工具返回的原始结构。
// Content 为最后的模型正文或结束工具返回的文本。
type RunResult struct {
	StopReason   StopReason
	Iterations   int
	Content      string
	Output       any
	FinishReason string
}
