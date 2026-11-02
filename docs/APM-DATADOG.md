# APM e Tracing com OpenTelemetry + Datadog (Seção 3 do Tech Challenge Fase 4)

Documento de passagem: qualquer pessoa do grupo deve conseguir continuar esta parte lendo só este arquivo.
Nenhuma chave ou segredo está neste documento. Só os nomes de onde elas ficam.

## 1. O que existe

| Item | Onde | Estado |
|---|---|---|
| Instrumentação OTel dos 5 serviços (Go: auth, evaluation. Python: flag, targeting, analytics) | PR #9, branch `feat/otel-instrumentation`, repo `LuSntgo/Toggle2` | Pronto e com CI verde. **Não mergeado** (ver seção 5) |
| Propagação de trace pelo SQS (evaluation → analytics) | `evaluation-service/sqs.go` (envia `traceparent` em MessageAttributes) e `analytics-service/app.py` (lê `MessageAttributeNames=["All"]`) | No PR #9 |
| Collector OTel com exportador Datadog | Namespace `observability`, Deployment `otel-collector` | Aplicado à mão no cluster. Precisa entrar no GitOps (seção 4) |
| Chave do Datadog | Secret `datadog-secret`, chave `api-key`, namespace `observability` | Só no cluster. Nunca no Git |
| Variáveis `OTEL_*` nos Deployments | Repo `LuSntgo/togglemaster-gitops`, `apps/<servico>/deployment.yaml` | **Pendente** (seção 3) |

## 1.1 Estado do GitOps (verificado em 02/11) — adicionar só as variáveis OTEL_* NÃO basta

`togglemaster-gitops` tem os 5 `apps/<servico>/deployment.yaml`, todos no namespace `default`, mas ainda não está pronto para os serviços funcionarem:

| Serviço | Porta no GitOps | Porta real do serviço | Variáveis no GitOps |
|---|---|---|---|
| auth | 8080 | 8001 | parcial |
| flag | 8080 | 8002 | nenhuma |
| targeting | 8080 | 8003 | nenhuma |
| analytics | 5000 | 8005 | nenhuma |
| evaluation | 8004 | 8004 | via `envFrom` |

- Faltam os manifestos de **Service** dos 5 serviços.
- O GitOps não sobe Postgres, Redis nem o Collector.
- Variáveis de ambiente que cada serviço precisa (além das `OTEL_*`):
  - auth: `DATABASE_URL`, `MASTER_KEY`
  - flag e targeting: `DATABASE_URL` (banco de cada um), `AUTH_SERVICE_URL`
  - evaluation: `REDIS_URL`, `FLAG_SERVICE_URL`, `TARGETING_SERVICE_URL`, `SERVICE_API_KEY`, `AWS_SQS_URL`, `AWS_REGION`. Não usar `envFrom` com o Secret inteiro: referenciar só as chaves necessárias.
  - analytics: `AWS_REGION`, `AWS_SQS_URL`, `AWS_DYNAMODB_TABLE`
- Sem isso, os serviços sobem (ou nem sobem) com tracing desligado, e as evidências não podem ser geradas.

## 2. Arquitetura

```
5 microsserviços --OTLP gRPC :4317--> OTel Collector (ns observability) --> Datadog (APM)
                                                                       \-> Prometheus / Loki (seção 2 do desafio, a definir pelo grupo)
```

- Os serviços só conhecem o Collector. O Collector decide o destino. Trocar de APM não exige mexer no código.
- Go: `otelhttp` (servidor e cliente) + exportador `otlptracegrpc`. Python: `opentelemetry-instrument gunicorn` (auto-instrumentation).
- O span `sqs.send` leva o `traceparent` na mensagem, então o consumer do analytics continua o mesmo trace.

## 3. Variáveis OTEL (Deployments)

Em todos os serviços:

```yaml
- name: OTEL_EXPORTER_OTLP_ENDPOINT
  value: http://otel-collector.observability.svc.cluster.local:4317
- name: OTEL_EXPORTER_OTLP_PROTOCOL
  value: grpc
- name: OTEL_RESOURCE_ATTRIBUTES
  value: deployment.environment=prod,service.version=1.0.0
- name: OTEL_SERVICE_NAME
  value: <auth-service | flag-service | targeting-service | evaluation-service | analytics-service>
```

