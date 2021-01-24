package hystrix

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// 对每个 ExecutorPool 分别创建 CircuitBreaker 熔断器，用来跟踪应该尝试请求，还是在健康状态过低时拒绝
type CircuitBreaker struct {
	Name                   string        // 自定义的断路器名字，也就是 ConfigureCommand 传入的 name
	open                   bool          // 是否为打开状态
	forceOpen              bool          // 是否强制打开断路器
	mutex                  *sync.RWMutex // 锁
	openedOrLastTestedTime int64         //	上一次尝试检查服务是否恢复的时间戳

	executorPool *executorPool   // 运行池+相关运行统计指标
	metrics      *metricExchange // 命令指标
}

var (
	circuitBreakersMutex *sync.RWMutex              // 熔断器锁，保护 circuitBreakers map
	circuitBreakers      map[string]*CircuitBreaker // 保存所有的断路器
)

// 初始化
func init() {
	circuitBreakersMutex = &sync.RWMutex{}
	circuitBreakers = make(map[string]*CircuitBreaker)
}

// GetCircuit 根据 name 命令参数返回熔断器对象，或者在不存在时创建它
func GetCircuit(name string) (*CircuitBreaker, bool, error) {
	circuitBreakersMutex.RLock()
	_, ok := circuitBreakers[name]

	// 断路器不存在时创建
	if !ok {
		circuitBreakersMutex.RUnlock()
		circuitBreakersMutex.Lock()
		defer circuitBreakersMutex.Unlock()

		// 由于需要先释放读锁，然后加写锁，所以二次检查防止加写锁期间其他协程已经创建
		if cb, ok := circuitBreakers[name]; ok {
			return cb, false, nil
		}

		// 新建熔断器
		circuitBreakers[name] = newCircuitBreaker(name)
	} else {
		defer circuitBreakersMutex.RUnlock()
	}

	// 参数1: 断路器
	// 参数2: 标示是否为新建
	// 参数3: 错误
	return circuitBreakers[name], !ok, nil
}

// 清理内存中断路器和统计的相关信息
func Flush() {
	circuitBreakersMutex.Lock()
	defer circuitBreakersMutex.Unlock()

	for name, cb := range circuitBreakers {
		cb.metrics.Reset()
		cb.executorPool.Metrics.Reset()
		delete(circuitBreakers, name)
	}
}

// 创建一个带有关联运行状况的熔断器
func newCircuitBreaker(name string) *CircuitBreaker {
	c := &CircuitBreaker{}
	c.Name = name
	c.metrics = newMetricExchange(name)    // 创建命令执行对象
	c.executorPool = newExecutorPool(name) // 创建令牌池
	c.mutex = &sync.RWMutex{}

	return c
}

// toggleForceOpen 允许手动设置执行所有实例的 fallback
// 目前没发现调用点，可能用于作者测试
func (circuit *CircuitBreaker) toggleForceOpen(toggle bool) error {
	circuit, _, err := GetCircuit(circuit.Name)
	if err != nil {
		return err
	}

	circuit.forceOpen = toggle
	return nil
}

// IsOpen 需要在每次执行调用前来检查是否可以尝试执行，IsOpen 为 true 的话表示断路器为打开状态，即禁止尝试执行
func (circuit *CircuitBreaker) IsOpen() bool {
	circuit.mutex.RLock()
	o := circuit.forceOpen || circuit.open // 优先检查是否强制打开
	circuit.mutex.RUnlock()

	// 一旦为打开状态，直接返回 true
	if o {
		return true
	}

	// 判断是否触发开启断路器检查的最小请求量（相当于 sentinel 的静默请求数量）
	// 如果还没到达 RequestVolumeThreshold 请求量，直接放行，不做断路器检查
	if uint64(circuit.metrics.Requests().Sum(time.Now())) < getSettings(circuit.Name).RequestVolumeThreshold {
		return false
	}

	// 根据当前的错误率判断健康度
	// 当不是健康状态时，打开断路器开关，并返回 true
	if !circuit.metrics.IsHealthy(time.Now()) {
		// 太多的错误会触发断路器为打开状态
		circuit.setOpen()
		return true
	}

	// 当为健康状态时，返回 false
	return false
}

