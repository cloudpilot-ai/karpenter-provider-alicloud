// reasonable contains a set of reasonable defaults that disagree with upstream alternatives
package reasonable

import (
	"time"

	"golang.org/x/time/rate"
	"k8s.io/client-go/util/workqueue"
)

func RateLimiter() workqueue.RateLimiter {
	return workqueue.NewMaxOfRateLimiter(
		workqueue.NewItemExponentialFailureRateLimiter(100*time.Millisecond, 1*time.Minute),
		&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
	)
}
