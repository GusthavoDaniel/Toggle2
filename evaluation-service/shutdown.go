package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"
)

// Somados, os prazos ficam abaixo dos 30s padrão de terminationGracePeriodSeconds.
const (
	serverShutdownTimeout = 15 * time.Second
	eventsDrainTimeout    = 5 * time.Second
	tracingFlushTimeout   = 5 * time.Second
)

// serveUntilSignal roda o servidor até ctx ser cancelado (SIGTERM/SIGINT) e então
// aguarda as requisições em andamento. Retorna nil em um encerramento normal.
func serveUntilSignal(ctx context.Context, srv *http.Server) error {
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	log.Println("Sinal de encerramento recebido; aguardando requisições em andamento.")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), serverShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	// ListenAndServe retorna ErrServerClosed após Shutdown; isso não é falha.
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// flushTracing exporta os spans pendentes dentro de um prazo limitado.
func flushTracing(shutdown func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), tracingFlushTimeout)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		log.Printf("Erro ao exportar os últimos spans: %v", err)
	}
}

// waitWithTimeout aguarda o WaitGroup, sem travar o encerramento do Pod. Retorna
// false se o prazo acabar antes.
func waitWithTimeout(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		log.Printf("Eventos SQS ainda em andamento após %s; encerrando mesmo assim.", timeout)
		return false
	}
}
