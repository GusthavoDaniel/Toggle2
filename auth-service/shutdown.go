package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"
)

// Somados, os prazos ficam abaixo dos 30s padrão de terminationGracePeriodSeconds.
const (
	serverShutdownTimeout = 15 * time.Second
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
