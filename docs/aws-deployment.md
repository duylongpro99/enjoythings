# EnjoyThings — AWS Deployment Architecture

Target reference architecture for deploying the EnjoyThings platform (Next.js web, FastAPI + LangGraph Python app, Go microservices, Kafka, PostgreSQL/TimescaleDB, observability stack) to AWS.

> **Interactive explorer** (click any service → instance type, subnet/AZ, security-group rules, and a beginner "why"): [`aws-explorer.html`](./aws-explorer.html).
> Overview architecture diagram: [`aws-infra.html`](./aws-infra.html) (generated with archify).

> Region used throughout as an example: **`ap-southeast-1`** (Singapore), AZs `1a` / `1b` / `1c`. Swap for your own. Instance types and CIDRs are sensible starting points — right-size against real load.

---

## 1. Overview

The platform is a container-first, event-driven system with three code surfaces (see `CLAUDE.md`):

- **web** — Next.js frontend (Vercel AI SDK, SSR + streaming chat)
- **app** — Python: FastAPI chat gateway (SSE) + LangGraph fraud worker + shared LLM adapter
- **services** — Go microservices: `gateway`, `wallet`, `ledger`, `verification`, `saga-orchestrator`, `payment-processor`, `notification`, `stub-payment-rail`

Because the repo already ships a Helm chart (`services/charts/enjoythings`) and Kubernetes manifests (`services/k8s`), the recommended runtime is **Amazon EKS**. ECS Fargate is a valid simpler alternative and is called out per component where it changes the trade-off.

### High-level flow on AWS

```
Users
  │  HTTPS
  ▼
CloudFront ──► S3 (Next.js static assets)
  │
  ▼
ALB (public)  ──►  EKS
                    ├─ web            (Next.js SSR pod)
                    ├─ FastAPI chat   (SSE)
                    ├─ gateway (REST/JWT) ──gRPC──► wallet / ledger / verification / saga
                    ├─ payment-processor ─► stub-payment-rail
                    ├─ notification
                    └─ fraud worker (LangGraph)
                          │
       events/outbox/DLQ  ▼
                    Amazon MSK (Kafka)
                          │
   ┌──────────────────────┼───────────────────────────┐
   ▼                      ▼                             ▼
Aurora PostgreSQL   TimescaleDB (fraud audit)   Amazon Bedrock (LLM)
(per-service schemas)  RDS PostgreSQL

Cross-cutting: ECR (images) · Secrets Manager (.env/keys) · Cognito (JWT/OIDC) ·
AMP + Amazon Managed Grafana + X-Ray/ADOT (observability) · IAM/VPC/WAF
```

---

## 2. Component → AWS service mapping

| Project component | AWS service | Alternative |
|---|---|---|
| Next.js web (SSR + static) | **CloudFront + S3** (static/ISR) fronting **EKS** SSR pods (or **Amplify Hosting**) | ECS Fargate service |
| FastAPI chat (SSE, `app/main.py`) | **Amazon EKS** (Fargate/EC2 nodes) | ECS Fargate |
| Fraud worker — LangGraph (`app/fraud`) | **Amazon EKS** deployment (Kafka consumer) | ECS Fargate |
| Shared LLM adapter (`app/llm`) → LLM provider | **Amazon Bedrock** (via VPC endpoint) | Keep external OpenAI-compatible endpoint |
| Go services: gateway, wallet, ledger, verification, saga, processor, notification, stub-rail | **Amazon EKS** deployments/services | ECS Fargate |
| North-south ingress (REST/JWT :8080, SSE) | **Application Load Balancer** + AWS Load Balancer Controller | API Gateway (HTTP API) |
| East-west gRPC between services | **EKS ClusterIP + Kubernetes DNS** (optionally App Mesh / service mesh) | ECS Service Connect |
| PostgreSQL (per-service schemas) | **Amazon Aurora PostgreSQL** | RDS for PostgreSQL |
| TimescaleDB (fraud audit) | **RDS for PostgreSQL** + Timescale extension (self-managed) | Timescale Cloud / EC2 |
| Apache Kafka (events, outbox, DLQ) | **Amazon MSK** (or MSK Serverless) | Confluent Cloud |
| Container images | **Amazon ECR** | — |
| Secrets: `.env`, API keys, DB creds | **AWS Secrets Manager** (+ SSM Parameter Store for config) | External Secrets Operator → Secrets Manager |
| Auth / JWT (HS256/RS256) issuer | **Amazon Cognito** (OIDC, RS256 JWKS) | Keep existing issuer; verify JWKS in gateway |
| Traces (Jaeger / OTLP) | **AWS X-Ray** via ADOT Collector, or **Managed Grafana + Tempo** | Self-host Jaeger on EKS |
| Metrics (Prometheus) | **Amazon Managed Service for Prometheus (AMP)** | Self-host Prometheus |
| Dashboards (Grafana) | **Amazon Managed Grafana** | Self-host Grafana |
| CI/CD (existing GitHub Actions) | **GitHub Actions → ECR/EKS** (OIDC role) | CodePipeline + CodeBuild |
| Networking / isolation | **VPC** (private subnets, NAT), **Security Groups**, **VPC endpoints** | — |
| Edge protection | **AWS WAF + Shield** on CloudFront/ALB | — |
| TLS certificates | **AWS Certificate Manager (ACM)** | — |

