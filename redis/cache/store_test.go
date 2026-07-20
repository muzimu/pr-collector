package cache

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"pr-collector/config"
)

// TestClearAllProjectKeys 本地调试用：清空本项目在 Redis 中的所有 key。
// ⚠️ 危险操作：会不可逆删除所有业务数据，仅用于本地测试。
func TestClearAllProjectKeys(t *testing.T) {
	if os.Getenv("PR_COLLECTOR_CLEAR_TEST_DATA") != "1" {
		t.Skip("set PR_COLLECTOR_CLEAR_TEST_DATA=1 to clear project Redis keys")
	}

	cfg := &config.Config{
		Redis: config.RedisConfig{
			Addr: "127.0.0.1:6379",
		},
	}

	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "config.yaml"
	}

	if loaded, err := config.Load(cfgPath); err == nil {
		cfg = loaded
	} else if loaded, err := config.Load("../../config.yaml"); err == nil {
		cfg = loaded
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})

	_ = NewStore(rdb, zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}))

	ctx := context.Background()

	patterns := []string{
		usersSetKey,
		fmt.Sprintf(userKeyPrefix, "*"),
		fmt.Sprintf(prListKeyPrefix, "*"),
		fmt.Sprintf(svgKeyPrefix, "*", "*"),
		fmt.Sprintf(svgBySuffixKeyPrefix, "*"),
		fmt.Sprintf(lockKeyPrefix, "*"),
		statsCardVisitsKey,
		statsPRVisitsKey,
		leaderboardCacheKey,
		leaderboardMetaKey,
		fmt.Sprintf(leaderboardUserKeyPrefix, "*"),
	}

	var total int64
	for _, pattern := range patterns {
		keys, err := rdb.Keys(ctx, pattern).Result()
		if err != nil {
			t.Fatalf("keys %q: %v", pattern, err)
		}
		if len(keys) == 0 {
			continue
		}
		n, err := rdb.Del(ctx, keys...).Result()
		if err != nil {
			t.Fatalf("del %q: %v", pattern, err)
		}
		total += n
		t.Logf("pattern %q deleted %d keys", pattern, n)
	}

	t.Logf("total deleted %d keys", total)
}
