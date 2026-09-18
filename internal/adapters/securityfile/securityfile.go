// Package securityfile — файловый адаптер порта SecurityPipeline (CM-09):
// читает манифесты артефактов пайплайна из каталога. Содержимое файлов — недоверенный ввод:
// значения читаются как строки, размер файла ограничен, ничего не исполняется.
package securityfile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/ports"
)

// DefaultMaxFileSize — лимит размера одного манифеста.
const DefaultMaxFileSize int64 = 16 << 20

// ErrFileTooLarge — файл превышает лимит.
var ErrFileTooLarge = errors.New("securityfile: файл превышает лимит размера")

// Adapter читает манифесты из каталога: по файлу на прогон пайплайна.
type Adapter struct {
	dir     string
	maxSize int64
}

// New создаёт адаптер над каталогом манифестов.
func New(dir string) *Adapter { return &Adapter{dir: dir, maxSize: DefaultMaxFileSize} }

var _ ports.SecurityPipeline = (*Adapter)(nil)

// manifest — формат файла: проект и список артефактов прогона.
type manifest struct {
	Project   string `json:"project"`
	Artifacts []struct {
		Kind       string `json:"kind"`
		Tool       string `json:"tool"`
		Title      string `json:"title"`
		URI        string `json:"uri"`
		SHA256     string `json:"sha256"`
		ProducedAt string `json:"produced_at"`
		Components []struct {
			Key     string `json:"key"`
			Version string `json:"version"`
		} `json:"components"`
	} `json:"artifacts"`
}

// Artifacts возвращает артефакты проекта, произведённые не раньше since.
func (a *Adapter) Artifacts(_ context.Context, project string, since time.Time) ([]ports.SecurityArtifact, error) {
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		return nil, fmt.Errorf("%w: каталог пайплайна: %w", kernel.ErrUnavailable, err)
	}
	out := make([]ports.SecurityArtifact, 0)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		m, err := a.read(filepath.Join(a.dir, e.Name()))
		if err != nil {
			return nil, err
		}
		if project != "" && m.Project != project {
			continue
		}
		for _, art := range m.Artifacts {
			produced := time.Time{}
			if art.ProducedAt != "" {
				produced, err = time.Parse(time.RFC3339, art.ProducedAt)
				if err != nil {
					return nil, fmt.Errorf("%w: %s: дата артефакта: %w", kernel.ErrValidation, e.Name(), err)
				}
			}
			if !since.IsZero() && produced.Before(since) {
				continue
			}
			components := make([]ports.SecurityComponent, 0, len(art.Components))
			for _, c := range art.Components {
				components = append(components, ports.SecurityComponent{Key: c.Key, Version: c.Version})
			}
			out = append(out, ports.SecurityArtifact{
				Kind: art.Kind, Tool: art.Tool, Title: art.Title, URI: art.URI, SHA256: art.SHA256,
				ProducedAt: produced.UTC(), Components: components,
			})
		}
	}
	return out, nil
}

func (a *Adapter) read(path string) (manifest, error) {
	f, err := os.Open(path) //nolint:gosec // путь собирается из каталога конфигурации, а не из запроса
	if err != nil {
		return manifest{}, fmt.Errorf("%w: %s: %w", kernel.ErrUnavailable, filepath.Base(path), err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return manifest{}, fmt.Errorf("%w: %s: %w", kernel.ErrUnavailable, filepath.Base(path), err)
	}
	if info.Size() > a.maxSize {
		return manifest{}, fmt.Errorf("%w: %s", ErrFileTooLarge, filepath.Base(path))
	}
	raw, err := io.ReadAll(io.LimitReader(f, a.maxSize))
	if err != nil {
		return manifest{}, fmt.Errorf("%w: %s: %w", kernel.ErrUnavailable, filepath.Base(path), err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return manifest{}, fmt.Errorf("%w: %s: %w", kernel.ErrValidation, filepath.Base(path), err)
	}
	return m, nil
}