---

## 3. Why these services

### Compute — Amazon EKS (primary)
The repo already defines a Helm chart and k8s manifests, so EKS reuses those artifacts with minimal change and keeps the local `kind` workflow and production runtime identical. It handles the mixed workload cleanly: long-lived HTTP/gRPC servers, an SSE streaming endpoint, and a Kafka-consumer fraud worker that scales on lag — all with one autoscaling and deployment model. **Pick ECS Fargate instead** if the team prefers not to operate Kubernetes; the container images are unchanged, but you lose direct Helm reuse and fine-grained mesh control.

### Frontend — CloudFront + S3 (+ EKS/Amplify)
Static assets and ISR output belong on S3 behind CloudFront for low-latency global delivery and caching, cutting load off the origin. SSR and streaming chat still need a Node runtime, served from an EKS pod (or Amplify Hosting for a fully managed path). CloudFront also gives one place for WAF and TLS at the edge.

### Messaging — Amazon MSK
The design centers on Kafka (outbox pattern, per-topic DLQs, the saga event flow, `fraud.score.requested`). MSK is managed Apache Kafka, so existing producers/consumers and topic semantics work unchanged — no client rewrite. MSK Serverless removes broker capacity planning; provisioned MSK is cheaper at steady high throughput. Confluent Cloud is the alternative if you want schema registry + governance out of the box.

### Databases — Aurora PostgreSQL + RDS (TimescaleDB)
Each Go service owns a schema in a shared PostgreSQL; Aurora PostgreSQL gives that a highly available, auto-scaling storage backend with fast failover and read replicas while staying wire-compatible. The fraud audit store uses TimescaleDB features (hypertables), which Aurora does not support — so it runs on **RDS for PostgreSQL** with the Timescale extension, or Timescale Cloud, keeping time-series audit queries first-class and isolated from the transactional path.

### LLM — Amazon Bedrock
`app/llm` is an OpenAI-compatible adapter, so the LLM backend is swappable. Bedrock keeps model traffic inside the VPC (private endpoint), removes a third-party data-egress path for sanitized fraud prompts, and consolidates billing/governance under IAM. Keep the external OpenAI-compatible endpoint if a specific model is required that Bedrock does not host.

### Observability — AMP + Managed Grafana + X-Ray/ADOT
Services already emit Prometheus metrics and OTLP traces and render in Grafana. AMP and Amazon Managed Grafana are drop-in managed replacements for the self-hosted stack (same PromQL, same dashboards), and the ADOT Collector forwards OTLP to X-Ray (or Tempo) — preserving the existing instrumentation while removing the operational burden of running Prometheus/Jaeger/Grafana yourself.

### Security & config — Secrets Manager, Cognito, IAM, VPC, WAF, ACM
`.env` and API keys must never be committed (per `CLAUDE.md`); Secrets Manager stores them with rotation and injects via IRSA/External Secrets, with SSM Parameter Store for non-secret config. Cognito can act as the RS256 OIDC issuer the gateway already verifies (JWKS), or you keep the current issuer. Private subnets + security groups + VPC endpoints keep east-west gRPC and data traffic off the public internet; WAF/Shield protect the edge; ACM manages TLS.

### Registry & CI/CD — ECR + GitHub Actions
ECR is the private image registry co-located with EKS (fast pulls, IAM-scoped, scan-on-push). The existing GitHub Actions workflow (`.github`) extends naturally: assume an AWS role via OIDC (no long-lived keys), push to ECR, and roll out to EKS — keeping the current CI while adding deployment.

