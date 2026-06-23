# ADR-0007: NATS JetStream over Kafka for streaming

**Status:** Accepted
**Date:** 2026-04-30

## Context
We need durable streaming for: shard dispatch, worker heartbeats, OTLP fan-out from receiver to writers. Throughput envelope at v1: 10K msg/s peak, 1KB-50KB messages.

## Decision
Use **NATS JetStream 2.10**.

## Consequences
**+** 3-pod cluster fits in the same chart; no Zookeeper or KRaft operator complexity.
**+** Native subject hierarchy maps cleanly to our message types (`teo.shards.dispatch`, `teo.results.test_finished`, `teo.workers.heartbeat`).
**+** At-least-once delivery, durable consumers, and dead-letter subjects are first-class.
**+** Apache 2.0; aligns with our license posture.
**−** Smaller ecosystem than Kafka. We accept this; we are not building Kafka-Connect-style data integrations.
**−** Less battle-tested at multi-PB scale. Not a v1 concern.

**Note (v1.1):** the GraphQL subscriptions UI-hint subject `teo.ui.run_changed` (FR-706, ADR-0008) deliberately uses **core NATS, not JetStream** — it is published fire-and-forget and is *not* added to `EnsureStreams`. The hints are ephemeral and lossy by design: every API replica fans the message out in-process and re-reads authoritative state from Postgres, so a dropped hint self-heals and a persisted stream would only accumulate garbage and add ack latency.

## Alternatives considered
- **Kafka (Strimzi).** Stronger at scale but materially heavier ops. Rejected.
- **Redis Streams.** Less durable; consumer groups less ergonomic. Rejected.
- **Direct gRPC streaming with Postgres outbox.** Possible, but reinvents what JetStream gives us. Rejected.
