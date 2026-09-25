//go:build !live

package rss

import "github.com/shijie152/ani-rss/go-backend/internal/testutil"

func init() {
	testutil.InstallTestNetworkGuard()
}