// 在执行命令之前检查 AllowRequest，以确保电路状态和度量标准运行状况允许它。
// 当断路器为打开状态时，此调用有时会返回 true，表示尝试外部服务是否已恢复。
func (circuit *CircuitBreaker) AllowRequest() bool {
	// 当断路器打开时，会判断是否允许尝试检查服务已经恢复
	// 当断路器关闭时，直接返回 true
	return !circuit.IsOpen() || circuit.allowSingleTest()
}

// 判断是否允许检查服务已经恢复
func (circuit *CircuitBreaker) allowSingleTest() bool {
	circuit.mutex.RLock()
	defer circuit.mutex.RUnlock()

	now := time.Now().UnixNano()
	openedOrLastTestedTime := atomic.LoadInt64(&circuit.openedOrLastTestedTime)

	// 当断路器为打开状态 && 已经经过了允许尝试时间窗口 && cas 成功，才允许进行尝试检查服务已经恢复
	if circuit.open && now > openedOrLastTestedTime+getSettings(circuit.Name).SleepWindow.Nanoseconds() {
		swapped := atomic.CompareAndSwapInt64(&circuit.openedOrLastTestedTime, openedOrLastTestedTime, now)
		if swapped {
			log.Printf("hystrix-go: allowing single test to possibly close circuit %v", circuit.Name)
		}
		return swapped
	}

	return false
}

// 设置断路器为打开状态
func (circuit *CircuitBreaker) setOpen() {
	circuit.mutex.Lock()
	defer circuit.mutex.Unlock()

	if circuit.open {
		return
	}

	log.Printf("hystrix-go: opening circuit %v", circuit.Name)

	// 保存打开断路器时间
	circuit.openedOrLastTestedTime = time.Now().UnixNano()

	// 将状态置为打开
	circuit.open = true
}

// 设置断路器为关闭状态
func (circuit *CircuitBreaker) setClose() {
	circuit.mutex.Lock()
	defer circuit.mutex.Unlock()

	if !circuit.open {
		return
	}

	log.Printf("hystrix-go: closing circuit %v", circuit.Name)

	// 状态置为关闭
	circuit.open = false

	// 重置统计数据
	circuit.metrics.Reset()
}

// ReportEvent 记录命令指标，以跟踪最近的错误率并将数据公开给仪表板
func (circuit *CircuitBreaker) ReportEvent(eventTypes []string, start time.Time, runDuration time.Duration) error {
	if len(eventTypes) == 0 {
		return fmt.Errorf("no event types sent for metrics")
	}

	circuit.mutex.RLock()
	o := circuit.open
	circuit.mutex.RUnlock()

	// 当为成功事件 && 当前断路器状态为打开时，将断路器状态置为关闭状态
	if eventTypes[0] == "success" && o {
		circuit.setClose()
	}

	var concurrencyInUse float64
	if circuit.executorPool.Max > 0 {
		// 计算当前的并发量和支持的最大并发比率
		concurrencyInUse = float64(circuit.executorPool.ActiveCount()) / float64(circuit.executorPool.Max)
	}

	select {
	case circuit.metrics.Updates <- &commandExecution{ // 发送命令执行的指标数据
		Types:            eventTypes,       // 事件列表
		Start:            start,            // 开始事件
		RunDuration:      runDuration,      // 执行耗时
		ConcurrencyInUse: concurrencyInUse, // 当前的并发占比
	}:
	default:
		return CircuitError{Message: fmt.Sprintf("metrics channel (%v) is at capacity", circuit.Name)}
	}

	return nil
}
