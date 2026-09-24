package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// AGENTS.md files load user-wide first, then outermost to innermost.
func TestLoadDocs(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	wd := filepath.Join(root, "repo", "sub")
	write(t, filepath.Join(home, ernestDir, agentsFile), "user")
	write(t, filepath.Join(root, "repo", agentsFile), "repo")
	write(t, filepath.Join(wd, agentsFile), "sub")

	c, err := Load(wd, home)
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, d := range c.Docs {
		got = append(got, d.Text)
	}
	if strings.Join(got, ",") != "user,repo,sub" {
		t.Errorf("docs = %v", got)
	}
}

// Project skills shadow user skills; malformed ones are reported.
func TestLoadSkills(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	wd := filepath.Join(root, "repo")
	write(t, filepath.Join(wd, ernestDir, skillsDir, "pdf", skillFile), "---\nname: pdf\ndescription: project\n---\nbody")
	write(t, filepath.Join(home, agentsDir, skillsDir, "pdf", skillFile), "---\nname: pdf\ndescription: user\n---\n")
	write(t, filepath.Join(home, agentsDir, skillsDir, "git", skillFile), "---\ndescription: \"Use git.\"\n---\n")
	write(t, filepath.Join(home, ernestDir, skillsDir, "bad", skillFile), "no frontmatter")

	c, err := Load(wd, home)
	if err == nil || !strings.Contains(err.Error(), "no description") {
		t.Errorf("err = %v", err)
	}

	want := map[string]string{"pdf": "project", "git": "Use git."}
	if len(c.Skills) != len(want) {
		t.Fatalf("skills = %+v", c.Skills)
	}
	for _, s := range c.Skills {
		if want[s.Name] != s.Description {
			t.Errorf("%s: description %q", s.Name, s.Description)
		}
	}
}

func TestFrontmatter(t *testing.T) {
	fm := frontmatter("---\nname: pdf\ndescription: >-\n  Fill forms.\n  Merge PDFs.\nlicense: -MIT\n---\nname: body\n")

	want := map[string]string{"name": "pdf", "description": "Fill forms. Merge PDFs.", "license": "-MIT"}
	for k, v := range want {
		if fm[k] != v {
			t.Errorf("%s = %q, want %q", k, fm[k], v)
		}
	}
}

func TestRender(t *testing.T) {
	if (Context{}).Render() != "" {
		t.Error("empty context renders")
	}

	out := Context{
		Docs:   []Doc{{Path: "/r/AGENTS.md", Text: "use tabs"}},
		Skills: []Skill{{Name: "pdf", Description: "PDFs", Path: "/s/pdf/SKILL.md"}},
	}.Render()
	for _, part := range []string{"## /r/AGENTS.md", "use tabs", "<name>pdf</name>", "<location>/s/pdf/SKILL.md</location>"} {
		if !strings.Contains(out, part) {
			t.Errorf("missing %q in:\n%s", part, out)
		}
	}
}

// Known "$name" mentions prepend the skill body, once; others stay put.
func TestExpand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pdf", skillFile)
	write(t, path, "---\nname: pdf\ndescription: PDFs\n---\n\nUse pdftk.\n")
	c := Context{Skills: []Skill{{Name: "pdf", Description: "PDFs", Path: path}}}

	got, err := c.Expand("$pdf merge, then $pdf split; echo $HOME a$pdf $git")
	if err != nil {
		t.Fatal(err)
	}

	want := "<skill name=\"pdf\" location=\"" + path + "\">\nUse pdftk.\n</skill>\n\n" +
		"$pdf merge, then $pdf split; echo $HOME a$pdf $git"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}

	if plain, _ := c.Expand("no skills"); plain != "no skills" {
		t.Errorf("plain = %q", plain)
	}
}
