// Package pgdb — инфраструктура доступа к PostgreSQL: пул соединений и транзакция в контексте.
// Все PG-хранилища берут исполнителя запросов через Querier(ctx, db): внутри Transact это
// транзакция, снаружи — пул. Домен (kernel/*.go и корни модулей) этот пакет не импортирует.
package pgdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onixus/metis/internal/kernel"
)

// DBTX — минимальный исполнитель запросов; ему удовлетворяют *pgxpool.Pool и pgx.Tx.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// DB — пул соединений.
type DB struct {
	pool *pgxpool.Pool
}

// Open создаёт пул по URL (только из окружения, инвариант 11) и проверяет соединение.
func Open(ctx context.Context, url string) (*DB, error) {
	if url == "" {
		return nil, fmt.Errorf("%w: пустой URL базы данных", kernel.ErrValidation)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("pgdb open: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pgdb ping: %w", err)
	}
	return &DB{pool: pool}, nil
}

// FromPool оборачивает готовый пул.
func FromPool(pool *pgxpool.Pool) *DB { return &DB{pool: pool} }

// Pool возвращает пул (для миграций и healthcheck).
func (db *DB) Pool() *pgxpool.Pool { return db.pool }

// Close закрывает пул.
func (db *DB) Close() { db.pool.Close() }

type txKey struct{}

// Transact выполняет fn в транзакции; транзакция доступна через Querier(ctx, db).
// Вложенный вызов создаёт savepoint. Ошибка fn откатывает транзакцию и возвращается обёрнутой.
func (db *DB) Transact(ctx context.Context, fn func(ctx context.Context) error) error {
	var (
		tx  pgx.Tx
		err error
	)
	if outer, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		tx, err = outer.Begin(ctx)
	} else {
		tx, err = db.pool.Begin(ctx)
	}
	if err != nil {
		return fmt.Errorf("pgdb begin: %w", err)
	}
	// Also release the connection/savepoint if fn panics or its context expires.
	// Rollback after commit is harmless and returns pgx.ErrTxClosed.
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			return fmt.Errorf("pgdb rollback: %w (причина: %w)", rbErr, err)
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pgdb commit: %w", err)
	}
	return nil
}

// Querier возвращает транзакцию из контекста, а если её нет — пул.
func Querier(ctx context.Context, db *DB) DBTX {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return db.pool
}

// InTx сообщает, идёт ли в контексте транзакция.
func InTx(ctx context.Context) bool {
	_, ok := ctx.Value(txKey{}).(pgx.Tx)
	return ok
}

// MapError переводит ошибки PostgreSQL в доменные классы kernel.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %w", kernel.ErrNotFound, err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "40001", "40P01": // unique_violation, serialization_failure, deadlock_detected
			return fmt.Errorf("%w: %w", kernel.ErrConflict, err)
		case "23503", "23502", "23514": // foreign_key, not_null, check
			return fmt.Errorf("%w: %w", kernel.ErrValidation, err)
		}
	}
	return err
}
