package handler

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
)

var (
	// memoryCheckTimeout bounds the memory-service health check.
	memoryCheckTimeout = 5 * time.Second
	// memorySearchTimeout bounds one per-user memory search so a stalled
	// memory service falls back to asking without memories rather than
	// consuming the chat deadline.
	memorySearchTimeout = 5 * time.Second
)

const (
	// memoryCheckEvery is how long a definitive health-check answer holds.
	memoryCheckEvery = 5 * time.Minute
	// memoryRetryAfter spaces retries after a transient check error (such
	// as one caller's expired token or a brief timeout), so one caller's
	// error does not disable memories for everyone for five minutes while
	// concurrent callers still avoid stampeding Synaplan.
	memoryRetryAfter = 15 * time.Second
	// memorySearchLimit is how many memories one question may bring in.
	memorySearchLimit = 6
)

// memoryCache keeps the last answer of Synaplan's memory-service health
// check, so a question does not pay for it every time.
type memoryCache struct {
	mu    sync.Mutex
	ok    bool
	until time.Time
}

// memoriesAvailable asks Synaplan whether its memory service answers.
// A definitive 200 response (`available: true` or `false`) is cached for
// memoryCheckEvery; a failed request or non-200 status is logged and
// retried after memoryRetryAfter.
func (h *Handler) memoriesAvailable(ctx context.Context) bool {
	if h.synaplanAPI == nil {
		return false
	}
	now := time.Now()
	h.memory.mu.Lock()
	if now.Before(h.memory.until) {
		ok := h.memory.ok
		h.memory.mu.Unlock()
		return ok
	}
	// Reserve a short retry window before releasing the lock so concurrent
	// chats do not queue behind a slow health check.
	h.memory.until = now.Add(memoryRetryAfter)
	ok := h.memory.ok
	h.memory.mu.Unlock()

	checkCtx, cancel := context.WithTimeout(ctx, memoryCheckTimeout)
	defer cancel()
	res, err := h.synaplanAPI.GetApiConfigMemoryServiceCheckWithResponse(checkCtx)
	if err != nil {
		log.Printf("memory check: %v", err)
		return ok
	}
	if res.StatusCode() != 200 || res.JSON200 == nil || res.JSON200.Available == nil {
		log.Printf("memory check: synaplan answered %d", res.StatusCode())
		return ok
	}
	available := *res.JSON200.Available
	h.memory.mu.Lock()
	h.memory.ok = available
	h.memory.until = time.Now().Add(memoryCheckEvery)
	h.memory.mu.Unlock()
	return available
}

// memoryContext is what Synaplan remembers about the person that fits
// the question, as lines for the model. It is empty with the shared API
// key, where every person would read the one account's memories, and
// when the memory service is unavailable or has nothing.
func (h *Handler) memoryContext(ctx context.Context, question string) string {
	if h.sharedKey || strings.TrimSpace(question) == "" || !h.memoriesAvailable(ctx) {
		return ""
	}
	searchCtx, cancel := context.WithTimeout(ctx, memorySearchTimeout)
	defer cancel()
	limit := memorySearchLimit
	res, err := h.synaplanAPI.PostAppUsermemorySearchmemoriesWithResponse(searchCtx, synaplanapi.PostAppUsermemorySearchmemoriesJSONRequestBody{
		Query: question,
		Limit: &limit,
	})
	if err != nil {
		log.Printf("memory search: %v", err)
		return ""
	}
	if res.JSON200 == nil || res.JSON200.Memories == nil {
		return ""
	}
	lines := make([]string, 0, len(*res.JSON200.Memories))
	for _, memory := range *res.JSON200.Memories {
		if memory.Value == nil || strings.TrimSpace(*memory.Value) == "" {
			continue
		}
		value := strings.TrimSpace(*memory.Value)
		if memory.Key != nil && strings.TrimSpace(*memory.Key) != "" {
			lines = append(lines, "- "+strings.TrimSpace(*memory.Key)+": "+value)
		} else {
			lines = append(lines, "- "+value)
		}
	}
	return strings.Join(lines, "\n")
}
