package hystrix

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type runFunc func() error
type fallbackFunc func(error) error
type runFuncC func(context.Context) error
type fallbackFuncC func(context.Context, error) error

// A CircuitError is an error which models various failure states of execution,
// such as the circuit being open or a timeout.
type CircuitError struct {
	Message string
}

func (e CircuitError) Error() string {
	return "hystrix: " + e.Message
}

// command models the state used for a single execution on a circuit. "hystrix command" is commonly
// used to describe the pairing of your run/fallback functions with a circuit.
// command 用于封装单个请求的熔断状态，其中包含 run/fallback 方法
type command struct {
	sync.Mutex

	ticket      *struct{}       // 保存获取到的令牌
	start       time.Time       // 开始时间
	errChan     chan error      // 错误信息 channel
	finished    chan bool       // 完成信息 channel
	circuit     *CircuitBreaker // 熔断器
	run         runFuncC        // 封装正常运行的方法
	fallback    fallbackFuncC   // 封装降级运行的方法
	runDuration time.Duration   // 执行花费的时间
	events      []string        // 保养当前的 command 事件
}

// 自定义错误
var (
	// ErrMaxConcurrency occurs when too many of the same named command are executed at the same time.
	ErrMaxConcurrency = CircuitError{Message: "max concurrency"}
	// ErrCircuitOpen returns when an execution attempt "short circuits". This happens due to the circuit being measured as unhealthy.
	ErrCircuitOpen = CircuitError{Message: "circuit open"}
	// ErrTimeout occurs when the provided function takes too long to execute.
	ErrTimeout = CircuitError{Message: "timeout"}
)

// Go 通过跟踪先前的调用健康状态来运行 run 函数
// 如果你的函数开始变慢或反复的失败，我们将会阻塞对该函数新的调用，以便给依赖的服务时间进行修复
// 如果想在熔断期间执行降级操作，需要定义一个 fallback 函数
// 返回值为 chan error 类型，需要调用端自行接受
func Go(name string, run runFunc, fallback fallbackFunc) chan error {
	// 包装正常的执行函数
	runC := func(ctx context.Context) error {
		return run()
	}

	// 当存在降级函数时，包装该函数
	var fallbackC fallbackFuncC
	if fallback != nil {
		fallbackC = func(ctx context.Context, err error) error {
			return fallback(err)
		}
	}

	// 本地统一调用 GoC
	return GoC(context.Background(), name, runC, fallbackC)
}