---

## 4. Network design — VPC, subnets, availability zones

**Not the default VPC.** The default VPC puts everything in public subnets on a flat network — fine for a demo, wrong for a payment system. We build a **custom VPC** and split it into three subnet tiers, each replicated across three AZs, so a zone failure is survivable and databases have no path to the internet.

### VPC & subnet plan (`10.0.0.0/16`)

| Tier | Purpose | Route for `0.0.0.0/0` | AZ 1a | AZ 1b | AZ 1c |
|---|---|---|---|---|---|
| **Public** | ALB, NAT Gateways | → Internet Gateway | `10.0.0.0/24` | `10.0.1.0/24` | `10.0.2.0/24` |
| **Private-app** | EKS worker nodes / pods | → NAT Gateway | `10.0.16.0/20` | `10.0.32.0/20` | `10.0.48.0/20` |
| **Private-data** | Aurora, RDS, MSK | *(none — local + VPC endpoints only)* | `10.0.64.0/24` | `10.0.65.0/24` | `10.0.66.0/24` |

- **Availability Zone** = isolated data center(s) with independent power/network. Using 3 means one zone can fail without downtime.
- **Subnet** = an IP range bound to exactly one AZ. Grouping subnets by job (public/app/data) is what lets us apply different routing and firewalling per tier.
- **Private-app** subnets get large `/20` ranges because each pod consumes a VPC IP (via the AWS VPC CNI); `/24` (256 IPs) would run out.
- **Private-data** subnets have **no default route** — the databases literally cannot reach or be reached from the internet.

### Gateways & routing
- **Internet Gateway (IGW)** — attached to the VPC; only public subnets route to it.
- **NAT Gateway** — one per AZ (prod) so private pods get *outbound-only* internet (package pulls, external APIs) with no inbound exposure. A single shared NAT is fine for dev to cut cost.
- **VPC endpoints** keep AWS-service traffic off the internet and reduce NAT charges:
  - Gateway endpoint (free): **S3**
  - Interface endpoints: **ECR** (`api` + `dkr`), **Secrets Manager**, **`bedrock-runtime`**, **STS**, **CloudWatch Logs**, **Elastic Load Balancing**

---

## 5. Security groups (per-service, least privilege)

A **security group (SG)** is a stateful allow-list attached to a resource — it names exactly who may connect and on which port (replies are allowed automatically). Each service gets its own SG (via *security groups for pods* / IRSA), so the blast radius of any one service is tiny.

**Ports:** web `3000` · chat `8000` · gateway `8080` · core gRPC `8081–8084` · Postgres `5432` · MSK IAM `9098` (TLS `9094`) · endpoints `443`.

| Security group | Ingress (who may connect in) | Egress (who it may reach out to) |
|---|---|---|
| `sg-alb` | `443` from CloudFront origin-facing prefix list | `3000`→`sg-web`, `8000`→`sg-chat`, `8080`→`sg-gateway` |
| `sg-web` | `3000` from `sg-alb` | `8000`→`sg-chat`, `443`→`sg-vpce`/NAT |
| `sg-chat` | `8000` from `sg-web` | `443`→`sg-vpce` (Bedrock) |
| `sg-gateway` | `8080` from `sg-alb` | `8081-8084`→`sg-core`, `443`→`sg-vpce` (Cognito JWKS, Secrets) |
| `sg-core` | `8081-8084` from `sg-gateway` **and** `sg-fraud` | `5432`→`sg-aurora`, `9098`→`sg-msk`, `443`→`sg-vpce` |
| `sg-processor` | *(consumer — none)* | `9098`→`sg-msk`, stub-rail, `443`→`sg-vpce` |
| `sg-notification` | *(consumer — none)* | `9098`→`sg-msk`, `443`→`sg-vpce` |
| `sg-fraud` | *(consumer — none)* | `9098`→`sg-msk`, `8081-8084`→`sg-core`, `5432`→`sg-timescale`, `443`→`sg-vpce` (Bedrock) |
| `sg-aurora` | `5432` from `sg-core` **only** | — |
| `sg-timescale` | `5432` from `sg-fraud` **only** | — |
| `sg-msk` | `9098` from `sg-core`, `sg-processor`, `sg-notification`, `sg-fraud` | — |
| `sg-vpce` | `443` from the app SGs | — |

