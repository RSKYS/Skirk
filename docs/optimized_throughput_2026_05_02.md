# Optimized Throughput 2026-05-02

Skirk was optimized for bulk throughput by changing the Drive+Sheets path from sequential per-chunk operations to:

- parallel Drive uploads;
- one batched Sheets append for all control rows in a transfer;
- one Sheets read for receive;
- parallel Drive downloads;
- cleanup by returned Drive file IDs instead of name lookups;
- batched Sheets tombstones for cleanup;
- larger default bulk chunk size.

The client still uses one config file. The relevant config fields are:

```json
{
  "tunnel": {
    "chunk_size": 1048576,
    "concurrency": 8
  }
}
```

## Direct Google APIs

Route:

```text
direct
```

Best result:

```text
32 MiB payload, 1 MiB chunks, concurrency 16
send       60.227 Mbps
receive    99.358 Mbps
round trip 37.498 Mbps
```

Full direct results:

| Payload | Chunk | Concurrency | Chunks | Send | Receive | Cleanup | Send Mbps | Receive Mbps | Round Trip Mbps |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 MiB | 1 MiB | 8 | 1 | 1.489s | 0.585s | 0.571s | 5.632 | 14.316 | 4.042 |
| 1 MiB | 4 MiB | 8 | 1 | 1.113s | 0.466s | 0.829s | 7.532 | 17.973 | 5.308 |
| 8 MiB | 1 MiB | 8 | 8 | 1.890s | 1.345s | 0.787s | 35.493 | 49.892 | 20.739 |
| 8 MiB | 4 MiB | 8 | 2 | 2.349s | 1.515s | 0.911s | 28.559 | 44.271 | 17.360 |
| 32 MiB | 1 MiB | 16 | 32 | 4.457s | 2.701s | 0.988s | 60.227 | 99.358 | 37.498 |
| 32 MiB | 4 MiB | 16 | 8 | 4.572s | 2.950s | 0.519s | 58.701 | 90.990 | 35.682 |

Previous best before optimization:

```text
1 MiB payload, 256 KiB chunks
round trip 1.264 Mbps
```

The optimized direct path is roughly 29.7x better by round-trip Mbps in the best measured case.

## Direct Google-Fronted APIs

Route:

```text
google_front_pinned without SOCKS proxy
TLS/SNI host: www.google.com
HTTP Host: Google API hosts
Pinned IP: 216.239.38.120
```

Best result:

```text
32 MiB payload, 1 MiB chunks, concurrency 16
send       61.221 Mbps
receive    91.444 Mbps
round trip 36.670 Mbps
```

Direct fronting is essentially the same order of throughput as direct non-fronted API access.

## Restricted Google-Fronted Path

Route:

```text
socks5h://127.0.0.1:1080
google_front_pinned
```

Stable results:

| Payload | Chunk | Concurrency | Chunks | Send | Receive | Cleanup | Send Mbps | Receive Mbps | Round Trip Mbps |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 MiB | 1 MiB | 1 | 1 | 53.465s | 7.203s | 1.540s | 0.157 | 1.164 | 0.138 |
| 4 MiB | 4 MiB | 1 | 1 | 202.456s | 20.559s | 2.286s | 0.166 | 1.632 | 0.150 |

Higher concurrency on the restricted path caused EOFs during large Drive uploads through the SOCKS proxy, so the practical restricted setting is conservative upload concurrency.

## Cleanup

Temporary benchmark spreadsheets were deleted. Drive cleanup queries found no leftover Skirk-shaped chunk objects after cleanup; one unrelated `data.z11` file was intentionally left untouched.

## Interpretation

The confirmed maximum from this machine to Google APIs is now about:

```text
37 Mbps round trip
60 Mbps send
99 Mbps receive
```

The confirmed restricted Google-fronted path is still limited mostly by the user-provided SOCKS path and upload behavior, not by the Skirk protocol.

## Learning Notes

This is the expected object-store pattern: batching the control plane and parallelizing the data plane changes the scaling curve. Before optimization, every chunk paid independent Drive and Sheets latency. After optimization, Sheets is one append/read per transfer and Drive requests run concurrently.

## Why This Matters

The one-config model stays intact, but Skirk now has a real bulk mode. For restricted networks, the next step is adaptive mode selection: low upload concurrency for fragile SOCKS paths, higher download concurrency where stable, and large chunk sizes for bulk transfers.
