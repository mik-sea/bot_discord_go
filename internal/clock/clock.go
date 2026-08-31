// Package clock provides the Asia/Jakarta timezone used for every
// user-facing or persisted timestamp in the bot.
package clock

import "time"

// Jakarta is the WIB timezone (UTC+7). If the host is missing tzdata, it
// falls back to a fixed UTC+7 offset so behavior stays correct either way.
var Jakarta = loadJakarta()

func loadJakarta() *time.Location {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		return time.FixedZone("WIB", 7*60*60)
	}
	return loc
}

// Now returns the current time in the Asia/Jakarta timezone.
func Now() time.Time {
	return time.Now().In(Jakarta)
}
