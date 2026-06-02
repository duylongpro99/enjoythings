# Product Requirements Document — Fintech Microservice Platform

**Version:** 1.0  
**Author:** Engineering  
**Status:** Draft  
**Last updated:** 2026-06-01

---

## 1. Overview

This document defines the product requirements for a fintech microservice platform built in Go. The platform provides core banking primitives — wallet management, double-entry ledger, KYC compliance, and payment processing — delivered as independently deployable services connected by an async event bus. An AI-powered fraud detection agent provides real-time transaction risk scoring.

The primary goal is a production-grade learning project that exercises every layer of modern microservice architecture: service communication, event-driven design, distributed transactions, container orchestration, and observability.

---

## 2. Goals

- Build a working, end-to-end fintech backend that handles money movement safely
- Practice real microservice patterns: gRPC, Kafka, saga, CQRS, event sourcing
- Integrate an AI agent (Claude API) as a first-class service in the event pipeline
- Deploy on Kubernetes with full observability (metrics, tracing, structured logs)
- Grow from a single runnable monolith to the full distributed system across four phases

---

## 3. Non-Goals

- Mobile or web frontend (API-only)
- Real money movement or integration with live payment rails
- Multi-region deployment or geo-redundancy
- PCI-DSS certification (this is a learning project)

---

## 4. Users & Personas

| Persona | Description |
|---|---|
| End user | A consumer with a wallet who initiates transfers and payments |
| Compliance officer | Reviews KYC status and flagged transactions |
| Platform engineer | Operates the services, monitors dashboards, responds to alerts |

---

## 5. Core Features

### 5.1 Wallet management
- Create and manage user wallets
- Read current balance (from Redis read model)
- Initiate transfers between wallets
- Enforce non-negative balance invariant

### 5.2 Double-entry ledger
- Record every money movement as a pair of debit/credit entries
- Append-only event store — no row updates, only new events
- CQRS: separate write path (event store) from read path (projected read model)
- Full audit trail for every wallet since account opening

### 5.3 KYC (Know Your Customer)
- User identity verification workflow with states: `pending → submitted → verified → rejected`
- Block transfers for unverified users
- Emit `kyc.verified` / `kyc.rejected` events to Kafka

### 5.4 Payment processing
- Process outbound payments via a payment processor service
- Idempotency keys on every payment request
- Retry with exponential backoff on transient failures
- Emit `payment.completed` / `payment.failed` events

### 5.5 Distributed transaction (saga)
- Coordinate multi-step payment flow without 2PC
- Saga steps: debit wallet → reserve ledger → call processor → confirm ledger → notify
- Compensating transactions on any step failure
- Outbox pattern to guarantee at-least-once event delivery

### 5.6 AI fraud detection agent
- Consume `tx.initiated` events from Kafka
- Score each transaction using the Claude API with transaction context
- Emit `fraud.flagged` events for high-risk transactions
- Store scoring history in TimescaleDB for trend analysis

### 5.7 Notifications
- Deliver SMS, email, and push notifications triggered by Kafka events
- Templated messages per event type

---

## 6. Quality Requirements

| Attribute | Target |
|---|---|
| API p99 latency | < 200ms for wallet reads |
| Payment saga completion | < 5s end-to-end under normal conditions |
| Fraud scoring latency | < 3s per transaction (async, does not block payment) |
| Uptime | 99.5% per service (learning target) |
| Test coverage | ≥ 80% on business logic packages |

---

## 7. Constraints

- Language: Go 1.22+
- Message broker: Apache Kafka
- Container runtime: Docker / Kubernetes
- All services must expose `/healthz` and `/readyz` endpoints
- All inter-service calls must carry OpenTelemetry trace context

---

## 8. Success Criteria

- A curl from client → API gateway → wallet service → Kafka → ledger service completes correctly
- A failed payment triggers compensating transactions and leaves balances unchanged
- A high-risk transaction is flagged by the AI agent and appears in the fraud dashboard
- All services are observable via Grafana with zero manual log-grepping needed to diagnose an issue

---

## 9. Out of scope for v1

- Multi-currency support
- Recurring payments / scheduled transfers
- Admin dashboard UI
- Webhook delivery to external systems
