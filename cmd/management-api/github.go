package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// GitHubConfig holds the configuration for GitHub integration.
type GitHubConfig struct {
	Token    string // Personal access token with repo scope
	Username string // GitHub username or org
	BaseURL  string // API base URL (defaults to https://api.github.com)
}

// GitHubConfigFromEnv reads GitHub configuration from environment variables.
func GitHubConfigFromEnv() *GitHubConfig {
	token := os.Getenv("GITOPS_GITHUB_TOKEN")
	username := os.Getenv("GITOPS_GITHUB_USERNAME")
	baseURL := os.Getenv("GITOPS_GITHUB_API_URL")
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	if token == "" || username == "" {
		return nil
	}
	return &GitHubConfig{Token: token, Username: username, BaseURL: baseURL}
}

// FleetRepoManager manages per-fleet Git repositories.
// Each fleet gets its own GitHub repo: github.com/<username>/fleet-<fleet-name>.
type FleetRepoManager struct {
	github   *GitHubConfig
	basePath string // Local directory for cloned repos (e.g. /tmp/gitops-repos)
	repos    map[string]*GitOpsRepo
	mu       sync.Mutex
}

// NewFleetRepoManager creates a manager for per-fleet GitHub repos.
func NewFleetRepoManager(github *GitHubConfig, basePath string) *FleetRepoManager {
	if basePath == "" {
		basePath = filepath.Join(os.TempDir(), "ingress-gitops-repos")
	}
	os.MkdirAll(basePath, 0o755)
	return &FleetRepoManager{
		github:   github,
		basePath: basePath,
		repos:    make(map[string]*GitOpsRepo),
	}
}

// repoName returns the GitHub repo name for a fleet.
func (m *FleetRepoManager) repoName(fleetName string) string {
	// Sanitize fleet name for GitHub repo naming
	name := strings.ToLower(fleetName)
	name = strings.ReplaceAll(name, " ", "-")
	// Remove anything that's not alphanumeric or hyphen
	cleaned := ""
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			cleaned += string(c)
		}
	}
	if cleaned == "" {
		cleaned = "fleet"
	}
	return "fleet-" + cleaned
}

// CreateFleetRepo creates a new GitHub repo for a fleet and clones it locally.
func (m *FleetRepoManager) CreateFleetRepo(fleetID, fleetName string) (*GitOpsRepo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	repoName := m.repoName(fleetName)

	// Create GitHub repo via API
	if err := m.createGitHubRepo(repoName, fmt.Sprintf("GitOps repo for fleet: %s", fleetName)); err != nil {
		return nil, fmt.Errorf("create github repo %s: %w", repoName, err)
	}

	// Clone locally
	localPath := filepath.Join(m.basePath, fleetID)
	remoteURL := fmt.Sprintf("https://%s:%s@github.com/%s/%s.git",
		m.github.Username, m.github.Token, m.github.Username, repoName)

	// Remove existing local dir if any
	os.RemoveAll(localPath)

	cmd := exec.Command("git", "clone", remoteURL, localPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		// Repo might be empty (just created), try init + add remote instead
		log.Printf("gitops: clone failed (likely empty repo), initializing locally: %s", strings.TrimSpace(string(out)))
		os.MkdirAll(localPath, 0o755)

		initCmd := exec.Command("git", "init")
		initCmd.Dir = localPath
		if out, err := initCmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("git init: %w: %s", err, string(out))
		}

		remoteCmd := exec.Command("git", "remote", "add", "origin", remoteURL)
		remoteCmd.Dir = localPath
		if out, err := remoteCmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("git remote add: %w: %s", err, string(out))
		}

		// Configure default branch
		branchCmd := exec.Command("git", "checkout", "-b", "main")
		branchCmd.Dir = localPath
		branchCmd.CombinedOutput() // ignore error if already on main
	}

	repo, err := NewGitOpsRepo(localPath)
	if err != nil {
		return nil, fmt.Errorf("init local repo: %w", err)
	}

	// Create initial directory structure
	for _, dir := range []string{"fleets", "routes", "lambdas"} {
		os.MkdirAll(filepath.Join(localPath, dir), 0o755)
	}

	// Write a README
	readme := fmt.Sprintf("# Fleet: %s\n\nGitOps repository for fleet `%s`.\nManaged by ingress-poc management-api.\n\n## Structure\n\n- `fleets/` — Fleet CRD manifests\n- `routes/` — Route CRD manifests\n- `lambdas/` — Lambda CRD manifests\n", fleetName, fleetID)
	repo.WriteManifest("README.md", []byte(readme))
	repo.CommitAndPush(fmt.Sprintf("Initialize fleet repo for %s", fleetName))

	m.repos[fleetID] = repo
	log.Printf("gitops: created GitHub repo %s/%s for fleet %s", m.github.Username, repoName, fleetID)
	return repo, nil
}