// GoC 通过跟踪先前的调用健康状态来运行 run 函数
// 如果你的函数开始变慢或反复的失败，我们将会阻塞对该函数新的调用，以便给依赖的服务时间进行修复
// 如果想在熔断期间执行降级操作，需要定义一个 fallback 函数
// 返回值为 chan error 类型，需要调用端自行接受
func GoC(ctx context.Context, name string, run runFuncC, fallback fallbackFuncC) chan error {
	// 对于每次调用新建 command 操作命令
	cmd := &command{
		run:      run,
		fallback: fallback,
		start:    time.Now(),
		errChan:  make(chan error, 1),
		finished: make(chan bool, 1),
	}

	// 没有具有显式参数和返回值的方法
	// dont have methods with explicit params and returns
	// let data come in and out naturally, like with any closure
	// explicit error return to give place for us to kill switch the operation (fallback)

	// 根据 command name 参数获取熔断器，当获取失败时返回错误
	circuit, _, err := GetCircuit(name)
	if err != nil {
		cmd.errChan <- err
		return cmd.errChan
	}

	// 断路器赋值
	cmd.circuit = circuit

	// TODO
	ticketCond := sync.NewCond(cmd)

	// 标示是否执行了获取令牌操作，不管获取失败与否
	ticketChecked := false

	// 当调用者从返回的 errChan 中获取到错误，那么假定 ticket 已返回给 executorPool。
	// 因此，在 cmd.errorWithFallback() 之后不能运行 returnTicket()。
	//
	// returnTicket 为归还令牌函数
	returnTicket := func() {
		cmd.Lock()
		// 阻塞等待，避免在获取到令牌之前就执行归还操作
		for !ticketChecked {
			ticketCond.Wait()
		}
		// 归还令牌
		cmd.circuit.executorPool.Return(cmd.ticket)
		cmd.Unlock()
	}

	// returnOnce 在下面的两个 goroutine 之间共享，用来确保只有执行的快的 goroutine 才能运行 errWithFallback() 和 reportAllEvent() 函数。
	returnOnce := &sync.Once{}

	// 打包上报当前 command 所有事件
	reportAllEvent := func() {
		err := cmd.circuit.ReportEvent(cmd.events, cmd.start, cmd.runDuration)
		if err != nil {
			log.Printf(err.Error())
		}
	}

	// 创建协程用于检查断路器状态、获取令牌、上报事件、执行用户函数
	go func() {
		// 执行成功后，写入 finish channel
		defer func() { cmd.finished <- true }()

		// 当最近的执行出现很高的错误率时，断路器就会打开，拒绝新的执行来使后端得以恢复，并且断路器在感觉健康状态已恢复时将允许新的请求。

		// 当断路器不允许新的请求时（此时断路器已经打开）
		if !cmd.circuit.AllowRequest() {
			cmd.Lock()
			// 当另一个 goroutine 提前执行释放令牌时依然安全
			ticketChecked = true

			// 唤醒一个正在等待的协程
			ticketCond.Signal()
			cmd.Unlock()
			returnOnce.Do(func() {
				returnTicket()                             // 归还令牌
				cmd.errorWithFallback(ctx, ErrCircuitOpen) // 上报当前错误，并尝试执行 fallback
				reportAllEvent()                           // 上报事件
			})
			return
		}

		// As backends falter, requests take longer but don't always fail.
		//
		// When requests slow down but the incoming rate of requests stays the same, you have to
		// run more at a time to keep up. By controlling concurrency during these situations, you can
		// shed load which accumulates due to the increasing ratio of active commands to incoming requests.

		// 加锁获取令牌
		cmd.Lock()
		select {
		case cmd.ticket = <-circuit.executorPool.Tickets: // 获取令牌成功
			ticketChecked = true
			ticketCond.Signal()
			cmd.Unlock()
		default: // 获取令牌失败（当超过 command 允许的最大并发量时）
			ticketChecked = true
			ticketCond.Signal()
			cmd.Unlock()
			returnOnce.Do(func() {
				returnTicket()
				cmd.errorWithFallback(ctx, ErrMaxConcurrency)
				reportAllEvent()
			})
			return
		}

		runStart := time.Now()

		// 真正执行用户正常的 run 函数
		runErr := run(ctx)

		returnOnce.Do(func() {
			defer reportAllEvent()                 // 上报保存的所有事件
			cmd.runDuration = time.Since(runStart) // 计算耗时
			returnTicket()                         // 归还令牌
			if runErr != nil {                     // 检查是否错误
				cmd.errorWithFallback(ctx, runErr) // 保存错误事件，并尝试执行 fallback
				return
			}
			cmd.reportEvent("success") // 保存执行成功事件
		})
	}()

	// 创建协程用于检查当前操作是否超时
	go func() {
		// 根据全局超时事件创建定时器
		timer := time.NewTimer(getSettings(name).Timeout)
		defer timer.Stop()

		select {
		case <-cmd.finished: // 检查是否已经完成
			// returnOnce 已经在另外的协程运行，这里不再执行
		case <-ctx.Done(): // 检查 context 自定义超时
			returnOnce.Do(func() {
				returnTicket()
				cmd.errorWithFallback(ctx, ctx.Err())
				reportAllEvent()
			})
			return
		case <-timer.C: // 检查 command 初始化定义超时
			returnOnce.Do(func() {
				returnTicket()
				cmd.errorWithFallback(ctx, ErrTimeout)
				reportAllEvent()
			})
			return
		}
	}()

	return cmd.errChan
}

