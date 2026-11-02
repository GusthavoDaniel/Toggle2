# ---- Stage 1: Build (instalação de dependências) ----
# Usamos a imagem 'slim' (não alpine) porque psycopg2-binary e outras libs
# C-extension costumam dar dor de cabeça com musl libc do Alpine.
FROM python:3.11-slim AS builder

WORKDIR /app

# Dependências de sistema necessárias apenas para compilar/instalar pacotes
# (ex: alguns wheels ainda compilam bits nativos). Removidas no estágio final.
RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential \
        libpq-dev \
    && rm -rf /var/lib/apt/lists/*

# Copia só requirements primeiro para aproveitar cache de camada.
COPY requirements.txt .

# Força a instalação do python-dotenv direto no prefixo para não depender do cache do requirements
RUN pip install --no-cache-dir --prefix=/install python-dotenv==1.0.1 -r requirements.txt

# ---- Stage 2: Runtime ----
FROM python:3.11-slim

WORKDIR /app

# libpq5 é a lib de runtime do client PostgreSQL (psycopg2 precisa dela
# mesmo em runtime, mas não do libpq-dev nem do compilador).
RUN apt-get update && apt-get install -y --no-install-recommends \
        curl \
    && addgroup --system app && adduser --system --ingroup app app \
    && apt-get purge -y --allow-remove-essential --auto-remove perl-base \
    && rm -rf /var/lib/apt/lists/*
# Copia os pacotes Python já instalados do estágio de build.
COPY --from=builder /install /usr/local

# Copia o código-fonte da aplicação.
COPY . .

USER app

EXPOSE 8002

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD curl -f http://localhost:8002/health || exit 1

# 2 workers: serviço CRUD simples, não é o hot path do sistema.
# opentelemetry-instrument ativa a auto-instrumentação (Flask, requests, ...).
# Configuração via OTEL_* (OTEL_SERVICE_NAME, OTEL_EXPORTER_OTLP_ENDPOINT, ...).
CMD ["opentelemetry-instrument", "gunicorn", "--bind", "0.0.0.0:8002", "--workers", "2", "app:app"]