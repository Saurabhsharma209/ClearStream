# ClearStream Billing Architecture
## Designed for 1 Billion Calls/Day

---

## 1. The Numbers (Why This Is Hard)

| Metric | Value |
|--------|-------|
| Calls/day | 1,000,000,000 |
| Avg calls/sec (24h) | 11,574 |
| Peak calls/sec (8hr window) | ~34,722 |
| Peak concurrent channels | **~6.25 million** |
| Audio throughput at peak | **200 GB/s** |
| CDR records/day | 1 billion (256 GB raw) |
| CPU cores for spectral gate only | ~1,250 |
| CPU cores for RNNoise/DeepFilterNet | ~18,750 |

180 billion per-second pulse ticks/day is too much to store individually.
The billing system must aggregate at the edge — not in a central database.

---

## 2. Billing Model Recommendation

### Option A: Channel-Based (Capacity License)
- Charge per **reserved concurrent channel** (like a SIP trunk license)
- e.g., 10,000 channels × $X/month
- Good for: enterprises that want predictable OPEX, contract-based deals
- Bad for: doesn't reflect actual usage, incentivizes over-provisioning
- **Use for: enterprise tier with committed capacity**

### Option B: Pulse-Based (Consumption)
- Charge per **time interval consumed** per feature, per session
- Telecom standard granularity options and their trade-offs:

| Pulse | Billing ticks/day | Revenue vs 1s | Notes |
|-------|-------------------|---------------|-------|
| 1 sec | 180B | 1.000× | Max granularity, hardest to store |
| **8 sec** | **23B** | **1.022×** | **Sweet spot — telecom standard, 2.2% rounding uplift** |
| 15 sec | 12B | 1.000× | Common for short-call heavy traffic |
| 30 sec | 6B | 1.000× | Enterprise-friendly |
| 60 sec | 3B | 1.000× | Minimal storage, loses revenue on short calls |

**Recommendation: 6-second minimum + 1-second increments after that.**
This is the standard used by Twilio, Vonage, and most cloud telephony providers.
- A 2-second call → billed as 6 seconds
- A 7-second call → billed as 7 seconds
- A 3-minute call → billed as 180 seconds

### Option C: Hybrid (Recommended for Exotel/ClearStream)

```
Base platform fee  →  per channel-month (capacity commitment)
  +
Feature consumption  →  per second per active feature
  +
Eval/reporting tier  →  per 1,000 calls analyzed
```

---

## 3. Per-Feature Billing Tiers

### Feature Bitmask
Each session's CDR stores which features were active as a bitmask:

```
Bit 0 (0x01)  VAD          — Voice Activity Detection
Bit 1 (0x02)  SpectralGate — Lightweight noise gate (CPU-cheap)
Bit 2 (0x04)  RNNoise      — ML noise suppression (moderate CPU)
Bit 3 (0x08)  DeepFilter   — Deep learning model (high CPU)
Bit 4 (0x10)  AGC          — Automatic Gain Control
Bit 5 (0x20)  RTPMonitor   — Live quality monitoring
Bit 6 (0x40)  Eval         — Post-call metrics & config tuning
Bit 7 (0x80)  Reserved
```

### Pricing Tiers (Indicative)

| Tier | Features | Price/sec | Target Customer |
|------|----------|-----------|-----------------|
| Base | VAD + RTP | $0.000001 | High-volume OBD |
| Standard | + SpectralGate + AGC | $0.000004 | Contact center agents |
| Premium | + RNNoise | $0.000010 | Quality-critical (banking, healthcare) |
| Ultra | + DeepFilterNet | $0.000025 | Broadcast, video conferencing |
| Eval Add-on | Metrics + Tuning | $0.0005/call | QA teams |

At 1B calls/day × 180s × $0.000004 = **$720K/day** on Standard tier.

---

## 4. CDR (Call Detail Record) Schema

Every session emits exactly **one CDR** on call end.
No per-second writes to any database at this scale.

```go
// pkg/billing/cdr.go

type CDR struct {
    // Identity
    SessionID   string `json:"sid"`      // UUID v4, globally unique
    AccountID   string `json:"aid"`      // customer account
    TraceID     string `json:"tid"`      // correlation with RTP/SIP logs

    // Timing
    StartTS     int64  `json:"t0"`       // unix milliseconds
    EndTS       int64  `json:"t1"`       // unix milliseconds
    DurationMs  int64  `json:"dur"`      // t1 - t0

    // Billing units
    Features    uint8  `json:"feat"`     // bitmask (see above)
    PulseMs     int32  `json:"pulse"`    // configured pulse interval (ms)
    BilledUnits int32  `json:"units"`    // ceil(DurationMs / PulseMs)

    // Quality snapshot (for anomaly detection + chargeback defense)
    AvgLatencyMs float32 `json:"lat"`
    PacketLossPct float32 `json:"loss"`
    SNREstDB     float32 `json:"snr"`

    // Routing
    Region      string `json:"region"`  // us-east-1, ap-south-1, etc.
    NodeID      string `json:"node"`    // pod/host that processed the call

    // Error
    ErrorCode   int8   `json:"err"`     // 0=clean, 1=cancelled, 2=pipeline_error
}
```

