package engine

import (
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// appListCache is one trusted application-list read. It is memory-only and is
// invalidated by TTL or by a paired-App instance change; a cache hit never takes
// the work position, renews a lease or consumes an observation.
type appListCache struct {
	entries     []device.AppEntry
	capturedAt  time.Time
	appInstance string
}

// appsUnavailable maps a control/availability blocker to a controlled query
// error without inventing a new availability authority.
func appsUnavailable(reason string) *protocol.APIError {
	if reason == "" {
		reason = protocol.CodeDeviceUnavailable
	}
	status := protocol.Status(reason)
	if status == 500 {
		status = 409
	}
	return protocol.NewError(status, reason, "当前不可读取应用清单")
}
