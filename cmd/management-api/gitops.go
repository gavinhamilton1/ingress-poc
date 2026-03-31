package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// GitOpsRepo manages a local Git repository used for GitOps-driven K8s deployments.
// Manifests are written to the local clone; CommitAndPush stages, commits, and pushes
// changes so that Argo CD (or a similar controller) can reconcile them.
type GitOpsRepo struct {
	repoPath string
	mu       sync.Mutex
}

// NewGitOpsRepo validates the repo path exists (creating it if necessary) and
// initialises a Git repo there if one does not already exist.
func NewGitOpsRepo(repoPath string) (*GitOpsRepo, error) {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("resolve repo path: %w", err)
	}

	// Ensure the directory exists.
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create repo directory %s: %w", abs, err)
	}

	// If .git does not exist, initialise a new repo.
	gitDir := filepath.Join(abs, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		cmd := exec.Command("git", "init")
		cmd.Dir = abs
		out, initErr := cmd.CombinedOutput()
		if initErr != nil {
			return nil, fmt.Errorf("git init in %s: %w: %s", abs, initErr, string(out))
		}
		log.Printf("gitops: initialised new git repo at %s", abs)
	}

	return &GitOpsRepo{repoPath: abs}, nil
}

// WriteManifest writes content to repoPath/relativePath, creating intermediate
// directories as needed.
func (g *GitOpsRepo) WriteManifest(relativePath string, content []byte) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	full := filepath.Join(g.repoPath, relativePath)
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", full, err)
	}
	return nil
}

// DeleteManifest removes a file at repoPath/relativePath.
// Returns nil if the file does not exist.
func (g *GitOpsRepo) DeleteManifest(relativePath string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	full := filepath.Join(g.repoPath, relativePath)
	err := os.Remove(full)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete %s: %w", full, err)
	}
	return nil
}

// ReadManifest reads and returns the contents of a file at repoPath/relativePath.
func (g *GitOpsRepo) ReadManifest(relativePath string) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	full := filepath.Join(g.repoPath, relativePath)
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", full, err)
	}
	return data, nil
}

// CommitAndPush stages all changes, commits with the given message, and pushes.
// If there is nothing to commit the function returns nil.
// If push fails (e.g. no remote configured) a warning is logged but no error is returned,
// supporting local-only development workflows.
func (g *GitOpsRepo) CommitAndPush(message string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Stage all changes.
	addCmd := exec.Command("git", "add", "-A")
	addCmd.Dir = g.repoPath
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add -A: %w: %s", err, string(out))
	}

	// Check if there is anything to commit.
	statusCmd := exec.Command("git", "diff", "--cached", "--quiet")
	statusCmd.Dir = g.repoPath
	if err := statusCmd.Run(); err == nil {
		// Exit code 0 means no staged changes.
		return nil
	}

	// Commit.
	commitCmd := exec.Command("git", "commit", "-m", message)
	commitCmd.Dir = g.repoPath
	// Set a default author if GIT_AUTHOR_NAME is not set to avoid failures in
	// environments where git user.name/email are not configured.
	commitCmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=management-api",
		"GIT_AUTHOR_EMAIL=management-api@ingress-poc.local",
		"GIT_COMMITTER_NAME=management-api",
		"GIT_COMMITTER_EMAIL=management-api@ingress-poc.local",
	)
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %w: %s", err, string(out))
	}

	// Push (best-effort).
	pushCmd := exec.Command("git", "push")
	pushCmd.Dir = g.repoPath
	if out, err := pushCmd.CombinedOutput(); err != nil {
		outStr := string(out)
		// Tolerate missing remote or push failures in local dev.
		if strings.Contains(outStr, "No configured push destination") ||
			strings.Contains(outStr, "does not appear to be a git repository") ||
			strings.Contains(outStr, "fatal:") {
			log.Printf("gitops: push skipped (no remote configured or push failed): %s", strings.TrimSpace(outStr))
			return nil
		}
		return fmt.Errorf("git push: %w: %s", err, outStr)
	}

	return nil
}

