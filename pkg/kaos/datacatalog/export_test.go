package datacatalog

import "time"

// SetClock replaces the cache's clock; for tests.
func (c *Cache) SetClock(now func() time.Time) { c.now = now }
