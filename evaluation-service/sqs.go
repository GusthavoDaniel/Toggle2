package main

import (
	"context"
	"encoding/json"
	"log"
	"path"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/sqs"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Evento que será enviado para a fila
type EvaluationEvent struct {
	UserID    string    `json:"user_id"`
	FlagName  string    `json:"flag_name"`
	Result    bool      `json:"result"`
	Timestamp time.Time `json:"timestamp"`
}

// queueName extrai o nome da fila da URL do SQS. A URL completa contém o ID da
// conta AWS, que não deve ir para o nome nem para os atributos do span.
func queueName(queueURL string) string {
	return path.Base(queueURL)
}

// sendEvaluationEvent envia um evento para a fila SQS
func (a *App) sendEvaluationEvent(ctx context.Context, userID, flagName string, result bool) {
	// Se a URL da fila não foi configurada, apenas loga localmente e sai.
	if a.SqsSvc == nil || a.SqsQueueURL == "" {
		log.Printf("[SQS_DISABLED] Evento: User '%s', Flag '%s', Result '%t'", userID, flagName, result)
		return
	}

	event := EvaluationEvent{
		UserID:    userID,
		FlagName:  flagName,
		Result:    result,
		Timestamp: time.Now().UTC(),
	}

	body, err := json.Marshal(event)
	if err != nil {
		log.Printf("Erro ao serializar evento SQS: %v", err)
		return
	}

	// Span de produtor + injeção do contexto (traceparent) nos atributos da
	// mensagem, para o analytics-service continuar o mesmo trace.
	queue := queueName(a.SqsQueueURL)
	ctx, span := otel.Tracer("evaluation-service").Start(ctx, "sqs.send "+queue,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(attribute.String("messaging.system", "aws_sqs"), attribute.String("messaging.destination.name", queue)))
	defer span.End()

	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	attrs := map[string]*sqs.MessageAttributeValue{}
	for k, v := range carrier {
		attrs[k] = &sqs.MessageAttributeValue{DataType: aws.String("String"), StringValue: aws.String(v)}
	}

	// Envia a mensagem
	_, err = a.SqsSvc.SendMessageWithContext(ctx, &sqs.SendMessageInput{
		MessageBody:       aws.String(string(body)),
		MessageAttributes: attrs,
		QueueUrl:          aws.String(a.SqsQueueURL),
	})
	if err != nil {
		markSpanError(ctx, err)
		log.Printf("Erro ao enviar mensagem para SQS: %v", err)
	} else {
		log.Printf("Evento de avaliação enviado para SQS (Flag: %s)", flagName)
	}
}