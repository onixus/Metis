// Package ports — интерфейсы портов ядра к внешним системам (ТЗ 4.1).
package ports

import (
	"context"
	"time"
)

// SecurityComponent — компонент из SBOM.
type SecurityComponent struct {
	Key     string
	Version string
}

// SecurityArtifact — артефакт пайплайна безопасности: отчёт SAST, SCA, DAST, фаззинга или SBOM.
// Поля принадлежат пайплайну и доступны только на чтение (инвариант 4).
type SecurityArtifact struct {
	Kind       string // sast | sca | dast | fuzz | sbom
	Tool       string
	Title      string
	URI        string
	SHA256     string
	ProducedAt time.Time
	Components []SecurityComponent
}

// SecurityPipeline — порт пайплайна безопасности (ТЗ 4.1, CM-09).
// Платформа в пайплайн ничего не пишет.
type SecurityPipeline interface {
	// Artifacts возвращает артефакты проекта, произведённые не раньше since.
	Artifacts(ctx context.Context, project string, since time.Time) ([]SecurityArtifact, error)
}
