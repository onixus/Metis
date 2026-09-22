package financexlsx

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	economics "github.com/onixus/metis/internal/economics/modeling"
	"github.com/onixus/metis/internal/kernel"
)

// DirSource — каталог, из которого загрузка по расписанию забирает книги XLSX (EC-01).
// Файлы только читаются: платформа ничего не пишет в каталог выгрузок.
type DirSource struct {
	dir     string
	maxSize int64
}

// NewDirSource создаёт источник файлов над каталогом.
func NewDirSource(dir string) *DirSource { return &DirSource{dir: dir, maxSize: 32 << 20} }

var _ economics.FileSource = (*DirSource)(nil)

// Pending возвращает книги каталога в порядке имени.
func (d *DirSource) Pending(_ context.Context) ([]economics.ImportFile, error) {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return nil, fmt.Errorf("%w: каталог финансовых выгрузок: %w", kernel.ErrUnavailable, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".xlsx") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	out := make([]economics.ImportFile, 0, len(names))
	for _, name := range names {
		data, err := d.read(filepath.Join(d.dir, name))
		if err != nil {
			return nil, err
		}
		out = append(out, economics.ImportFile{Name: name, Data: data})
	}
	return out, nil
}

func (d *DirSource) read(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // путь собирается из каталога конфигурации, а не из запроса
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", kernel.ErrUnavailable, filepath.Base(path), err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, d.maxSize+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", kernel.ErrUnavailable, filepath.Base(path), err)
	}
	if int64(len(data)) > d.maxSize {
		return nil, fmt.Errorf("%w: %s", ErrFileTooLarge, filepath.Base(path))
	}
	return data, nil
}
