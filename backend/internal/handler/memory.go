package handler

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/metadist/synaplan-opencloud/internal/synaplanapi"
)

const (
	// memoryCheckTimeout bounds the memory-service health check, which
	// runs under the cache lock.
	memoryCheckTimeout = 5 * time.Second
	// memoryCheckEvery is how long one answer of the health check holds.
	memoryCheckEvery = 5 * time.Minute
	// memorySearchLimit is how many memories one question may bring in.
	memorySearchLimit = 6
	// memorySearchTimeout bounds the optional memory lookup. The
	// generated client has no HTTP timeout, and the chat context lives
	// for summaryTimeout. A stalled memory service must not hold the
	// question: when this deadline passes, the question is asked
	// without memories.
	memorySearchTimeout = 5 * time.Second
)

// memoryCache keeps the last answer of Synaplan's memory-service health
// check, so a question does not pay for it every time.
type memoryCache struct {
	mu sync.Mutex
	ok bool
	at time.Time
}

// memoriesAvailable asks Synaplan at most once every memoryCheckEvery
// whether its memory service answers. A failed check counts as
// unavailable.
func (h *Handler) memoriesAvailable(ctx context.Context) bool {
	if h.synaplanAPI == nil {
		return false
	}
	h.memory.mu.Lock()
	defer h.memory.mu.Unlock()
	if !h.memory.at.IsZero() && time.Since(h.memory.at) < memoryCheckEvery {
		return h.memory.ok
	}
	checkCtx, cancel := context.WithTimeout(ctx, memoryCheckTimeout)
	defer cancel()
	res, err := h.synaplanAPI.GetApiConfigMemoryServiceCheckWithResponse(checkCtx)
	if err != nil {
		log.Printf("memory service check: %v", err)
	} else if res.StatusCode() != http.StatusOK {
		log.Printf("memory service check: status %d", res.StatusCode())
	}
	available := err == nil && res.JSON200 != nil && res.JSON200.Available != nil && *res.JSON200.Available
	h.memory.ok = available
	h.memory.at = time.Now()
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
	limit := memorySearchLimit
	searchCtx, cancel := context.WithTimeout(ctx, memorySearchTimeout)
	defer cancel()
	res, err := h.synaplanAPI.PostAppUsermemorySearchmemoriesWithResponse(searchCtx, synaplanapi.PostAppUsermemorySearchmemoriesJSONRequestBody{
		Query: question,
		Limit: &limit,
	})
	if err != nil {
		log.Printf("memory search: %v", err)
		return ""
	}
	if res.StatusCode() != http.StatusOK || res.JSON200 == nil || res.JSON200.Memories == nil {
		if res.StatusCode() != http.StatusOK {
			log.Printf("memory search: status %d", res.StatusCode())
		}
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
