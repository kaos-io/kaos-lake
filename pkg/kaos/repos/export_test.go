// KAOS (kaos-lake, PRD novelcore/kaos#1911 CON-03): new file, Apache-2.0.

package repos

// SetHistoryPage changes how many commits of main's history are read at a time, for a test; it
// returns the function that restores the value.
func SetHistoryPage(n int) (restore func()) {
	old := historyPage
	historyPage = n
	return func() { historyPage = old }
}
