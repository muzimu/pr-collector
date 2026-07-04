package handler

import (
	"testing"

	"pr-collector/redis/cache"
)

func TestAddRanks(t *testing.T) {
	h := &LeaderboardHandler{}
	users := []cache.LeaderboardUser{
		{Username: "a", Score: 100},
		{Username: "b", Score: 90},
		{Username: "c", Score: 80},
	}

	h.addRanks(users, 0)
	if users[0].Rank != 1 || users[1].Rank != 2 || users[2].Rank != 3 {
		t.Errorf("ranks = %v, want [1 2 3]", []int{users[0].Rank, users[1].Rank, users[2].Rank})
	}

	h.addRanks(users, 50)
	if users[0].Rank != 51 || users[1].Rank != 52 || users[2].Rank != 53 {
		t.Errorf("ranks = %v, want [51 52 53]", []int{users[0].Rank, users[1].Rank, users[2].Rank})
	}
}
