package lint

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LoadSkills returns every installed skill available for brief routing:
// one entry per SKILL.md found directly under a skill-name directory in
// ~/.agents/skills and under repoRoot/.agents/skills, with its front
// matter's name and description. A skill under repoRoot overrides a
// same-named global skill, since the repository's copy is the more
// specific one. Entries are sorted by name for reproducible question
// ordering. A missing skills directory contributes no entries, and a skill
// directory with no SKILL.md, or a SKILL.md with no front matter
// description, is skipped; neither is an error. LoadSkills returns an error
// when the home directory cannot be resolved (HOME unset or empty), when a
// skills directory exists but cannot be read, or when a SKILL.md exists but
// cannot be read: routing over a partial skill set would pass silently.
func LoadSkills(repoRoot string) ([]Skill, error) {
	byName := map[string]Skill{}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("lint: resolving the home directory for ~/.agents/skills: %w", err)
	}
	dirs := []string{filepath.Join(home, ".agents", "skills")}
	if repoRoot != "" {
		dirs = append(dirs, filepath.Join(repoRoot, ".agents", "skills"))
	}
	for _, dir := range dirs {
		if err := addSkillsFrom(byName, dir); err != nil {
			return nil, err
		}
	}

	skills := make([]Skill, 0, len(byName))
	for _, s := range byName {
		skills = append(skills, s)
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills, nil
}

// addSkillsFrom reads every "<dir>/<skill>/SKILL.md" under dir into byName,
// keyed by the skill's front-matter name (falling back to the directory
// name when front matter carries none), overwriting any entry already
// present for that name. A missing dir or SKILL.md is skipped; any other
// read error is returned.
func addSkillsFrom(byName map[string]Skill, dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lint: reading skills directory: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), "SKILL.md")
		name, desc, err := readFrontMatter(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("lint: reading %s: %w", path, err)
		}
		if desc == "" {
			continue
		}
		if name == "" {
			name = e.Name()
		}
		byName[name] = Skill{Name: name, Description: desc}
	}
	return nil
}

// readFrontMatter reads the "name" and "description" fields from a
// SKILL.md's YAML front matter (the "---" delimited block at the top of the
// file). A file with no front matter, or front matter with no description,
// yields an empty description — routing has nothing to judge without one.
// It returns an error only when the file cannot be opened or read.
func readFrontMatter(path string) (name, description string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return "", "", scanner.Err()
	}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "name":
			name = value
		case "description":
			description = value
		}
	}
	return name, description, scanner.Err()
}
