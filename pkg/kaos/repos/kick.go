// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-12): new file, Apache-2.0.

package repos

// kicks wakes Run between ticks. One pending kick is enough: the pass it starts reads every
// dataset's kaos.shared_with as it is then.
var kicks = make(chan struct{}, 1)

// Kick asks Run to apply the dataset shares (ReconcileShares) now, without waiting for the next
// interval. The metadata API calls it after a write or delete touching kaos.shared_with, so a
// share or unshare reaches the consuming project's group at once. It never blocks.
func Kick() {
	select {
	case kicks <- struct{}{}:
	default:
	}
}

// Kicks is what Kick signals and Run waits on, for tests of the callers.
func Kicks() <-chan struct{} {
	return kicks
}
