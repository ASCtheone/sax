package nx

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Project represents a discovered NX project with its serve targets.
type Project struct {
	Name    string
	Root    string
	Targets []Target
}

// Target represents an executable target (serve, dev, start, etc.).
type Target struct {
	Name    string
	Command string
	Options map[string]interface{}
}

// Workspace represents a discovered NX workspace.
type Workspace struct {
	Root     string
	Projects []Project
}

// Discover scans the given directory (or current dir) for an NX workspace
// and returns all projects with their serve-like targets.
func Discover(dir string) (*Workspace, error) {
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("getwd: %w", err)
		}
	}

	// Walk up to find nx.json
	root := findNxRoot(dir)
	if root == "" {
		return nil, fmt.Errorf("no nx.json found in %s or parent directories", dir)
	}

	ws := &Workspace{Root: root}

	// Read nx.json for project defaults
	nxConfig, _ := readJSON(filepath.Join(root, "nx.json"))

	// Discover projects from project.json files
	projects, err := discoverProjects(root, nxConfig)
	if err != nil {
		return nil, err
	}

	ws.Projects = projects
	return ws, nil
}

// ServeTargets returns only projects that have serve-like targets.
func (ws *Workspace) ServeTargets() []Project {
	var result []Project
	for _, p := range ws.Projects {
		var targets []Target
		for _, t := range p.Targets {
			if isServeTarget(t.Name) {
				targets = append(targets, t)
			}
		}
		if len(targets) > 0 {
			proj := p
			proj.Targets = targets
			result = append(result, proj)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// NxCommand returns the nx run command for a project target.
func NxCommand(projectName, targetName string) []string {
	return []string{"npx", "nx", "run", projectName + ":" + targetName}
}

func findNxRoot(dir string) string {
	current := dir
	for {
		if _, err := os.Stat(filepath.Join(current, "nx.json")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

func discoverProjects(root string, nxConfig map[string]interface{}) ([]Project, error) {
	var projects []Project

	// Look for project.json files in apps/ and libs/ directories
	// Also scan for any project.json recursively (nx supports any structure)
	seen := make(map[string]bool)

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}

		// Skip node_modules, dist, .git, etc.
		if info.IsDir() {
			base := filepath.Base(path)
			if base == "node_modules" || base == ".git" || base == "dist" || base == ".nx" {
				return filepath.SkipDir
			}
			return nil
		}

		if info.Name() != "project.json" {
			return nil
		}

		projDir := filepath.Dir(path)
		rel, _ := filepath.Rel(root, projDir)
		if seen[rel] {
			return nil
		}
		seen[rel] = true

		proj, err := parseProjectJSON(path, rel, nxConfig)
		if err != nil {
			return nil // skip invalid project.json
		}
		if proj != nil {
			projects = append(projects, *proj)
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walk: %w", err)
	}

	// Also check package.json based projects (workspace.json / nx.json projects field)
	if nxProjects, ok := nxConfig["projects"].(map[string]interface{}); ok {
		for name, pathVal := range nxProjects {
			if seen[name] {
				continue
			}
			pathStr, ok := pathVal.(string)
			if !ok {
				continue
			}
			projPath := filepath.Join(root, pathStr, "project.json")
			if _, err := os.Stat(projPath); err == nil {
				proj, err := parseProjectJSON(projPath, pathStr, nxConfig)
				if err == nil && proj != nil {
					projects = append(projects, *proj)
					seen[name] = true
				}
			}
		}
	}

	sort.Slice(projects, func(i, j int) bool {
		return projects[i].Name < projects[j].Name
	})

	return projects, nil
}

func parseProjectJSON(path, relDir string, nxConfig map[string]interface{}) (*Project, error) {
	data, err := readJSON(path)
	if err != nil {
		return nil, err
	}

	name, _ := data["name"].(string)
	if name == "" {
		name = filepath.Base(relDir)
	}

	proj := &Project{
		Name: name,
		Root: relDir,
	}

	targets, ok := data["targets"].(map[string]interface{})
	if !ok {
		return proj, nil
	}

	for tName, tVal := range targets {
		tMap, ok := tVal.(map[string]interface{})
		if !ok {
			continue
		}

		target := Target{Name: tName}

		if executor, ok := tMap["executor"].(string); ok {
			target.Command = executor
		} else if command, ok := tMap["command"].(string); ok {
			target.Command = command
		}

		if opts, ok := tMap["options"].(map[string]interface{}); ok {
			target.Options = opts
		}

		proj.Targets = append(proj.Targets, target)
	}

	// Sort targets for deterministic output
	sort.Slice(proj.Targets, func(i, j int) bool {
		return proj.Targets[i].Name < proj.Targets[j].Name
	})

	return proj, nil
}

func readJSON(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func isServeTarget(name string) bool {
	lower := strings.ToLower(name)
	serveNames := []string{"serve", "dev", "start", "serve-ssr", "dev-server"}
	for _, s := range serveNames {
		if lower == s {
			return true
		}
	}
	return false
}
