// Package prompt gathers project context for the system prompt:
// AGENTS.md files and agent skills (https://agentskills.io).
//
//	~/.ernest/AGENTS.md         user-wide
//	/AGENTS.md ... <wd>/AGENTS.md  ancestors of wd, outermost first
//
//	<wd>/.ernest/skills/<name>/SKILL.md   project skills win
//	<wd>/.agents/skills/<name>/SKILL.md   over user skills
//	~/.ernest/skills/<name>/SKILL.md
//	~/.agents/skills/<name>/SKILL.md
//
// AGENTS.md files are inlined. Skills are listed by name, description and
// location only; the model reads a SKILL.md when a task calls for it.
package prompt

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	agentsFile = "AGENTS.md"
	skillFile  = "SKILL.md"
	ernestDir  = ".ernest"
	agentsDir  = ".agents"
	skillsDir  = "skills"

	fence   = "---"
	keyName = "name"
	keyDesc = "description"
)

// Doc is an AGENTS.md file.
type Doc struct {
	Path string
	Text string
}

// Skill is a SKILL.md's frontmatter and location.
type Skill struct {
	Name        string
	Description string
	Path        string
}

// Context is what Load found.
type Context struct {
	Docs   []Doc
	Skills []Skill
}

// Load gathers AGENTS.md files and skills visible from wd. Missing files
// are skipped; unreadable or malformed ones are reported in the error,
// alongside whatever did load.
func Load(wd, home string) (Context, error) {
	var c Context
	var errs []error

	for _, p := range docPaths(wd, home) {
		data, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		c.Docs = append(c.Docs, Doc{Path: p, Text: strings.TrimSpace(string(data))})
	}

	// First dir wins a name clash.
	seen := map[string]bool{}
	for _, dir := range skillDirs(wd, home) {
		skills, err := scan(dir)
		errs = append(errs, err)
		for _, s := range skills {
			if seen[s.Name] {
				continue
			}
			seen[s.Name] = true
			c.Skills = append(c.Skills, s)
		}
	}
	return c, errors.Join(errs...)
}

// docPaths is the user AGENTS.md, then one per ancestor of wd, root first.
func docPaths(wd, home string) []string {
	var dirs []string
	for d := wd; ; d = filepath.Dir(d) {
		dirs = append(dirs, d)
		if d == filepath.Dir(d) {
			break
		}
	}

	paths := []string{filepath.Join(home, ernestDir, agentsFile)}
	for _, d := range slices.Backward(dirs) {
		paths = append(paths, filepath.Join(d, agentsFile))
	}
	return paths
}

// skillDirs lists skill roots by precedence.
func skillDirs(wd, home string) []string {
	return []string{
		filepath.Join(wd, ernestDir, skillsDir),
		filepath.Join(wd, agentsDir, skillsDir),
		filepath.Join(home, ernestDir, skillsDir),
		filepath.Join(home, agentsDir, skillsDir),
	}
}

// scan reads <dir>/*/SKILL.md. A missing dir yields no skills.
func scan(dir string) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var skills []Skill
	var errs []error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		p := filepath.Join(dir, e.Name(), skillFile)
		s, err := parse(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		skills = append(skills, s)
	}
	return skills, errors.Join(errs...)
}

// parse reads a SKILL.md's name and description. The name defaults to
// the directory's.
func parse(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}

	fm := frontmatter(string(data))
	s := Skill{Name: fm[keyName], Description: fm[keyDesc], Path: path}
	if s.Name == "" {
		s.Name = filepath.Base(filepath.Dir(path))
	}
	if s.Description == "" {
		return Skill{}, fmt.Errorf("%s: no description", path)
	}
	return s, nil
}

// frontmatter reads top-level "key: value" pairs between "---" fences.
// Block scalars ("key: >" or "key: |") and indented continuation lines
// are folded into one line:
//
//	---
//	name: pdf
//	description: >
//	  Fill PDF forms.   →  {"name": "pdf", "description": "Fill PDF forms. Merge PDFs."}
//	  Merge PDFs.
//	---
func frontmatter(text string) map[string]string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != fence {
		return nil
	}

	fm := map[string]string{}
	key := ""
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == fence {
			break
		}

		// Indented: continues the previous key.
		if line != strings.TrimLeft(line, " \t") {
			if key != "" {
				fm[key] = strings.TrimSpace(fm[key] + " " + strings.TrimSpace(line))
			}
			continue
		}

		k, v, ok := strings.Cut(line, ":")
		if !ok {
			key = ""
			continue
		}
		key = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if blockScalar(v) {
			v = ""
		}
		fm[key] = unquote(v)
	}
	return fm
}

// blockScalar reports a YAML block indicator, e.g. ">", "|-" or ">+".
func blockScalar(v string) bool {
	if v == "" || (v[0] != '>' && v[0] != '|') {
		return false
	}
	return strings.Trim(v[1:], "-+") == ""
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// Render is the context as a system prompt suffix; empty if none.
//
//	# Project instructions
//	## /repo/AGENTS.md
//	...
//	# Skills
//	...
//	<available_skills>
//	  <skill><name>pdf</name><description>...</description><location>...</location></skill>
//	</available_skills>
func (c Context) Render() string {
	var b strings.Builder

	if len(c.Docs) > 0 {
		b.WriteString("\n\n# Project instructions\n")
		for _, d := range c.Docs {
			fmt.Fprintf(&b, "\n## %s\n\n%s\n", d.Path, d.Text)
		}
	}

	if len(c.Skills) > 0 {
		b.WriteString("\n\n# Skills\n\n")
		b.WriteString("When a task matches a skill's description, read its SKILL.md with the read tool first and follow it. ")
		b.WriteString("Resolve relative paths in a skill against its directory.\n\n")
		b.WriteString("<available_skills>\n")
		for _, s := range c.Skills {
			fmt.Fprintf(&b, "  <skill><name>%s</name><description>%s</description><location>%s</location></skill>\n",
				s.Name, s.Description, s.Path)
		}
		b.WriteString("</available_skills>\n")
	}
	return b.String()
}