Só em flag, targeting e analytics (Python), acrescentar:

```yaml
- name: OTEL_METRICS_EXPORTER
  value: none
- name: OTEL_LOGS_EXPORTER
  value: none
```

`OTEL_SERVICE_NAME` tem que ser exatamente o nome do serviço, pois é o que aparece no Datadog.

Comportamento no código:
- Nos serviços Go (auth e evaluation), o tracing **só liga se `OTEL_EXPORTER_OTLP_ENDPOINT` estiver definida**. Sem ela o serviço roda normalmente, mas não gera traces. É o primeiro ponto a conferir se o Datadog não mostrar um dos dois.
- `OTEL_EXPORTER_OTLP_PROTOCOL` é ignorada nos serviços Go, que usam sempre gRPC. Ela só vale para os serviços Python.
- `OTEL_SERVICE_NAME` e `OTEL_RESOURCE_ATTRIBUTES` são lidos nos serviços Go por `resource.WithFromEnv()`. Nos Python, tudo é lido pelo `opentelemetry-instrument`.
- Hoje os `deployment.yaml` de `togglemaster-gitops` (namespace `default`) ainda **não têm nenhuma variável `OTEL_*`**.
`deployment.environment=prod` gera o filtro `env:prod`.

## 4. Collector (parte do Datadog)

O Collector oficial precisa enviar para Prometheus, Loki e Datadog. Deve existir **um único** Collector no GitOps (nome `otel-collector`, namespace `observability`).

Parte do Datadog, para juntar ao manifesto final:

- Imagem: `otel/opentelemetry-collector-contrib:0.164.0`
- Variáveis do contêiner: `DD_API_KEY` vinda do Secret `datadog-secret` (chave `api-key`, via `valueFrom.secretKeyRef`) e `DD_SITE=datadoghq.com`
- Receiver OTLP gRPC `:4317` e HTTP `:4318`. Processors `memory_limiter` (limit 400 MiB, spike 80 MiB) e `batch`
- Exportador `datadog` (`api.key: ${env:DD_API_KEY}`, `api.site: ${env:DD_SITE}`) e conector `datadog/connector`
- Pipelines: traces `[otlp] -> [datadog/connector, datadog]`. Metrics `[otlp, datadog/connector] -> [datadog]`

Criar o Secret (fora do Git, a chave vem da conta Datadog em Organization Settings > API Keys):

```
kubectl create secret generic datadog-secret -n observability --from-literal=api-key=<CHAVE>
kubectl rollout restart deployment/otel-collector -n observability
```

Se a chave for trocada, atualizar o Secret e reiniciar o Collector. Ao reiniciar, o log deve mostrar `API key validation successful`.

## 5. Merge do PR #9 (FEITO em 02/11, commit `23a8042`)

O merge foi feito antes de o cluster estar pronto, porque ninguém do grupo tinha começado a editar o GitOps (sem risco de conflito). Resultado: os 5 workflows da `main` ficaram verdes, as imagens foram publicadas no ECR da conta `186107385777` com a tag igual ao SHA do commit, e o bot atualizou a linha `image:` dos `deployment.yaml` em `togglemaster-gitops`. Isso também trocou o registry antigo (conta da Fase 3) pelo atual.

Ordem original prevista, mantida como referência dos pré-requisitos para os serviços funcionarem:

1. Cluster EKS e ArgoCD rodando, com os Deployments e Services dos 5 serviços no GitOps.
2. Variáveis `OTEL_*` nos Deployments (seção 3) e Collector oficial no ar (seção 4).
3. Merge do PR #9 em `main` do `LuSntgo/Toggle2` (feito).
4. Cada push na `main` dispara o CI: build, scan, push das imagens no ECR e atualização da imagem em `togglemaster-gitops` pelo bot. Os jobs de imagem e GitOps foram validados no merge do PR #9.