// CommitFiles stages only the given relative paths and commits. This is safer
// than CommitAndPush when you know exactly which files changed and want to
// avoid accidentally staging deletions caused by a stale working tree.
// Falls back to CommitAndPush (git add -A) when paths is empty.
func (g *GitOpsRepo) CommitFiles(paths []string, message string) error {
	if len(paths) == 0 {
		return g.CommitAndPush(message)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// First reset the index so we start clean (no pre-staged deletions etc.)
	resetCmd := exec.Command("git", "reset", "HEAD")
	resetCmd.Dir = g.repoPath
	resetCmd.CombinedOutput() // ignore errors (might be on an empty/fresh repo)

	// Stage only the specific files.
	addArgs := append([]string{"add", "--"}, paths...)
	addCmd := exec.Command("git", addArgs...)
	addCmd.Dir = g.repoPath
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add %v: %w: %s", paths, err, string(out))
	}

	// Nothing to commit?
	statusCmd := exec.Command("git", "diff", "--cached", "--quiet")
	statusCmd.Dir = g.repoPath
	if err := statusCmd.Run(); err == nil {
		return nil
	}

	commitCmd := exec.Command("git", "commit", "-m", message)
	commitCmd.Dir = g.repoPath
	commitCmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=management-api",
		"GIT_AUTHOR_EMAIL=management-api@ingress-poc.local",
		"GIT_COMMITTER_NAME=management-api",
		"GIT_COMMITTER_EMAIL=management-api@ingress-poc.local",
	)
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %w: %s", err, string(out))
	}

	pushCmd := exec.Command("git", "push")
	pushCmd.Dir = g.repoPath
	if out, err := pushCmd.CombinedOutput(); err != nil {
		outStr := string(out)
		if strings.Contains(outStr, "No configured push destination") ||
			strings.Contains(outStr, "does not appear to be a git repository") ||
			strings.Contains(outStr, "fatal:") {
			log.Printf("gitops: push skipped (no remote or push failed): %s", strings.TrimSpace(outStr))
			return nil
		}
		return fmt.Errorf("git push: %w: %s", err, outStr)
	}
	return nil
}

// EnsureDirectory creates the directory at repoPath/relativePath if it does not exist.
func (g *GitOpsRepo) EnsureDirectory(relativePath string) error {
	full := filepath.Join(g.repoPath, relativePath)
	return os.MkdirAll(full, 0o755)
}

// RepoPath returns the absolute path to the Git repository.
func (g *GitOpsRepo) RepoPath() string {
	return g.repoPath
}

// RecentCommits returns recent git log entries as structured data.
// Each entry contains sha, message, author, and date fields.
func (g *GitOpsRepo) RecentCommits(limit int) ([]map[string]string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if limit <= 0 {
		limit = 20
	}

	cmd := exec.Command("git", "log",
		fmt.Sprintf("--max-count=%d", limit),
		"--format=%H|||%s|||%an|||%aI",
	)
	cmd.Dir = g.repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Empty repo or no commits yet.
		outStr := strings.TrimSpace(string(out))
		if strings.Contains(outStr, "does not have any commits") || strings.Contains(outStr, "bad default revision") {
			return []map[string]string{}, nil
		}
		return nil, fmt.Errorf("git log: %w: %s", err, outStr)
	}

	var commits []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|||", 4)
		if len(parts) < 4 {
			continue
		}
		commits = append(commits, map[string]string{
			"sha":     parts[0],
			"message": parts[1],
			"author":  parts[2],
			"date":    parts[3],
		})
	}
	return commits, nil
}

// EnsureMultiRegionDirs creates the directory structure for multiple clusters:
//
//	clusters/<name>/fleets/
//	clusters/<name>/routes/
func (g *GitOpsRepo) EnsureMultiRegionDirs(clusterNames []string) error {
	for _, name := range clusterNames {
		for _, sub := range []string{"fleets", "routes"} {
			dir := filepath.Join(g.repoPath, "clusters", name, sub)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create dir %s: %w", dir, err)
			}
		}
	}
	return nil
}

// Pull does a git pull --ff-only on the repo. Best-effort: if no remote is
// configured or the pull fails for a non-fatal reason (e.g. already up to date,
// no configured upstream), the error is logged and nil is returned.
func (g *GitOpsRepo) Pull() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	cmd := exec.Command("git", "pull", "--ff-only")
	cmd.Dir = g.repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		outStr := strings.TrimSpace(string(out))
		// Tolerate common non-fatal cases.
		if strings.Contains(outStr, "no tracking information") ||
			strings.Contains(outStr, "There is no tracking information") ||
			strings.Contains(outStr, "does not appear to be a git repository") ||
			strings.Contains(outStr, "fatal:") ||
			strings.Contains(outStr, "Already up to date") {
			log.Printf("gitops: pull skipped for %s: %s", g.repoPath, outStr)
			return nil
		}
		return fmt.Errorf("git pull: %w: %s", err, outStr)
	}
	return nil
}

// ListManifests returns the names of YAML files in the given directory (relative
// to the repo root). Returns an empty slice if the directory does not exist.
func (g *GitOpsRepo) ListManifests(relativeDir string) ([]string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	full := filepath.Join(g.repoPath, relativeDir)
	entries, err := os.ReadDir(full)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("read directory %s: %w", full, err)
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
			files = append(files, name)
		}
	}
	return files, nil
}
