package metricCollector

import (
	"sync"
	"time"
)

// Registry is the default metricCollectorRegistry that circuits will use to
// collect statistics about the health of the circuit.
var Registry = metricCollectorRegistry{
	lock: &sync.RWMutex{},
	registry: []func(name string) MetricCollector{
		newDefaultMetricCollector,
	},
}

type metricCollectorRegistry struct {
	lock     *sync.RWMutex
	registry []func(name string) MetricCollector
}

// InitializeMetricCollectors runs the registried MetricCollector Initializers to create an array of MetricCollectors.
func (m *metricCollectorRegistry) InitializeMetricCollectors(name string) []MetricCollector {
	m.lock.RLock()
	defer m.lock.RUnlock()

	metrics := make([]MetricCollector, len(m.registry))
	for i, metricCollectorInitializer := range m.registry {
		metrics[i] = metricCollectorInitializer(name)
	}
	return metrics
}

// Register places a MetricCollector Initializer in the registry maintained by this metricCollectorRegistry.
func (m *metricCollectorRegistry) Register(initMetricCollector func(string) MetricCollector) {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.registry = append(m.registry, initMetricCollector)
}

type MetricResult struct {
	Attempts                float64       // 请求数
	Errors                  float64       // 错误数
	Successes               float64       // 成功数
	Failures                float64       // 失败数
	Rejects                 float64       // 拒绝数
	ShortCircuits           float64       // 熔断数
	Timeouts                float64       // 超时数
	FallbackSuccesses       float64       // 降级成功数
	FallbackFailures        float64       // 降级失败数
	ContextCanceled         float64       // contxt 被取消数
	ContextDeadlineExceeded float64       // context 超时数
	TotalDuration           time.Duration // 总耗时（到达 moniter 计算的总耗时）
	RunDuration             time.Duration // 运行总耗时
	ConcurrencyInUse        float64       // 并发占比
}

// MetricCollector represents the contract that all collectors must fulfill to gather circuit statistics.
// Implementations of this interface do not have to maintain locking around thier data stores so long as
// they are not modified outside of the hystrix context.
type MetricCollector interface {
	// Update accepts a set of metrics from a command execution for remote instrumentation
	Update(MetricResult)
	// Reset resets the internal counters and timers.
	Reset()
}