Pontos de atenção do CI:
- Login no ECR com chaves AWS estáticas, guardadas como secrets do repositório `LuSntgo/Toggle2` (usuário IAM `luiza_fiap`, da conta de laboratório). Se a pessoa que mantém essas chaves não estiver disponível, outra pessoa precisa criar novas e atualizar os secrets.
- O bot de GitOps usa o secret `GITOPS_GITHUB_TOKEN`. Se expirar, o job de atualização falha. É preciso gerar um novo token com acesso de escrita ao `togglemaster-gitops`.
- O `SERVICE_API_KEY` do evaluation-service só pode ser criado depois que o auth-service estiver rodando.

## 6. Como validar (foi o que fizemos no ensaio)

1. Chamar o `GET /evaluate` do evaluation-service **com a flag fora do cache**. O cache do evaluation é **por flag** (chave `flag_info:<flag>`, TTL de 30 segundos), não por usuário. Só a primeira chamada de uma flag, ou a primeira depois de 30 s sem uso, gera cache MISS e passa por flag, targeting e auth. Chamadas seguintes dão HIT e o trace fica só com o evaluation. Para gravar o vídeo, usar uma flag nova ou esperar mais de 30 s entre as chamadas.
2. Datadog > APM > Traces, filtro `env:prod service:evaluation-service`. Abrir o `sqs.send` ou um `GET` de 4 a 10 ms e olhar a aba Waterfall.
   Esperado: evaluation → targeting → auth e evaluation → flag → auth, mais o `sqs.send`.
3. Datadog > APM > Service Map (`https://app.datadoghq.com/apm/map`), Env `prod`. Esperado: os 5 serviços e as setas. Podem aparecer nós extras de borda tracejada: são serviços inferidos, não erro.
4. Para esconder o ruído dos healthchecks nos prints: `-resource_name:"GET /health"`.
5. Se o Datadog mostrar a tela de boas-vindas ("Install the Datadog Agent"), nada chegou. Diagnóstico sem alterar nada:
   - Log do Collector: `kubectl logs -n observability -l app=otel-collector --tail=40` (procurar `403`, `Exporting failed`)
   - Contadores: `kubectl port-forward -n observability deploy/otel-collector 8888:8888` e depois `curl http://localhost:8888/metrics`, procurando `receiver_accepted_spans` e `exporter_sent_spans`. Se o primeiro é maior que 0 e o segundo igual, os dados chegaram ao Datadog.
   - Conferir se a organização do Datadog em que se está logado é a da chave (Organization Settings > API Keys, os 4 últimos caracteres).

## 7. Datadog

- Conta de trial, site US1 (`datadoghq.com`), vence por volta de **16/11**. A entrega do grupo é 17/11. Gravar as evidências e o vídeo até **13/11**.
- Plano B: conta de estudante (GitHub Student Pack) aprovada em 01/11. Se for usada, é uma conta nova: criar nova API key, conferir o `DD_SITE` e atualizar o Secret (seção 4).
- Acesso de mais pessoas: convidar pelo menu da organização no Datadog.

## 8. Evidências para a entrega (cluster real, `env:prod`)

- [ ] Print de um trace distribuído de `/evaluate` (Waterfall expandido com os 5 níveis)
- [ ] Print do Service Map com os 5 serviços
- [ ] Trecho do vídeo: abrir o Service Map, fazer uma requisição de teste e mostrar o trace detalhado
- [ ] Opcional: trace do consumer do analytics ligado ao `sqs.send` pelo SQS
- [ ] Justificativa técnica no relatório: arquitetura OTel (Collector central, serviços desacoplados do backend) e escolha Datadog vs New Relic

## 9. Problemas conhecidos

- Script `db-init/multi-db/00-init-databases.sh` com final de linha CRLF quebra no Linux se o repo for clonado no Windows. Corrigir com `.gitattributes` (`*.sh text eol=lf`).
- Spans HTTP dos serviços Go aparecem com resource `GET` (sem o caminho). O caminho está no atributo `url.path`.
- Ao deixar de usar as chaves de CI no fim do projeto, desativar a access key do IAM.
