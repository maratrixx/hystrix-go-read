package hystrix

type executorPool struct {
	Name    string         // command name
	Metrics *poolMetrics   //
	Max     int            // 最大令牌数
	Tickets chan *struct{} // 存放令牌信息
}

func newExecutorPool(name string) *executorPool {
	p := &executorPool{}
	p.Name = name
	p.Metrics = newPoolMetrics(name)
	p.Max = getSettings(name).MaxConcurrentRequests

	// 初始化令牌池
	p.Tickets = make(chan *struct{}, p.Max)
	for i := 0; i < p.Max; i++ {
		p.Tickets <- &struct{}{}
	}

	return p
}

// 归还令牌
func (p *executorPool) Return(ticket *struct{}) {
	if ticket == nil {
		return
	}

	// 通知运行池更新统计指标
	p.Metrics.Updates <- poolMetricsUpdate{
		activeCount: p.ActiveCount(),
	}

	// 放回令牌池
	p.Tickets <- ticket
}

// 计算当前的活跃运行数
func (p *executorPool) ActiveCount() int {
	return p.Max - len(p.Tickets)
}
