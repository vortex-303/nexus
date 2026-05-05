package brain3

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

// MemoryStoreName returns the Anthropic memory_store name we provision per
// workspace. Anthropic auto-derives the mount path from this — the FUSE
// mount inside the session container ends up at /mnt/memory/<store-name>.
// We can't override the mount path on the resource attachment (no mount_path
// field on BetaManagedAgentsMemoryStoreResourceParam), so naming + mount
// path are coupled.
func MemoryStoreName(slug string) string {
	return "nexus-brain-" + slug
}

// MemoryMountPath returns the actual mount point inside the session container
// for the given workspace's memory_store. Used to teach the agent (in the
// system prompt addendum) where to read/write its memory files.
func MemoryMountPath(slug string) string {
	return "/mnt/memory/" + MemoryStoreName(slug)
}

// PinnedPath is the always-include constraint file inside the store.
const PinnedPath = "/pinned.md"

// IndexPath is the agent-maintained map of what's stored.
const IndexPath = "/INDEX.md"

// PeoplePath returns the per-member profile path for a sender slug.
// Sender slug is sender display name, lowercased + non-alnum stripped.
func PeoplePath(senderSlug string) string {
	if senderSlug == "" {
		return ""
	}
	return "/people/" + senderSlug + ".md"
}

// SenderSlug normalizes a display name into a stable filename component.
// "Alice O'Brien" → "alice-obrien". Empty input returns "".
func SenderSlug(displayName string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range displayName {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			prevDash = false
		case !prevDash && b.Len() > 0:
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	return out
}

// ensureMemoryStore creates a memory_store for the workspace if one isn't
// already persisted in brain_settings. Returns the store ID.
func ensureMemoryStore(ctx context.Context, client *anthropic.Client, settings SettingsStore, slug string) (string, error) {
	if id := settings.Get(slug, "mga_memory_store_id"); id != "" {
		return id, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	store, err := client.Beta.MemoryStores.New(ctx, anthropic.BetaMemoryStoreNewParams{
		Name:        MemoryStoreName(slug),
		Description: param.NewOpt("Brain v3 persistent memory for Nexus workspace " + slug),
	})
	if err != nil {
		return "", fmt.Errorf("create memory_store: %w", err)
	}

	if err := settings.Set(slug, "mga_memory_store_id", store.ID); err != nil {
		return "", fmt.Errorf("persist memory_store id: %w", err)
	}
	return store.ID, nil
}

// PreloadedContext holds memory snippets read from the store before each
// turn. Pre-injecting these into the user message saves the agent from
// having to read them every turn and guarantees they land in context.
type PreloadedContext struct {
	Pinned        string // contents of pinned.md, "" if not present
	SenderProfile string // contents of people/<sender>.md, "" if not present
}

// IsEmpty reports whether the preload added nothing — used to skip emitting
// an empty <context> block.
func (c PreloadedContext) IsEmpty() bool {
	return c.Pinned == "" && c.SenderProfile == ""
}

// Render formats the preload as a <context> block to prepend to the user message.
// The agent treats <context>...</context> as ground-truth metadata, not user
// instruction, by convention (reinforced in the system-prompt addendum).
func (c PreloadedContext) Render(senderName string) string {
	if c.IsEmpty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("<context>\n")
	if c.Pinned != "" {
		b.WriteString("# Pinned (always-relevant constraints)\n\n")
		b.WriteString(strings.TrimSpace(c.Pinned))
		b.WriteString("\n\n")
	}
	if c.SenderProfile != "" {
		b.WriteString("# About ")
		b.WriteString(senderName)
		b.WriteString("\n\n")
		b.WriteString(strings.TrimSpace(c.SenderProfile))
		b.WriteString("\n\n")
	}
	b.WriteString("</context>\n\n")
	return b.String()
}

// LoadPreloadedContext fetches pinned.md and the sender's profile (if either
// exists) from the workspace's memory_store. Missing files are skipped, not
// fatal — a fresh workspace has neither and that's fine.
//
// Implementation: list with exact path as prefix, view=full to get content
// inline. One call per file. ~50–200ms per call; both run sequentially because
// they're cheap and ordered logging beats a parallel goroutine here.
func LoadPreloadedContext(ctx context.Context, client *anthropic.Client, storeID, senderSlug string) (PreloadedContext, error) {
	if storeID == "" {
		return PreloadedContext{}, errors.New("brain3: memory_store id is empty")
	}
	pinned, err := readMemoryAtPath(ctx, client, storeID, PinnedPath)
	if err != nil {
		return PreloadedContext{}, fmt.Errorf("read pinned.md: %w", err)
	}
	var profile string
	if senderSlug != "" {
		profile, err = readMemoryAtPath(ctx, client, storeID, PeoplePath(senderSlug))
		if err != nil {
			return PreloadedContext{}, fmt.Errorf("read sender profile: %w", err)
		}
	}
	return PreloadedContext{Pinned: pinned, SenderProfile: profile}, nil
}

// readMemoryAtPath returns the content of the memory at the given path, or
// "" if it doesn't exist. Uses list+path_prefix because there's no GET-by-path
// endpoint; the prefix match returns at most one entry for an exact path.
func readMemoryAtPath(ctx context.Context, client *anthropic.Client, storeID, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	page, err := client.Beta.MemoryStores.Memories.List(ctx, storeID, anthropic.BetaMemoryStoreMemoryListParams{
		PathPrefix: param.NewOpt(path),
		Limit:      param.NewOpt[int64](1),
		View:       anthropic.BetaManagedAgentsMemoryViewFull,
	})
	if err != nil {
		return "", err
	}
	for _, item := range page.Data {
		// Prefix is a raw string-prefix match; verify exact path so we don't
		// false-match e.g. "/people/alice" to "/people/alice-2".
		if item.Path == path {
			return item.Content, nil
		}
	}
	return "", nil
}
