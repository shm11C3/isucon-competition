package isureal

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v5"
)

// Writes commit with synchronous_commit=local so that row locks are released
// without waiting for the standbys. Before a write response is sent, the
// request waits on a shared barrier: one short transaction committed with
// remote_apply, whose commit record follows every earlier local commit, so when
// it returns both standbys have applied those writes as well. Requests are
// batched onto a bounded number of concurrent barriers.

const applyBarrierMaxConcurrent = 8
const applyBarrierMinInterval = 10 * time.Millisecond
const applyBarrierTimeout = 5 * time.Minute

type applyBarrier struct {
	db        *sqlx.DB
	mu        sync.Mutex
	waiters   []chan error
	running   int
	lastStart time.Time
	timerSet  bool
}

// newApplyBarrier uses its own small pool whose sessions commit with
// remote_apply, so a barrier is a single autocommit statement.
func newApplyBarrier(dsn string) (*applyBarrier, error) {
	db, err := sqlx.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(applyBarrierMaxConcurrent)
	db.SetMaxIdleConns(applyBarrierMaxConcurrent)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return &applyBarrier{db: db}, nil
}

// Wait returns once every standby has applied all WAL committed before the call.
func (b *applyBarrier) Wait(ctx context.Context) error {
	ch := make(chan error, 1)
	b.mu.Lock()
	b.waiters = append(b.waiters, ch)
	b.maybeStartLocked()
	b.mu.Unlock()
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *applyBarrier) maybeStartLocked() {
	if len(b.waiters) == 0 || b.running >= applyBarrierMaxConcurrent {
		return
	}
	if b.running > 0 {
		if wait := applyBarrierMinInterval - time.Since(b.lastStart); wait > 0 {
			if !b.timerSet {
				b.timerSet = true
				time.AfterFunc(wait, func() {
					b.mu.Lock()
					b.timerSet = false
					b.maybeStartLocked()
					b.mu.Unlock()
				})
			}
			return
		}
	}
	batch := b.waiters
	b.waiters = nil
	b.running++
	b.lastStart = time.Now()
	go b.run(batch)
}

func (b *applyBarrier) run(batch []chan error) {
	err := b.commitBarrier()
	for _, ch := range batch {
		ch <- err
	}
	b.mu.Lock()
	b.running--
	b.maybeStartLocked()
	b.mu.Unlock()
}

func (b *applyBarrier) commitBarrier() error {
	ctx, cancel := context.WithTimeout(context.Background(), applyBarrierTimeout)
	defer cancel()
	// A transactional WAL record in an autocommit statement: the session has
	// synchronous_commit=remote_apply, so this returns once both standbys applied it.
	_, err := b.db.ExecContext(ctx, `SELECT pg_logical_emit_message(true, 'apply_barrier', '')`)
	return err
}

type bufferedResponseWriter struct {
	header http.Header
	status int
	body   []byte
}

func (w *bufferedResponseWriter) Header() http.Header { return w.header }

func (w *bufferedResponseWriter) WriteHeader(code int) { w.status = code }

func (w *bufferedResponseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.body = append(w.body, b...)
	return len(b), nil
}

// ApplyBarrierMiddleware holds back the response of every non-GET request until
// the standbys have applied the writes it committed.
func ApplyBarrierMiddleware(b *applyBarrier) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if b == nil || c.Request().Method == http.MethodGet {
				return next(c)
			}
			resp, err := echo.UnwrapResponse(c.Response())
			if err != nil {
				return next(c)
			}
			real := resp.ResponseWriter
			buf := &bufferedResponseWriter{header: real.Header()}
			resp.ResponseWriter = buf
			restored := false
			restore := func() {
				if !restored {
					resp.ResponseWriter = real
					restored = true
				}
			}
			defer restore()
			err = next(c)
			restore()
			if !resp.Committed {
				// Nothing was written (error path): the error handler writes to the real writer.
				return err
			}
			if waitErr := b.Wait(c.Request().Context()); waitErr != nil && c.Request().Context().Err() == nil {
				c.Logger().Error("apply barrier failed", "error", waitErr)
			}
			real.WriteHeader(buf.status)
			if len(buf.body) > 0 {
				if _, writeErr := real.Write(buf.body); writeErr != nil {
					return writeErr
				}
			}
			return err
		}
	}
}
