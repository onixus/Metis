package economics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/onixus/metis/internal/identityaccess/authz"
	"github.com/onixus/metis/internal/kernel"
)

// ImportFile — файл, поданный на загрузку по расписанию (EC-01).
type ImportFile struct {
	Name string
	Data []byte
}

// FileSource — источник файлов для загрузки по расписанию. Работу с файловой системой
// выполняет адаптер: домен получает уже прочитанные файлы (инвариант 2).
type FileSource interface {
	Pending(ctx context.Context) ([]ImportFile, error)
}

// RunScheduledImports загружает файлы источника по шаблону (EC-01). Файл, уже загруженный
// раньше (совпадает SHA-256), пропускается: повторный запуск по расписанию не плодит версии.
func (s *Service) RunScheduledImports(ctx context.Context, sc authz.Scope, templateID kernel.ID, src FileSource) ([]ImportBatch, error) {
	if err := s.requireWrite(sc, kernel.NilID); err != nil {
		return nil, err
	}
	if src == nil {
		return nil, fmt.Errorf("%w: источник файлов не задан", kernel.ErrUnavailable)
	}
	files, err := src.Pending(ctx)
	if err != nil {
		return nil, fmt.Errorf("источник файлов: %w", err)
	}
	if len(files) == 0 {
		return nil, nil
	}
	history, err := s.store.Batches(ctx, Period{})
	if err != nil {
		return nil, fmt.Errorf("batches: %w", err)
	}
	loaded := make(map[string]bool, len(history))
	for _, b := range history {
		if b.Status == BatchApplied {
			loaded[b.SHA256] = true
		}
	}
	out := make([]ImportBatch, 0, len(files))
	for _, f := range files {
		sum := sha256.Sum256(f.Data)
		if loaded[hex.EncodeToString(sum[:])] {
			continue
		}
		res, err := s.ApplyImport(ctx, sc, ImportInput{TemplateID: templateID, FileName: f.Name,
			Data: f.Data, Scheduled: true})
		if err != nil {
			return out, fmt.Errorf("загрузка %q: %w", f.Name, err)
		}
		loaded[res.Batch.SHA256] = true
		out = append(out, res.Batch)
	}
	return out, nil
}