// Do 以同步阻塞方式调用 run 函数，直到返回成功或者失败，当然也包含触发熔断错误
func Do(name string, run runFunc, fallback fallbackFunc) error {
	// 包装正常函数的调用
	runC := func(ctx context.Context) error {
		return run()
	}

	// 当定义了 fallback 函数，对齐进行包装
	var fallbackC fallbackFuncC
	if fallback != nil {
		fallbackC = func(ctx context.Context, err error) error {
			return fallback(err)
		}
	}

	// 调用 DoC
	return DoC(context.Background(), name, runC, fallbackC)
}

// DoC 以同步阻塞方式调用 run 函数，直到返回成功或者失败，当然也包含触发熔断错误
func DoC(ctx context.Context, name string, run runFuncC, fallback fallbackFuncC) error {
	// 初始化 done chan 用于接收完成信息，当失败时不会往 done chan 写入数据
	done := make(chan struct{}, 1)

	// 对正常的 run 函数进行二次包装，只有当内部执行成功时才调用 done <- struct{}{}
	r := func(ctx context.Context) error {
		err := run(ctx)
		if err != nil {
			return err
		}

		done <- struct{}{}
		return nil
	}

	// 对 fallback 函数进行二次包装，只有当内部 fallback 执行成功时才调用 done <- struct{}{}
	f := func(ctx context.Context, e error) error {
		err := fallback(ctx, e)
		if err != nil {
			return err
		}

		done <- struct{}{}
		return nil
	}

	// 最后都是统一调用 GoC 函数
	var errChan chan error
	if fallback == nil {
		errChan = GoC(ctx, name, r, nil)
	} else {
		errChan = GoC(ctx, name, r, f)
	}

	// select 阻塞等待接收 chan 信息，成功或失败哪个 chan 信息先到达就返回哪个
	select {
	case <-done:
		return nil
	case err := <-errChan:
		return err
	}
}

// reportEvent 应该叫保存事件，先临时放入 event 字段，后面会统一调用 reportAllEvent 上报所有事件
func (c *command) reportEvent(eventType string) {
	c.Lock()
	defer c.Unlock()

	c.events = append(c.events, eventType)
}

// errorWithFallback 在报告适当的度量标准事件时触发 fallback
func (c *command) errorWithFallback(ctx context.Context, err error) {
	eventType := "failure"
	if err == ErrCircuitOpen {
		eventType = "short-circuit"
	} else if err == ErrMaxConcurrency {
		eventType = "rejected"
	} else if err == ErrTimeout {
		eventType = "timeout"
	} else if err == context.Canceled {
		eventType = "context_canceled"
	} else if err == context.DeadlineExceeded {
		eventType = "context_deadline_exceeded"
	}

	// 保存事件
	c.reportEvent(eventType)

	// 尝试执行 fallback，并上报 fallback 执行结果事件
	fallbackErr := c.tryFallback(ctx, err)

	// fallback 执行失败时，将错误信息写入 error channel
	if fallbackErr != nil {
		c.errChan <- fallbackErr
	}
}

// tryFallback 尝试执行 fallback
func (c *command) tryFallback(ctx context.Context, err error) error {
	// 如果未定义 fallback 直接返回原始的错误
	if c.fallback == nil {
		return err
	}

	// 执行 fallback
	fallbackErr := c.fallback(ctx, err)
	// fallback 执行失败时保存错误事件
	if fallbackErr != nil {
		c.reportEvent("fallback-failure")
		return fmt.Errorf("fallback failed with '%v'. run error was '%v'", fallbackErr, err)
	}

	// 保存 fallback 执行成功事件
	c.reportEvent("fallback-success")

	return nil
}