Two things worth noticing for a beginner: (1) **Kafka consumers** (processor, notification, fraud) have **no ingress rule at all** — they pull work, nothing connects *to* them. (2) The databases allow **exactly one** caller each; the ledger is unreachable from the internet, the frontend, or even most backend services.

---

## 6. Compute sizing (instances, nodes, pods)

### EKS
- **Control plane** — fully managed by AWS, multi-AZ (no instances to run).
- **Managed node group** — `m7g.large` (Graviton, 2 vCPU / 8 GiB) × **3–6**, one+ node per AZ; `t4g.medium` for dev. On-demand for stateful, optional **Spot** for stateless pods.
- **Topology spread constraints** so each Deployment lands pods in all 3 AZs.
- **HPA** on CPU for request-serving pods; **KEDA** on MSK consumer lag for the fraud worker.

### Pod requests → limits

| Service | Requests | Limits | Replicas | Notes |
|---|---|---|---|---|
| Next.js Web | 250m / 512Mi | 500m / 1Gi | 2 | static/ISR offloaded to S3+CloudFront |
| FastAPI Chat | 250m / 512Mi | 500m / 1Gi | 2 | SSE; egress only to Bedrock |
| API Gateway | 500m / 512Mi | 1 / 1Gi | 3 | busiest hop; JWT verify + gRPC fan-out |
| Wallet / Ledger / Verification | 250m / 512Mi | 500m / 1Gi | 2 each | gRPC `8081/8082/8083` |
| Saga | 500m / 1Gi | 1 / 2Gi | 2 | holds compensation state, gRPC `8084` |
| Payment Processor | 500m / 512Mi | 1 / 1Gi | 2 | + stub-rail pod (100m / 128Mi) |
| Notification | 100m / 256Mi | 250m / 512Mi | 2 | lightest workload |
| Fraud Worker | 500m / 1Gi | 1 / 2Gi | 2 → 8 | KEDA-scaled on Kafka lag; LLM context needs memory |

### Data tier

| Store | Recommended | Topology | Alternative |
|---|---|---|---|
| Aurora PostgreSQL | **Serverless v2**, 0.5–4 ACU | writer 1a + reader 1b, auto-failover | `db.r6g.large` provisioned |
| RDS TimescaleDB | `db.m6g.large` (2 vCPU / 8 GiB), gp3, Multi-AZ | primary 1a + standby 1b | `db.t4g.medium` (dev) |
| Amazon MSK | **MSK Serverless** | brokers 1a/1b/1c, RF = 3 | `kafka.m7g.large` × 3 provisioned |

Starting with the **serverless** options (Aurora v2, MSK Serverless) means you don't have to guess capacity up front — they scale with load and cost little when idle. Move to provisioned classes once steady-state load is known and cheaper.

---

## 7. Well-Architected trade-offs

- **Security** — private-by-default (only the ALB is public); least-privilege SGs; IRSA + Secrets Manager (no long-lived keys, `.env` never committed); KMS at rest, TLS in transit; WAF/Shield at the edge; Cognito RS256.
- **Reliability** — 3 AZs; Multi-AZ DB failover; MSK RF=3 + per-topic DLQ; HPA/KEDA autoscaling.
- **Performance Efficiency** — Graviton (`m7g`/`m6g`); CloudFront edge caching; serverless data tiers auto-scale; right-sized pod requests pack nodes densely.
- **Cost Optimization** — pay-per-use serverless data; VPC endpoints + free S3 gateway cut NAT charges; Spot for stateless pods; single NAT in dev, per-AZ NAT in prod.
- **Operational Excellence** — Terraform/`eksctl` IaC; GitHub Actions → ECR/EKS via OIDC; one observability plane (OTLP → AMP + Managed Grafana + X-Ray via ADOT).
- **Sustainability** — Graviton (more work per watt); serverless/autoscaling releases idle capacity.

---

## 8. Environment & rollout notes

- **Environments:** separate AWS accounts (or at least VPCs) for `dev`/`staging`/`prod`; promote the same immutable ECR image tag across them.
- **Scaling:** HPA on CPU/latency for HTTP/gRPC services; scale the fraud worker on **MSK consumer lag** (KEDA).
- **Data safety:** Aurora + RDS automated backups and PITR; MSK multi-AZ; store nothing sensitive in logs (extracted text + metadata only, per `CLAUDE.md`).
- **Cost levers:** start with MSK Serverless + Fargate for low ops, move to provisioned MSK + EC2 node groups as steady load grows.

---

*Diagram (`aws-infra.html`) generated with archify — see section 1.*
