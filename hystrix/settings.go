package hystrix

import (
	"sync"
	"time"
)

// 配置项默认值
var (
	// 等待 command 完成的时间，默认 1000ms
	DefaultTimeout = 1000
	// 同一个 command 支持的并发量，默认 10
	DefaultMaxConcurrent = 10
	// 触发开启熔断的最新请求量，相当于 sentinel 的静默请求数量，默认 20
	// 换言之，若当前统计周期内的请求数小于此值，即使达到熔断条件规则也不会触发
	DefaultVolumeThreshold = 20
	// 当熔断器被打开后，控制过多久后去尝试探测服务是否可用了，默认值是 5000ms
	DefaultSleepWindow = 5000
	// 错误百分比，当错误比例达到此值后触发熔断，默认 50
	DefaultErrorPercentThreshold = 50
	// 默认日志
	DefaultLogger = NoopLogger{}
)

// hystrix 内部使用的配置，和 CommandConfig 类似，只是做了类型值得转换
type Settings struct {
	Timeout                time.Duration // 等待 command 完成的时间，默认 1000ms
	MaxConcurrentRequests  int           // 同一个 command 支持的并发量，默认 10
	RequestVolumeThreshold uint64        // 触发开启熔断的最小请求量，相当于 sentinel 的静默请求数量，默认 20
	SleepWindow            time.Duration // 当熔断器被打开后，控制过多久后去尝试探测服务是否可用了，默认值是 5000ms
	ErrorPercentThreshold  int           // 错误百分比，当错误比例达到此值后触发熔断
}

// 暴露给用户使用的配置项
type CommandConfig struct {
	Timeout                int `json:"timeout"`                  // 等待 command 完成的时间，默认 1000（ms）
	MaxConcurrentRequests  int `json:"max_concurrent_requests"`  // 同一个 command 支持的并发量，默认 10
	RequestVolumeThreshold int `json:"request_volume_threshold"` // 触发开启熔断的最新请求量，相当于 sentinel 的静默请求数量，默认 20
	SleepWindow            int `json:"sleep_window"`             // 当熔断器被打开后，控制过多久后去尝试探测服务是否可用了，默认值是 5000（ms）
	ErrorPercentThreshold  int `json:"error_percent_threshold"`  // 错误百分比，当错误比例达到此值后触发熔断，默认 50（%）
}

var circuitSettings map[string]*Settings // 保存运行时的所有配置项，可能有多个 command 对应的配置
var settingsMutex *sync.RWMutex          // 操作 circuitSettings 需要加锁
var log logger                           // 内部日志，可以使用 SetLogger 修改

func init() {
	circuitSettings = make(map[string]*Settings)
	settingsMutex = &sync.RWMutex{}
	log = DefaultLogger
}

// 添加一组配置
func Configure(cmds map[string]CommandConfig) {
	for k, v := range cmds {
		ConfigureCommand(k, v)
	}
}

// 添加单个配置
func ConfigureCommand(name string, config CommandConfig) {
	// 更新 circuitSettings 时需要加锁保护
	settingsMutex.Lock()
	defer settingsMutex.Unlock()

	// 当用户没自定义配置时，使用默认值
	timeout := DefaultTimeout
	if config.Timeout != 0 {
		timeout = config.Timeout
	}

	max := DefaultMaxConcurrent
	if config.MaxConcurrentRequests != 0 {
		max = config.MaxConcurrentRequests
	}

	volume := DefaultVolumeThreshold
	if config.RequestVolumeThreshold != 0 {
		volume = config.RequestVolumeThreshold
	}

	sleep := DefaultSleepWindow
	if config.SleepWindow != 0 {
		sleep = config.SleepWindow
	}

	errorPercent := DefaultErrorPercentThreshold
	if config.ErrorPercentThreshold != 0 {
		errorPercent = config.ErrorPercentThreshold
	}

	// 根据 config 转换为最终想要的 setting 格式保存到 circuitSettings
	circuitSettings[name] = &Settings{
		Timeout:                time.Duration(timeout) * time.Millisecond,
		MaxConcurrentRequests:  max,
		RequestVolumeThreshold: uint64(volume),
		SleepWindow:            time.Duration(sleep) * time.Millisecond,
		ErrorPercentThreshold:  errorPercent, // ErrorPercentThreshold 为百分比值，比如 50 代表 50%
	}
}

// 获取 name 对应的配置，不存在时创建一个默认的
func getSettings(name string) *Settings {
	settingsMutex.RLock()
	s, exists := circuitSettings[name]
	settingsMutex.RUnlock()

	if !exists {
		ConfigureCommand(name, CommandConfig{})
		s = getSettings(name)
	}

	return s
}

// 获取所有配置
func GetCircuitSettings() map[string]*Settings {
	copy := make(map[string]*Settings)

	settingsMutex.RLock()
	for key, val := range circuitSettings {
		copy[key] = val
	}
	settingsMutex.RUnlock()

	return copy
}

// 修改默认日志
func SetLogger(l logger) {
	log = l
}
