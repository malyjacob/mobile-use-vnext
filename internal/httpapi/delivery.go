package httpapi

import (
	"sync"
	"time"

	"net/http"
)

// Large-result delivery is a bounded resource independent of the single device
// work position. The engine finishes its work before the HTTP handler writes;
// a slow client must not be able to accumulate an unbounded number of frozen
// trees or shell outputs. Admission reserves a fixed upper bound before the
// engine is called; the slot is released after the write completes or fails.
const (
	deliveryMaxActive = 2
	deliveryMaxBytes  = 4 << 20
	deliveryDeadline  = 15 * time.Second

	deliveryReserveTree  = 256 << 10
	deliveryReserveImage = 1 << 20 // tree + one bounded base64 image carrier
	deliveryReserveShell = 384 << 10
	deliveryReserveApps  = 64 << 10
	deliveryReserveDiag  = 32 << 10
)

// deliveryLimiter bounds in-flight large result deliveries.
type deliveryLimiter struct {
	mu       sync.Mutex
	active   int
	reserved int64
}

// acquire reserves one delivery slot and its byte upper bound. It never
// queues: a caller that cannot reserve is rejected before doing device work.
func (d *deliveryLimiter) acquire(reserve int64) (func(), bool) {
	if d == nil {
		return func() {}, true
	}
	if reserve < 0 {
		reserve = 0
	}
	d.mu.Lock()
	if d.active >= deliveryMaxActive || d.reserved+reserve > deliveryMaxBytes {
		d.mu.Unlock()
		return nil, false
	}
	d.active++
	d.reserved += reserve
	d.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			d.active--
			d.reserved -= reserve
			if d.active < 0 {
				d.active = 0
			}
			if d.reserved < 0 {
				d.reserved = 0
			}
			d.mu.Unlock()
		})
	}, true
}

// stats exposes bounded counters for tests and diagnostics.
func (d *deliveryLimiter) stats() (active int, reserved int64) {
	if d == nil {
		return 0, 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.active, d.reserved
}

// writeJSONDelivery writes one result with a bounded send deadline. A slow
// client cannot hold a delivery slot forever; the committed engine facts are
// unaffected by the failed write.
func writeJSONDelivery(w http.ResponseWriter, status int, value any) {
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(deliveryDeadline))
	writeJSON(w, status, value)
	_ = controller.SetWriteDeadline(time.Time{})
}
