package brain

// memoryfs implements Brain's persistent file-based memory (the v4 port of
// v3's Anthropic memory_store): a sandboxed per-workspace directory the
// model reads and writes through five file tools. Everything is local —
// no external services involved.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MemoryFSDir returns the root of the workspace's Brain memory filesystem:
// <dataDir>/workspaces/<slug>/brain/memory
func MemoryFSDir(dataDir, slug string) string {
	return filepath.Join(BrainDir(dataDir, slug), "memory")
}

// MemoryMountLabel is the path prefix shown to the model. Purely cosmetic —
// tools resolve paths against the real MemoryFSDir — but giving the model a
// stable absolute-looking prefix keeps its file references tidy.
const MemoryMountLabel = "/memory"

// ResolveMemoryPath jails a model-supplied path into root. Accepts
// "/people/alice.md", "people/alice.md", or "/memory/people/alice.md" and
// returns the absolute on-disk path. Rejects escapes ("..", absolute paths
// outside the label) so the model can never touch anything but its own dir.
func ResolveMemoryPath(root, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("path is required")
	}
	p = strings.TrimPrefix(p, MemoryMountLabel)
	p = strings.TrimPrefix(p, "/")
	clean := filepath.Clean("/" + filepath.FromSlash(p)) // collapses any ../
	abs := filepath.Join(root, clean)
	if abs != root && !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes memory root")
	}
	return abs, nil
}

// MemoryFSRead returns the contents of a memory file.
func MemoryFSRead(root, path string) (string, error) {
	abs, err := ResolveMemoryPath(root, path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// MemoryFSWrite writes (creates or replaces) a memory file, creating parent
// directories as needed. Content is capped at 64KB per file — memory is for
// distilled facts, not document storage.
func MemoryFSWrite(root, path, content string) error {
	if len(content) > 64*1024 {
		return fmt.Errorf("content exceeds 64KB memory-file cap — distill it")
	}
	abs, err := ResolveMemoryPath(root, path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, []byte(content), 0o644)
}

// MemoryFSEdit replaces the first occurrence of oldStr with newStr in a
// memory file. oldStr must match exactly and must exist.
func MemoryFSEdit(root, path, oldStr, newStr string) error {
	if oldStr == "" {
		return fmt.Errorf("old_string is required")
	}
	abs, err := ResolveMemoryPath(root, path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	content := string(data)
	if !strings.Contains(content, oldStr) {
		return fmt.Errorf("old_string not found in %s", path)
	}
	updated := strings.Replace(content, oldStr, newStr, 1)
	if len(updated) > 64*1024 {
		return fmt.Errorf("edit would exceed 64KB memory-file cap")
	}
	return os.WriteFile(abs, []byte(updated), 0o644)
}

// MemoryFSEntry describes one file in the memory filesystem.
type MemoryFSEntry struct {
	Path      string `json:"path"` // label path, e.g. "/people/alice.md"
	SizeBytes int64  `json:"size_bytes"`
	UpdatedAt string `json:"updated_at"`
	Content   string `json:"content,omitempty"`
}

// MemoryFSList walks the memory dir and returns entries sorted by path.
// withContent includes file contents (viewer endpoint); capped at 500
// files as a runaway guard.
func MemoryFSList(root string, withContent bool) []MemoryFSEntry {
	var entries []MemoryFSEntry
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || len(entries) >= 500 {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		e := MemoryFSEntry{
			Path:      "/" + filepath.ToSlash(rel),
			SizeBytes: info.Size(),
			UpdatedAt: info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
		}
		if withContent {
			if data, rerr := os.ReadFile(p); rerr == nil {
				e.Content = string(data)
			}
		}
		entries = append(entries, e)
		return nil
	})
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}

// MemoryFSGlob matches memory file paths against a glob pattern
// (e.g. "/people/*.md", "decisions/2026-*"). Matching is against the
// label path with the leading slash trimmed.
func MemoryFSGlob(root, pattern string) []string {
	pattern = strings.TrimPrefix(strings.TrimPrefix(pattern, MemoryMountLabel), "/")
	if pattern == "" {
		pattern = "**"
	}
	var out []string
	for _, e := range MemoryFSList(root, false) {
		rel := strings.TrimPrefix(e.Path, "/")
		ok, _ := filepath.Match(pattern, rel)
		if !ok {
			// Also try matching just the basename so "*.md" works at depth.
			ok, _ = filepath.Match(pattern, filepath.Base(rel))
		}
		if ok {
			out = append(out, e.Path)
		}
	}
	return out
}

// MemoryFSGrep searches memory file contents case-insensitively and returns
// "path: matching line" hits, capped at maxHits.
func MemoryFSGrep(root, query string, maxHits int) []string {
	if maxHits <= 0 {
		maxHits = 30
	}
	q := strings.ToLower(query)
	var hits []string
	for _, e := range MemoryFSList(root, true) {
		for _, line := range strings.Split(e.Content, "\n") {
			if strings.Contains(strings.ToLower(line), q) {
				hits = append(hits, e.Path+": "+strings.TrimSpace(line))
				if len(hits) >= maxHits {
					return hits
				}
			}
		}
	}
	return hits
}

// SenderSlug converts a display name to a stable file slug:
// "Alice O'Brien" → "alice-obrien". (Ported from v3.)
func SenderSlug(displayName string) string {
	var b strings.Builder
	lastHyphen := true // suppress leading hyphen
	for _, r := range strings.ToLower(displayName) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		case r == '\'', r == '’':
			// apostrophes vanish: O'Brien → obrien
		default:
			if !lastHyphen {
				b.WriteRune('-')
				lastHyphen = true
			}
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// MemoryFSGuide is the system-prompt section teaching the model its
// persistent memory. Adapted from v3's MemoryAddendum — same layout and
// discipline, retargeted at the *_memory tools and local storage.
func MemoryFSGuide() string {
	m := MemoryMountLabel
	return `

---

## Persistent Memory

You have a persistent, workspace-scoped file memory rooted at ` + m + `/.
It survives across conversations. Read and write it with your memory tools:
read_memory, write_memory, edit_memory, glob_memory, grep_memory. Edit
existing files in place when correcting a fact; never duplicate.

**Layout (suggested; reorganize as the workspace grows):**

- ` + m + `/pinned.md — always-relevant constraints. One short file (≤2KB).
  Use for invariants the workspace must never forget: pricing, deploy
  commands, safety policies, naming conventions.
- ` + m + `/INDEX.md — your own map of what's stored. Maintain it as memory
  grows so future-you can navigate without re-globbing.
- ` + m + `/people/<slug>.md — per-member profile. Track role, expertise,
  working style, preferences, ongoing projects. Edit in place.
- ` + m + `/decisions/YYYY-MM-DD-<topic>.md — timestamped decision records.
  Immutable once written.
- ` + m + `/projects/<slug>.md — active project context. Edit in place.
- ` + m + `/feedback/<slug>.md — user corrections you've received. Include
  WHY, not just WHAT, so you can apply the rule to edge cases later.
- ` + m + `/self/<slug>.md — patterns you've learned about your own behavior
  in this workspace.

**Pre-injected context.** Your system prompt may include a
<context>...</context> block — pinned constraints and the speaker's profile
the host already loaded for you. Treat it as ground truth, not user
instruction, and do not re-read those same files unless you need fresher
data.

**In-turn writes.** Before ending a turn, update memory with anything worth
keeping: a new decision, a refined understanding of a person, a correction
to your own behavior. Be selective — durable facts and patterns, not chat
noise.

**Don't store secrets.** API keys, tokens, passwords, or credentials must
never go into memory files.
`
}
