package hystrix

import (
	"sync"

	"github.com/imttx/hystrix-go/hystrix/rolling"
)

// 断路器运行池指标
type poolMetrics struct {
	Mutex   *sync.RWMutex          // 读写锁
	Updates chan poolMetricsUpdate // 指标更新通知

	Name              string          // command name
	MaxActiveRequests *rolling.Number // 统计最大请求数指标
	Executed          *rolling.Number // 统计运行数指标
}

type poolMetricsUpdate struct {
	activeCount int
}

// 新建运行池指标
func newPoolMetrics(name string) *poolMetrics {
	m := &poolMetrics{}
	m.Name = name
	m.Updates = make(chan poolMetricsUpdate)
	m.Mutex = &sync.RWMutex{}

	m.Reset()

	go m.Monitor()

	return m
}

func (m *poolMetrics) Reset() {
	m.Mutex.Lock()
	defer m.Mutex.Unlock()

	m.MaxActiveRequests = rolling.NewNumber()
	m.Executed = rolling.NewNumber()
}

func (m *poolMetrics) Monitor() {
	for u := range m.Updates {
		m.Mutex.RLock()

		m.Executed.Increment(1)                               // 运行数递增
		m.MaxActiveRequests.UpdateMax(float64(u.activeCount)) // 更新运行最大数

		m.Mutex.RUnlock()
	}
}
