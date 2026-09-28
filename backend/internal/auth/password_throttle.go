package auth

import (
	"sync"
	"time"
)

// 密码失败节流参数。
//
// 验证码通道天然有 5 分钟过期 + 6 位长度两道限制，窗口小到不值得再挂节流；密码
// 是长期有效的固定串，同样的 6 位空间在这里可以慢慢试到天亮，所以必须有。
const (
	passwordFailureLimit    = 5
	passwordFailureWindow   = 15 * time.Minute
	passwordThrottleMaxKeys = 8192
)

// passwordThrottle 是进程内的密码失败计数器。
//
// 只做进程内计数是个有意识的取舍：单实例部署够用，且不需要为它加一张 Prisma 迁移
// 管理的表。多实例部署下每个副本各自计数，有效阈值会放大到「副本数 × limit」——
// 到那一步必须换成 Redis 或数据库这类共享存储，不能靠调小 limit 蒙过去。
type passwordThrottle struct {
	mu       sync.Mutex
	failures map[string][]time.Time
	now      func() time.Time
}

func newPasswordThrottle(now func() time.Time) *passwordThrottle {
	if now == nil {
		now = time.Now
	}
	return &passwordThrottle{failures: make(map[string][]time.Time), now: now}
}

// check 在验证密码之前判断是否已经超过失败阈值。
func (t *passwordThrottle) check(keys ...string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	cutoff := t.now().Add(-passwordFailureWindow)
	t.pruneLocked(cutoff)
	for _, key := range keys {
		if len(t.failures[key]) >= passwordFailureLimit {
			return rateLimited("密码错误次数过多，请 15 分钟后再试，或改用验证码登录")
		}
	}
	return nil
}

// fail 记录一次失败尝试。
func (t *passwordThrottle) fail(keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	cutoff := t.now().Add(-passwordFailureWindow)
	t.pruneLocked(cutoff)
	now := t.now()
	for _, key := range keys {
		if key == "" {
			continue
		}
		t.failures[key] = append(t.failures[key], now)
	}
}

// reset 在登录成功后清空计数，避免「自己先打错几次、之后被自己的历史卡住」。
func (t *passwordThrottle) reset(keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, key := range keys {
		delete(t.failures, key)
	}
}

// pruneLocked 丢弃窗口外的记录，并在键数量失控时整体清空。
//
// 键来自外部输入（标识与来源 IP），不设上限就是一个可以被撑爆的内存增长点；
// 触发上限时整体清空而不是逐条淘汰，是因为这个计数器只做粗粒度限流，
// 一次误放行远好于为了精确淘汰引入 LRU 状态。
func (t *passwordThrottle) pruneLocked(cutoff time.Time) {
	for key, stamps := range t.failures {
		kept := stamps[:0]
		for _, stamp := range stamps {
			if stamp.After(cutoff) {
				kept = append(kept, stamp)
			}
		}
		if len(kept) == 0 {
			delete(t.failures, key)
			continue
		}
		t.failures[key] = kept
	}
	if len(t.failures) > passwordThrottleMaxKeys {
		t.failures = make(map[string][]time.Time)
	}
}
