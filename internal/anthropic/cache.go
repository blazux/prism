package anthropic

import "os"

// The default wire representation keeps Anthropic's five-minute cache policy.
// Only an explicit opt-in requests the longer, more expensive write lifetime.
func configureCacheTTL(request *apiRequest) {
	if os.Getenv("PRISM_ANTHROPIC_CACHE_TTL") != "1h" {
		return
	}
	set := func(control *cacheControl) {
		if control != nil {
			control.TTL = "1h"
		}
	}
	set(request.CacheControl)
	for i := range request.Tools {
		set(request.Tools[i].CacheControl)
	}
	for i := range request.System {
		set(request.System[i].CacheControl)
	}
}
