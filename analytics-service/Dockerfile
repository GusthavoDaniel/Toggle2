# ---- Stage 1: Build (instalação de dependências) ----
# Este serviço não usa PostgreSQL (só boto3/SQS/DynamoDB), então não
# precisa de build-essential nem libpq-dev — instalação bem mais leve.
FROM python:3.11-slim AS builder

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir --prefix=/install -r requirements.txt

# ---- Stage 2: Runtime ----
FROM python:3.11-slim

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
        curl \
    && addgroup --system app && adduser --system --ingroup app app \
    && apt-get purge -y --allow-remove-essential --auto-remove perl-base \
    && rm -rf /var/lib/apt/lists/*
    
COPY --from=builder /install /usr/local
COPY . .

USER app

EXPOSE 8005

HEALTHCHECK --interval=15s --timeout=5s --start-period=5s --retries=3 \
  CMD curl -f http://localhost:8005/health || exit 1

# IMPORTANTE: --workers 1 é proposital.
# O worker SQS (thread que consome a fila) é iniciado no nível do módulo
# (start_worker() roda assim que o app.py é importado), fora do
# 'if __name__ == "__main__"'. Isso significa que CADA worker process do
# gunicorn vai disparar sua própria thread consumindo a MESMA fila SQS.
# Com 1 worker, garantimos um único consumidor por Pod -- o que é o
# comportamento esperado, já que a escalabilidade real deste serviço
# acontece via réplicas de Pods (HPA/KEDA), não via processos gunicorn.
# opentelemetry-instrument ativa a auto-instrumentação (Flask, requests, ...).
# Configuração via OTEL_* (OTEL_SERVICE_NAME, OTEL_EXPORTER_OTLP_ENDPOINT, ...).
CMD ["opentelemetry-instrument", "gunicorn", "--bind", "0.0.0.0:8005", "--workers", "1", "--timeout", "120", "app:app"]
