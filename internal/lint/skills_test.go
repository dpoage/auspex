package lint

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSkill writes "<dir>/<name>/SKILL.md" with the given front-matter
// description.
func writeSkill(t *testing.T, dir, name, description string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLoadSkillsCombinesGlobalAndRepo is criterion O4: brief routing asks
// one question per skill under both ~/.agents/skills and
// <repo>/.agents/skills, with each skill's description from its front
// matter. Mutant: read only the global dir.
func TestLoadSkillsCombinesGlobalAndRepo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalSkills := filepath.Join(home, ".agents", "skills")
	writeSkill(t, globalSkills, "global-only", "A skill installed only globally.")

	repoRoot := t.TempDir()
	repoSkills := filepath.Join(repoRoot, ".agents", "skills")
	writeSkill(t, repoSkills, "repo-only", "A skill installed only in this repository.")

	skills, err := LoadSkills(repoRoot)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	byName := map[string]Skill{}
	for _, s := range skills {
		byName[s.Name] = s
	}
	global, ok := byName["global-only"]
	if !ok {
		t.Fatalf("skills = %v, missing the globally installed skill", skills)
	}
	if global.Description != "A skill installed only globally." {
		t.Fatalf("global-only description = %q", global.Description)
	}
	repo, ok := byName["repo-only"]
	if !ok {
		t.Fatalf("skills = %v, missing the repository-installed skill (the global dir was read alone)", skills)
	}
	if repo.Description != "A skill installed only in this repository." {
		t.Fatalf("repo-only description = %q", repo.Description)
	}
}

// TestLoadSkillsRepoOverridesGlobal documents the same-name resolution: a
// repository skill takes precedence over a same-named global one.
func TestLoadSkillsRepoOverridesGlobal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeSkill(t, filepath.Join(home, ".agents", "skills"), "shared", "global description")

	repoRoot := t.TempDir()
	writeSkill(t, filepath.Join(repoRoot, ".agents", "skills"), "shared", "repo description")

	skills, err := LoadSkills(repoRoot)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}
	if len(skills) != 1 || skills[0].Description != "repo description" {
		t.Fatalf("skills = %+v, want one entry with the repo description", skills)
	}
}

// TestLoadSkillsFailsWhenSkillsCannotBeRead defends fail-closed over a
// partial skill set: LoadSkills errors when HOME cannot be resolved, when
// a skills directory exists but cannot be read, or when a SKILL.md exists
// but cannot be read. A missing skills directory is not an error.
// Mutant: ignore the ReadDir error.
func TestLoadSkillsFailsWhenSkillsCannotBeRead(t *testing.T) {
	t.Run("HOME unset", func(t *testing.T) {
		t.Setenv("HOME", "")
		os.Unsetenv("HOME")
		if skills, err := LoadSkills(t.TempDir()); err == nil {
			t.Fatalf("LoadSkills = %v, nil; want an error when HOME is unset", skills)
		}
	})
	t.Run("HOME empty", func(t *testing.T) {
		t.Setenv("HOME", "")
		if skills, err := LoadSkills(t.TempDir()); err == nil {
			t.Fatalf("LoadSkills = %v, nil; want an error when HOME is empty", skills)
		}
	})
	t.Run("global skills directory unreadable", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		unreadableDir(t, filepath.Join(home, ".agents", "skills"))
		if skills, err := LoadSkills(t.TempDir()); err == nil {
			t.Fatalf("LoadSkills = %v, nil; want an error for an unreadable ~/.agents/skills", skills)
		}
	})
	t.Run("repository skills directory unreadable", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		repoRoot := t.TempDir()
		unreadableDir(t, filepath.Join(repoRoot, ".agents", "skills"))
		if skills, err := LoadSkills(repoRoot); err == nil {
			t.Fatalf("LoadSkills = %v, nil; want an error for an unreadable <repo>/.agents/skills", skills)
		}
	})
	t.Run("SKILL.md unreadable", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		skills := filepath.Join(home, ".agents", "skills")
		writeSkill(t, skills, "locked", "A skill nobody can read.")
		path := filepath.Join(skills, "locked", "SKILL.md")
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := os.ReadFile(path); err == nil {
			t.Fatal("mode 000 does not deny this user (root?); the probe cannot run")
		}
		if got, err := LoadSkills(t.TempDir()); err == nil {
			t.Fatalf("LoadSkills = %v, nil; want an error for an unreadable SKILL.md", got)
		}
	})
	t.Run("missing directories are not an error", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		skills, err := LoadSkills(t.TempDir())
		if err != nil || len(skills) != 0 {
			t.Fatalf("LoadSkills = %v, %v; want no skills and no error", skills, err)
		}
	})
}

// unreadableDir creates dir at mode 000 (restored at cleanup so t.TempDir
// can remove it) and fails when the mode does not deny this user.
func unreadableDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	if _, err := os.ReadDir(dir); err == nil {
		t.Fatal("mode 000 does not deny this user (root?); the probe cannot run")
	}
}
