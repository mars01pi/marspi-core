package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		wantOK   bool
		wantDesc string
		wantTags []string
		wantBody string
	}{
		{
			name: "valid",
			content: `---
name: demo
description: A demo skill
tags: [a, b, c]
# comment ignored
---
# Body

Hello.`,
			wantOK:   true,
			wantDesc: "A demo skill",
			wantTags: []string{"a", "b", "c"},
			wantBody: "# Body\n\nHello.",
		},
		{
			name:    "no frontmatter",
			content: "# Just markdown",
			wantOK:  false,
		},
		{
			name:    "unclosed frontmatter",
			content: "---\ndescription: x\n",
			wantOK:  false,
		},
		{
			name: "empty tags and quoted values",
			content: `---
description: hello
tags: ["x", 'y']
---
body`,
			wantOK:   true,
			wantDesc: "hello",
			wantTags: []string{"x", "y"},
			wantBody: "body",
		},
		{
			name: "lines without colon ignored",
			content: `---
description: ok
not-a-pair
---
x`,
			wantOK:   true,
			wantDesc: "ok",
			wantBody: "x",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			meta, tags, body, ok := parseFrontmatter(tt.content)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if meta["description"] != tt.wantDesc {
				t.Fatalf("description = %q, want %q", meta["description"], tt.wantDesc)
			}
			if body != tt.wantBody {
				t.Fatalf("body = %q, want %q", body, tt.wantBody)
			}
			if len(tags) != len(tt.wantTags) {
				t.Fatalf("tags = %v, want %v", tags, tt.wantTags)
			}
			for i := range tt.wantTags {
				if tags[i] != tt.wantTags[i] {
					t.Fatalf("tags[%d] = %q, want %q", i, tags[i], tt.wantTags[i])
				}
			}
		})
	}
}

func TestParseTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want []string
	}{
		{"[a, b, c]", []string{"a", "b", "c"}},
		{"a, b", []string{"a", "b"}},
		{`["one", 'two']`, []string{"one", "two"}},
		{"[]", nil},
		{"", nil},
		{"  [ , , x , ]  ", []string{"x"}},
	}
	for _, tt := range tests {
		got := parseTags(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("parseTags(%q) = %v, want %v", tt.in, got, tt.want)
			continue
		}
		for i := range tt.want {
			if got[i] != tt.want[i] {
				t.Errorf("parseTags(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
			}
		}
	}
}

func TestManagerLoadAndQuery(t *testing.T) {
	base := mustTempDir(t, "skill-test-*")
	skillsRoot := filepath.Join(base, "skills")

	writeSkill(t, skillsRoot, "greeter", `---
description: Says hello
tags: [demo, greet]
---
# Greeter

Say hello.`, map[string]string{
		"scripts/hello.sh":    "echo hello\n",
		"references/notes.md": "note content\n",
	})
	writeSkill(t, skillsRoot, "broken", "# no frontmatter\n", nil)

	m := New([]string{skillsRoot})

	if _, ok := m.Get("broken"); ok {
		t.Fatal("skill without frontmatter should be skipped")
	}

	s, ok := m.Get("greeter")
	if !ok {
		t.Fatal("greeter skill missing")
	}
	if s.Name != "greeter" {
		t.Fatalf("Name = %q", s.Name)
	}
	if s.Description != "Says hello" {
		t.Fatalf("Description = %q", s.Description)
	}
	if s.Body != "# Greeter\n\nSay hello." {
		t.Fatalf("Body = %q", s.Body)
	}
	if len(s.Tags) != 2 || s.Tags[0] != "demo" || s.Tags[1] != "greet" {
		t.Fatalf("Tags = %v", s.Tags)
	}
	if s.Meta["description"] != "Says hello" {
		t.Fatalf("Meta description = %q", s.Meta["description"])
	}

	scriptPath := filepath.Join(skillsRoot, "greeter", "scripts", "hello.sh")
	if got := s.Scripts[scriptPath]; got != "echo hello\n" {
		t.Fatalf("Scripts[%q] = %q", scriptPath, got)
	}
	refPath := filepath.Join(skillsRoot, "greeter", "references", "notes.md")
	if got := s.References[refPath]; got != "note content\n" {
		t.Fatalf("References[%q] = %q", refPath, got)
	}

	all := m.All()
	if len(all) != 1 {
		t.Fatalf("All len = %d, want 1", len(all))
	}

	desc := m.Descriptions()
	if !strings.Contains(desc, "- greeter: Says hello") {
		t.Fatalf("Descriptions = %q", desc)
	}
}

func TestAddBasePathAndOverride(t *testing.T) {
	base := mustTempDir(t, "skill-override-*")
	first := filepath.Join(base, "first")
	second := filepath.Join(base, "second")

	writeSkill(t, first, "shared", `---
description: from first
---
body1`, nil)
	writeSkill(t, first, "only-first", `---
description: first only
---
x`, nil)
	writeSkill(t, second, "shared", `---
description: from second
---
body2`, nil)

	m := New([]string{first})
	if s, _ := m.Get("shared"); s.Description != "from first" {
		t.Fatalf("initial shared = %q", s.Description)
	}

	m.AddBasePath(second)
	if s, ok := m.Get("shared"); !ok || s.Description != "from second" {
		t.Fatalf("overridden shared = %#v", s)
	}
	if _, ok := m.Get("only-first"); !ok {
		t.Fatal("only-first should remain after reload")
	}
	if s, _ := m.Get("shared"); s.Body != "body2" {
		t.Fatalf("shared body = %q", s.Body)
	}
}

func TestNewDefaultBasePath(t *testing.T) {
	base := mustTempDir(t, "skill-home-*")
	home := filepath.Join(base, "home")
	mustMkdir(t, home)
	t.Setenv("HOME", home)

	skillsRoot := filepath.Join(home, ".marspicli", "skills")
	writeSkill(t, skillsRoot, "home-skill", `---
description: from home
---
ok`, nil)

	m := New(nil)
	s, ok := m.Get("home-skill")
	if !ok {
		t.Fatal("expected skill from default ~/.marspicli/skills")
	}
	if s.Description != "from home" {
		t.Fatalf("Description = %q", s.Description)
	}
}

func TestLoadDirectoryMissing(t *testing.T) {
	t.Parallel()
	base := mustTempDir(t, "skill-nodir-*")
	if got := loadDirectory(base, "scripts"); got != nil {
		t.Fatalf("missing dir should return nil, got %v", got)
	}
}

func TestDescriptionsEmpty(t *testing.T) {
	t.Parallel()
	base := mustTempDir(t, "skill-empty-*")
	m := New([]string{base})
	if got := m.Descriptions(); got != "" {
		t.Fatalf("Descriptions = %q, want empty", got)
	}
}

func writeSkill(t *testing.T, root, name, skillMD string, extras map[string]string) {
	t.Helper()
	dir := filepath.Join(root, name)
	mustMkdir(t, dir)
	writeFile(t, filepath.Join(dir, "SKILL.md"), skillMD)
	for rel, content := range extras {
		p := filepath.Join(dir, rel)
		mustMkdir(t, filepath.Dir(p))
		writeFile(t, p, content)
	}
}

func mustTempDir(t *testing.T, pattern string) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", pattern)
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", p, err)
	}
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", p, err)
	}
}