// GetFleetRepo returns the local GitOpsRepo for a fleet, cloning if needed.
func (m *FleetRepoManager) GetFleetRepo(fleetID, fleetName string) (*GitOpsRepo, error) {
	m.mu.Lock()
	if repo, ok := m.repos[fleetID]; ok {
		m.mu.Unlock()
		return repo, nil
	}
	m.mu.Unlock()

	// Try to clone existing repo
	repoName := m.repoName(fleetName)
	localPath := filepath.Join(m.basePath, fleetID)

	// Check if already cloned on disk
	if _, err := os.Stat(filepath.Join(localPath, ".git")); err == nil {
		repo, err := NewGitOpsRepo(localPath)
		if err != nil {
			return nil, err
		}
		m.mu.Lock()
		m.repos[fleetID] = repo
		m.mu.Unlock()
		return repo, nil
	}

	// Clone from GitHub
	remoteURL := fmt.Sprintf("https://%s:%s@github.com/%s/%s.git",
		m.github.Username, m.github.Token, m.github.Username, repoName)

	os.MkdirAll(localPath, 0o755)
	cmd := exec.Command("git", "clone", remoteURL, localPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("clone %s: %w: %s", repoName, err, string(out))
	}

	repo, err := NewGitOpsRepo(localPath)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.repos[fleetID] = repo
	m.mu.Unlock()
	return repo, nil
}

// DeleteFleetRepo removes the local clone for a fleet.
// GitHub repo deletion requires delete_repo scope which is intentionally not
// granted by default. The repo is left on GitHub for audit/recovery purposes.
func (m *FleetRepoManager) DeleteFleetRepo(fleetID, fleetName string) error {
	m.mu.Lock()
	delete(m.repos, fleetID)
	m.mu.Unlock()

	// Remove local clone
	localPath := filepath.Join(m.basePath, fleetID)
	os.RemoveAll(localPath)

	// Attempt to delete GitHub repo (will fail gracefully if token lacks delete_repo scope)
	repoName := m.repoName(fleetName)
	if err := m.deleteGitHubRepo(repoName); err != nil {
		log.Printf("gitops: could not delete GitHub repo %s (requires delete_repo scope): %v", repoName, err)
		log.Printf("gitops: please delete github.com/%s/%s manually if desired", m.github.Username, repoName)
	}
	return nil
}

// GetRepoURL returns the GitHub URL for a fleet's repo.
func (m *FleetRepoManager) GetRepoURL(fleetName string) string {
	repoName := m.repoName(fleetName)
	return fmt.Sprintf("https://github.com/%s/%s", m.github.Username, repoName)
}

// createGitHubRepo creates a new GitHub repository via the API.
func (m *FleetRepoManager) createGitHubRepo(name, description string) error {
	payload := map[string]interface{}{
		"name":        name,
		"description": description,
		"private":     false,
		"auto_init":   false,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", m.github.BaseURL+"/user/repos", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+m.github.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("github API request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 422 {
		// Repo might already exist
		if strings.Contains(string(respBody), "already exists") {
			log.Printf("gitops: GitHub repo %s already exists, reusing", name)
			return nil
		}
	}

	if resp.StatusCode != 201 && resp.StatusCode != 422 {
		return fmt.Errorf("github create repo: status %d: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("gitops: created GitHub repo %s/%s", m.github.Username, name)
	return nil
}

// deleteGitHubRepo deletes a GitHub repository via the API.
func (m *FleetRepoManager) deleteGitHubRepo(name string) error {
	url := fmt.Sprintf("%s/repos/%s/%s", m.github.BaseURL, m.github.Username, name)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+m.github.Token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("github API request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 204 || resp.StatusCode == 404 {
		log.Printf("gitops: deleted GitHub repo %s/%s", m.github.Username, name)
		return nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("github delete repo: status %d: %s", resp.StatusCode, string(respBody))
}

// ListFleetRepos lists all fleet repos the user owns on GitHub.
func (m *FleetRepoManager) ListFleetRepos() ([]map[string]string, error) {
	url := fmt.Sprintf("%s/user/repos?per_page=100&type=owner", m.github.BaseURL)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+m.github.Token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var repos []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&repos)

	var fleetRepos []map[string]string
	for _, r := range repos {
		name, _ := r["name"].(string)
		if strings.HasPrefix(name, "fleet-") {
			htmlURL, _ := r["html_url"].(string)
			desc, _ := r["description"].(string)
			fleetRepos = append(fleetRepos, map[string]string{
				"name":        name,
				"url":         htmlURL,
				"description": desc,
			})
		}
	}
	return fleetRepos, nil
}
