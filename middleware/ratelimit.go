package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

// RateLimiter 简易令牌桶限流中间件（单机版，适合轻量部署）
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	rps     int
	log     zerolog.Logger
}

type tokenBucket struct {
	tokens   float64
	lastTime time.Time
}

// NewRateLimiter 创建限流器，rps 为每秒允许请求数
func NewRateLimiter(rps int, log zerolog.Logger) *RateLimiter {
	rl := &RateLimiter{
		buckets: make(map[string]*tokenBucket),
		rps:     rps,
		log:     log,
	}
	// 定期清理过期桶
	go rl.cleanup()
	return rl
}

// Handler 返回 Gin 中间件处理函数
func (rl *RateLimiter) Handler(keyFunc func(*gin.Context) string) gin.HandlerFunc {
	return rl.HandlerWithResponder(keyFunc, func(c *gin.Context) {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": "rate limit exceeded",
		})
	})
}

// HandlerWithResponder returns rate limiting middleware with a caller-defined
// response for rejected requests.
func (rl *RateLimiter) HandlerWithResponder(keyFunc func(*gin.Context) string, onLimit gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := keyFunc(c)

		rl.mu.Lock()
		bucket, ok := rl.buckets[key]
		if !ok {
			bucket = &tokenBucket{
				tokens:   float64(rl.rps),
				lastTime: time.Now(),
			}
			rl.buckets[key] = bucket
		}

		now := time.Now()
		elapsed := now.Sub(bucket.lastTime).Seconds()
		bucket.tokens += elapsed * float64(rl.rps)
		if bucket.tokens > float64(rl.rps) {
			bucket.tokens = float64(rl.rps)
		}
		bucket.lastTime = now

		if bucket.tokens < 1 {
			rl.mu.Unlock()
			rl.log.Warn().Str("key", key).Msg("rate limited")
			c.Abort()
			onLimit(c)
			return
		}

		bucket.tokens--
		rl.mu.Unlock()
		c.Next()
	}
}

func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		rl.mu.Lock()
		cutoff := time.Now().Add(-10 * time.Minute)
		for key, bucket := range rl.buckets {
			if bucket.lastTime.Before(cutoff) {
				delete(rl.buckets, key)
			}
		}
		rl.mu.Unlock()
	}
}