**On-wire size: ~180 bytes/CDR compressed (protobuf).
At 1B CDRs/day: ~167 GB/day compressed — fully storable.**

---

## 5. Metering Architecture at 1B Scale

```
┌─────────────────────────────────────────────────────────┐
│  ClearStream SDK (pkg/billing)                           │
│                                                          │
│  ┌──────────────┐   call ends   ┌──────────────────────┐│
│  │ SessionMeter  │ ───────────► │ LocalWAL (append-only)││
│  │ (in-memory)   │              │ /var/clearstream/wal/ ││
│  └──────────────┘              └──────────┬─────────────┘│
│                                           │ flush (async) │
└───────────────────────────────────────────┼─────────────┘
                                            │
                               ┌────────────▼──────────────┐
                               │  Kafka / Pulsar            │
                               │  Topic: clearstream.cdrs   │
                               │  Partitioned by AccountID  │
                               │  Retention: 72h            │
                               └────────────┬──────────────┘
                                            │
                    ┌───────────────────────┼────────────────────────┐
                    │                       │                        │
          ┌─────────▼──────┐    ┌──────────▼──────┐    ┌────────────▼──────┐
          │  Flink Job:     │    │  Flink Job:      │    │  Flink Job:       │
          │  Real-time      │    │  Per-account     │    │  Fraud/Anomaly    │
          │  spend meter    │    │  hourly rollup   │    │  detection        │
          └─────────┬──────┘    └──────────┬──────┘    └────────────┬──────┘
                    │                       │                        │
          ┌─────────▼──────┐    ┌──────────▼──────┐    ┌────────────▼──────┐
          │  Redis          │    │  ClickHouse      │    │  Alert Manager    │
          │  spend:aid:hour │    │  (OLAP)          │    │  (PagerDuty/SNS)  │
          └─────────┬──────┘    └──────────┬──────┘    └───────────────────┘
                    │ rate limit            │ billing queries
          ┌─────────▼──────┐    ┌──────────▼──────┐
          │  API Guard      │    │  Billing Engine  │
          │  (real-time     │    │  (invoicing,     │
          │   spend limit)  │    │   rate cards)    │
          └────────────────┘    └─────────────────┘
```

### Why each component:

**LocalWAL (Write-Ahead Log):**
- CDR written to local disk before ACKing session end
- Survives pod crash/restart — Kafka push retried on recovery
- Append-only file, rotated every 10 minutes
- Critical: prevents revenue loss from node failures

**Kafka partitioned by AccountID:**
- All CDRs for one account go to same partition → ordered aggregation
- 1B CDRs/day at 200 bytes = 200 GB/day → needs ~10 Gbps throughput
- 100 partitions × 20 brokers handles this comfortably

**Redis (real-time spend meter):**
- Key: `spend:{account_id}:{YYYYMMDD_HH}` → integer (billed units)
- Updated by Flink within <1s of CDR arrival
- Used by API Gateway to enforce per-account spend caps
- Prevents a runaway loop from costing $1M before anyone notices

**ClickHouse (billing OLAP):**
- Partitioned by `toYYYYMM(StartTS)`, sorted by (AccountID, StartTS)
- Powers: invoices, usage dashboards, per-feature breakdowns
- Handles 1B row inserts/day trivially (ClickHouse is built for this)
- Query: "all Premium-tier seconds for account X in May" → <100ms

---

## 6. SDK-Level Metering (pkg/billing)

```go
// SessionMeter tracks a single call's billing state.
// Created at session start; Finalize() called at session end.
type SessionMeter struct {
    sessionID  string
    accountID  string
    features   uint8         // bitmask, set via EnableFeature()
    startMs    int64
    pulseMs    int32         // from account rate card
    walWriter  *WALWriter
}

// EnableFeature records that a feature was active in this session.
func (m *SessionMeter) EnableFeature(f Feature) { m.features |= uint8(f) }

// Finalize builds and WAL-writes the CDR. Non-blocking (async Kafka push).
func (m *SessionMeter) Finalize(quality QualitySnapshot) CDR

// WALWriter is a process-wide singleton.
// Batches CDRs and flushes to Kafka with at-least-once delivery.
type WALWriter struct {
    dir     string
    buf     []CDR
    mu      sync.Mutex
    flushCh chan struct{}
}
```

