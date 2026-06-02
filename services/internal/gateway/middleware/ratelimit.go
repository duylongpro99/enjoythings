package middleware

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"enjoythings/services/internal/auth"

	"github.com/google/uuid"
)

type RateLimiter struct {
	burst       int
	refillEvery time.Duration
	now         func() time.Time

	mu      sync.Mutex
	buckets map[uuid.UUID]*bucket
}

type bucket struct {
	tokens int
	last   time.Time
}

func NewRateLimiter(burst int, refillEvery time.Duration) *RateLimiter {
	return &RateLimiter{
		burst:       burst,
		refillEvery: refillEvery,
		now:         time.Now,
		buckets:     make(map[uuid.UUID]*bucket),
	}
}

func (limiter *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			writeRateLimitError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if !limiter.allow(principal.UserID) {
			writeRateLimitError(w, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (limiter *RateLimiter) allow(userID uuid.UUID) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	now := limiter.now()
	b, ok := limiter.buckets[userID]
	if !ok {
		limiter.buckets[userID] = &bucket{tokens: limiter.burst - 1, last: now}
		return true
	}

	if elapsed := now.Sub(b.last); elapsed >= limiter.refillEvery {
		refill := int(elapsed / limiter.refillEvery)
		b.tokens = min(limiter.burst, b.tokens+refill)
		b.last = b.last.Add(time.Duration(refill) * limiter.refillEvery)
	}
	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}

func writeRateLimitError(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}
