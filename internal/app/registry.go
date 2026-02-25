package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Registry struct {
	Schema    string              `json:"schema"`
	UpdatedAt time.Time           `json:"updated_at"`
	Projects  []RegisteredProject `json:"projects"`
}

type RegisteredProject struct {
	Name      string    `json:"name"`
	RepoRoot  string    `json:"repo_root"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func loadRegistry(path string) (Registry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Registry{}, err
	}
	var r Registry
	if err := json.Unmarshal(b, &r); err != nil {
		return Registry{}, err
	}
	if r.Schema == "" {
		r.Schema = "dalek.registry.v0"
	}
	if r.Projects == nil {
		r.Projects = []RegisteredProject{}
	}
	return r, nil
}

func writeRegistryAtomic(path string, r Registry) error {
	if r.Schema == "" {
		r.Schema = "dalek.registry.v0"
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".registry.json.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("写入 registry 失败: %w", err)
	}
	return nil
}