**Integration in pkg/rtp/session.go:**
```go
// At call setup:
meter := billing.NewSessionMeter(billing.MeterConfig{
    SessionID: s.id,
    AccountID: s.cfg.AccountID,
    Features:  billing.FeatureVAD | billing.FeatureSpectralGate,
    PulseMs:   6000,  // from account rate card
})
meter.EnableFeature(billing.FeatureRNNoise)

// At call teardown:
meter.Finalize(billing.QualitySnapshot{
    AvgLatencyMs:  stats.AvgLatencyMs,
    PacketLossPct: stats.LossPct,
})
```

---

## 7. Handling the 1B Scale — Key Principles

### Never write per-second to a database
At 1B calls × 180 seconds = 180B writes/day. No database survives this.
Aggregate in Flink; write rollups to ClickHouse every minute.

### Edge deduplication with idempotent CDR IDs
SessionID is a UUID generated at RTP session creation.
If a CDR arrives twice (network retry), Kafka consumer deduplicates via SessionID.
ClickHouse ReplacingMergeTree engine handles late duplicates.

### Backpressure from Redis spend caps
If an account hits their hourly cap, API Guard blocks new sessions immediately.
Redis TTL on the key is 2 hours — automatic reset, no cron needed.

### Regional independence
Each region (us-east, ap-south, eu-west) runs its own Kafka + Flink.
Global ClickHouse aggregation happens once/hour via cross-region replication.
No single point of failure for billing.

### WAL survives pod death
In Kubernetes: WAL directory is on a hostPath or PVC.
On pod restart, billing agent scans for unflushed WAL files and retries.
Kafka idempotent producer (acks=all, enable.idempotence=true) prevents doubles.

---

## 8. ClickHouse Schema

```sql
CREATE TABLE billing.cdr_v1
(
    session_id     String,
    account_id     String,
    region         LowCardinality(String),
    start_ts       DateTime64(3),           -- millisecond precision
    duration_ms    UInt32,
    features       UInt8,
    billed_units   UInt32,
    pulse_ms       UInt16,
    avg_latency_ms Float32,
    packet_loss_pct Float32,
    snr_est_db     Float32,
    error_code     Int8
)
ENGINE = ReplacingMergeTree()
PARTITION BY toYYYYMM(start_ts)
ORDER BY (account_id, start_ts, session_id)
TTL start_ts + INTERVAL 13 MONTH DELETE;

-- Hourly rollup materialized view (drives invoicing)
CREATE MATERIALIZED VIEW billing.hourly_mv
ENGINE = SummingMergeTree()
PARTITION BY toYYYYMM(hour)
ORDER BY (account_id, feature_name, hour)
AS SELECT
    account_id,
    arrayJoin(['vad','spectral','rnnoise','deepfilter','agc','monitor','eval']) AS feature_name,
    toStartOfHour(start_ts)                                                     AS hour,
    countIf(bitAnd(features, 1)  > 0)  AS vad_sessions,
    sumIf(billed_units, bitAnd(features, 2)  > 0) AS spectral_units,
    sumIf(billed_units, bitAnd(features, 4)  > 0) AS rnnoise_units,
    sumIf(billed_units, bitAnd(features, 8)  > 0) AS deepfilter_units,
    sumIf(billed_units, bitAnd(features, 16) > 0) AS agc_units,
    sum(billed_units)                               AS total_units
FROM billing.cdr_v1
GROUP BY account_id, hour;
```

---

## 9. Sprint 24 Scope — What to Build

| # | Component | File | Owner |
|---|-----------|------|-------|
| 1 | Feature bitmask + constants | `pkg/billing/feature.go` | API Layer |
| 2 | CDR struct + builder | `pkg/billing/cdr.go` | API Layer |
| 3 | SessionMeter | `pkg/billing/meter.go` | API Layer |
| 4 | LocalWAL writer | `pkg/billing/wal.go` | API Layer |
| 5 | Kafka CDR producer | `pkg/billing/producer.go` | API Layer |
| 6 | RTP session integration | `pkg/rtp/session.go` (hook) | RTP/SIP |
| 7 | Rate card interface | `pkg/billing/ratecard.go` | API Layer |
| 8 | Redis spend meter client | `pkg/billing/spendmeter.go` | API Layer |
| 9 | Billing tests | `pkg/billing/billing_test.go` | QA |
| 10 | ClickHouse schema migration | `deploy/clickhouse/` | API Layer |

Out of scope for this sprint (future): Flink jobs, billing UI, invoice generation.

---

## 10. Open Questions for Saurabh

1. **Pulse interval**: Start with 6-second minimum or go full 1-second (more complex)?
2. **Kafka vs direct HTTP**: Do we have Kafka already in Exotel infra, or should Sprint 24 use a simpler HTTP CDR forwarder first?
3. **ClickHouse vs existing data warehouse**: Does Exotel already use ClickHouse for CDRs? We should emit in the same schema.
4. **Rate card storage**: Redis + DB, or just DB with a cache layer?
5. **Spend caps**: Hard block (reject new sessions) or soft alert (allow with warning)?
6. **Channel vs consumption**: Exotel's existing customers — are they on channel licenses or per-minute/second already?
