package brain

import (
	"strings"
	"testing"
)

func TestResolveMemoryPathJail(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"/pinned.md", false},
		{"pinned.md", false},
		{"/memory/people/alice.md", false},
		{"/people/alice.md", false},
		{"decisions/2026-07-27-x.md", false},
		{"", true},
		{"../outside.md", false}, // cleaned to /outside.md inside root
		{"/../../etc/passwd", false},
		{"/people/../../../../etc/passwd", false},
	}
	for _, c := range cases {
		got, err := ResolveMemoryPath(root, c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ResolveMemoryPath(%q) expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ResolveMemoryPath(%q) unexpected error: %v", c.in, err)
			continue
		}
		if !strings.HasPrefix(got, root) {
			t.Errorf("ResolveMemoryPath(%q) escaped root: %q", c.in, got)
		}
	}
}

func TestMemoryFSRoundTrip(t *testing.T) {
	root := t.TempDir()

	if err := MemoryFSWrite(root, "/people/alice.md", "# Alice\n\nRole: designer\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	content, err := MemoryFSRead(root, "people/alice.md")
	if err != nil || !strings.Contains(content, "designer") {
		t.Fatalf("read back: %v content=%q", err, content)
	}

	if err := MemoryFSEdit(root, "/people/alice.md", "designer", "design lead"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	content, _ = MemoryFSRead(root, "/people/alice.md")
	if !strings.Contains(content, "design lead") {
		t.Fatalf("edit didn't land: %q", content)
	}

	if err := MemoryFSEdit(root, "/people/alice.md", "not-present", "x"); err == nil {
		t.Fatal("edit with missing old_string should error")
	}

	MemoryFSWrite(root, "/decisions/2026-07-27-pricing.md", "# Pricing decision\n")
	if got := MemoryFSGlob(root, "/people/*.md"); len(got) != 1 || got[0] != "/people/alice.md" {
		t.Fatalf("glob people: %v", got)
	}
	if got := MemoryFSGlob(root, "*.md"); len(got) != 2 {
		t.Fatalf("glob basename: %v", got)
	}
	if hits := MemoryFSGrep(root, "PRICING", 10); len(hits) != 1 || !strings.HasPrefix(hits[0], "/decisions/") {
		t.Fatalf("grep: %v", hits)
	}
}

func TestSenderSlug(t *testing.T) {
	cases := map[string]string{
		"Alice O'Brien": "alice-obrien",
		"Nico":          "nico",
		"José García":   "jos-garc-a",
		"  spaced  ":    "spaced",
	}
	for in, want := range cases {
		if got := SenderSlug(in); got != want {
			t.Errorf("SenderSlug(%q) = %q, want %q", in, got, want)
		}
	}
}
