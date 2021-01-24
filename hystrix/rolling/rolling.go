package rolling

import (
	"sync"
	"time"
)

// Number 在一定数量的时间段内跟踪 numberBucket, 当前存储桶（numberBucket）长一秒，仅保留最后 10s
type Number struct {
	Buckets map[int64]*numberBucket
	Mutex   *sync.RWMutex
}

type numberBucket struct {
	Value float64
}

// 初始化 Number
func NewNumber() *Number {
	r := &Number{
		Buckets: make(map[int64]*numberBucket),
		Mutex:   &sync.RWMutex{},
	}
	return r
}

// 获取当前时间戳对应的 numberBucket
func (r *Number) getCurrentBucket() *numberBucket {
	now := time.Now().Unix()
	var bucket *numberBucket
	var ok bool

	if bucket, ok = r.Buckets[now]; !ok {
		bucket = &numberBucket{}
		r.Buckets[now] = bucket
	}

	return bucket
}

// 移除 10s 以前的 numberBucket
func (r *Number) removeOldBuckets() {
	now := time.Now().Unix() - 10

	for timestamp := range r.Buckets {
		// TODO: configurable rolling window
		if timestamp <= now {
			delete(r.Buckets, timestamp)
		}
	}
}

// 增加当前时间对应的 numberBucket 值
func (r *Number) Increment(i float64) {
	if i == 0 {
		return
	}

	r.Mutex.Lock()
	defer r.Mutex.Unlock()

	b := r.getCurrentBucket() // 获取当前 numberBucket
	b.Value += i              // numberBucket.Value += i
	r.removeOldBuckets()      // 移除 10s 以前的 numberBucket
}

// UpdateMax 更新当前存储桶中的最大值
func (r *Number) UpdateMax(n float64) {
	r.Mutex.Lock()
	defer r.Mutex.Unlock()

	b := r.getCurrentBucket()
	if n > b.Value { // 只有当大于当前值时进行更新
		b.Value = n
	}
	r.removeOldBuckets()
}

// Sum 对过去 10s 内桶内的数值求和
func (r *Number) Sum(now time.Time) float64 {
	sum := float64(0)

	r.Mutex.RLock()
	defer r.Mutex.RUnlock()

	for timestamp, bucket := range r.Buckets {
		// TODO: configurable rolling window
		if timestamp >= now.Unix()-10 {
			sum += bucket.Value
		}
	}

	return sum
}

// Max 获取过去 10s 内桶内的最大值
func (r *Number) Max(now time.Time) float64 {
	var max float64

	r.Mutex.RLock()
	defer r.Mutex.RUnlock()

	for timestamp, bucket := range r.Buckets {
		// TODO: configurable rolling window
		if timestamp >= now.Unix()-10 {
			if bucket.Value > max {
				max = bucket.Value
			}
		}
	}

	return max
}

// Avg 计算过去 10s 内的平均值
func (r *Number) Avg(now time.Time) float64 {
	return r.Sum(now) / 10
}
