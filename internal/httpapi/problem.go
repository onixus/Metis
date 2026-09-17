// Package httpapi — HTTP-слой платформы: chi, обработчики, сгенерированные из api/openapi.yaml
// интерфейсы (NF-M04). Авторизация выполняется в домене через authz.Scope; здесь только
// аутентификация и маппинг ошибок в application/problem+json.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/onixus/metis/internal/httpapi/gen"
	"github.com/onixus/metis/internal/kernel"
	"github.com/onixus/metis/internal/portfoliograph"
)

const problemContentType = "application/problem+json"

// problemFor переводит доменную ошибку в Problem и HTTP-статус.
func problemFor(err error) (int, any) {
	var cycle *portfoliograph.CycleError
	if errors.As(err, &cycle) {
		detail := "связь создаёт цикл в графе зависимостей фич"
		ids := make([]gen.FeatureId, 0, len(cycle.Path))
		ids = append(ids, cycle.Path...)
		return http.StatusConflict, gen.CycleProblem{
			Type: "urn:metis:problem:feature-cycle", Title: "Цикл зависимостей", Status: http.StatusConflict, Detail: &detail, Cycle: ids,
		}
	}
	var ve *kernel.ValidationError
	if errors.As(err, &ve) {
		msg := ve.Message
		field := ve.Field
		return http.StatusBadRequest, gen.Problem{Type: "urn:metis:problem:validation", Title: "Некорректные данные", Status: http.StatusBadRequest, Detail: &msg, Field: &field}
	}
	switch {
	case errors.Is(err, kernel.ErrValidation):
		msg := err.Error()
		return http.StatusBadRequest, gen.Problem{Type: "urn:metis:problem:validation", Title: "Некорректные данные", Status: http.StatusBadRequest, Detail: &msg}
	case errors.Is(err, kernel.ErrForbidden):
		return http.StatusForbidden, gen.Problem{Type: "urn:metis:problem:forbidden", Title: "Доступ запрещён", Status: http.StatusForbidden}
	case errors.Is(err, kernel.ErrNotFound):
		return http.StatusNotFound, gen.Problem{Type: "urn:metis:problem:not-found", Title: "Не найдено", Status: http.StatusNotFound}
	case errors.Is(err, kernel.ErrConflict):
		msg := err.Error()
		return http.StatusConflict, gen.Problem{Type: "urn:metis:problem:conflict", Title: "Конфликт", Status: http.StatusConflict, Detail: &msg}
	case errors.Is(err, kernel.ErrUnavailable):
		return http.StatusServiceUnavailable, gen.Problem{Type: "urn:metis:problem:unavailable", Title: "Внешняя система недоступна", Status: http.StatusServiceUnavailable}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return 499, gen.Problem{Type: "urn:metis:problem:canceled", Title: "Запрос отменён", Status: 499}
	}
	return http.StatusInternalServerError, gen.Problem{Type: "urn:metis:problem:internal", Title: "Внутренняя ошибка", Status: http.StatusInternalServerError}
}

func writeProblem(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// responseErrorHandler — обработчик ошибок strict-сервера: домен → problem+json.
func responseErrorHandler(log *slog.Logger) func(w http.ResponseWriter, r *http.Request, err error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		status, body := problemFor(err)
		if status >= 500 {
			log.ErrorContext(r.Context(), "внутренняя ошибка обработчика", "path", r.URL.Path, "err", err)
		}
		writeProblem(w, status, body)
	}
}

// requestErrorHandler — ошибки разбора запроса (тело, параметры).
func requestErrorHandler(w http.ResponseWriter, _ *http.Request, err error) {
	msg := err.Error()
	writeProblem(w, http.StatusBadRequest, gen.Problem{Type: "urn:metis:problem:bad-request", Title: "Некорректный запрос", Status: http.StatusBadRequest, Detail: &msg})
}
